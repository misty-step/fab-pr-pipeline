package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"
)

// CircuitBreaker tracks per-PR failures and skips PRs that repeatedly fail.
// After N consecutive failures, the circuit opens and the PR is skipped for M runs.
// This prevents one bad PR from consuming the entire error budget.
type CircuitBreaker struct {
	mu sync.RWMutex

	// prURL -> consecutive failure count
	failures map[string]int
	// prURL -> remaining skip runs when circuit is open
	skipsRemaining map[string]int

	// Config
	failureThreshold int // N: failures before opening circuit
	skipRuns         int // M: runs to skip when circuit is open
}

// NewCircuitBreaker creates a new circuit breaker with the given thresholds.
func NewCircuitBreaker(failureThreshold, skipRuns int) *CircuitBreaker {
	return &CircuitBreaker{
		failures:         make(map[string]int),
		skipsRemaining:   make(map[string]int),
		failureThreshold: failureThreshold,
		skipRuns:         skipRuns,
	}
}

// RecordFailure increments the failure count for a PR.
// If failures reach the threshold, the circuit opens.
func (cb *CircuitBreaker) RecordFailure(prURL string) {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	cb.failures[prURL]++
	if cb.failures[prURL] >= cb.failureThreshold {
		// Circuit opens - only log on transition
		if cb.skipsRemaining[prURL] == 0 {
			cb.skipsRemaining[prURL] = cb.skipRuns
			fmt.Fprintf(os.Stderr, "[circuit-breaker] OPENED for %s (after %d consecutive failures, skipping for %d runs)\n", prURL, cb.failures[prURL], cb.skipRuns)
		}
	}
}

// RecordSuccess clears the failure count for a PR.
// If the circuit was open, logs recovery.
func (cb *CircuitBreaker) RecordSuccess(prURL string) {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	if cb.failures[prURL] > 0 {
		delete(cb.failures, prURL)
	}
	if cb.skipsRemaining[prURL] > 0 {
		delete(cb.skipsRemaining, prURL)
		fmt.Fprintf(os.Stderr, "[circuit-breaker] CLOSED for %s (recovered after success)\n", prURL)
	}
}

// IsOpen returns true if the circuit is open for this PR (should be skipped).
// Decrements the skip counter each time it's checked.
func (cb *CircuitBreaker) IsOpen(prURL string) bool {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	if remaining := cb.skipsRemaining[prURL]; remaining > 0 {
		cb.skipsRemaining[prURL]--
		if cb.skipsRemaining[prURL] == 0 {
			// Circuit will close after this skip - reset failures so next error doesn't immediately reopen
			delete(cb.failures, prURL)
			fmt.Fprintf(os.Stderr, "[circuit-breaker] CLOSED for %s (skip period expired, will retry)\n", prURL)
		}
		return true
	}
	return false
}

type searchPR struct {
	URL       string    `json:"url"`
	Title     string    `json:"title"`
	Body      string    `json:"body"`
	UpdatedAt time.Time `json:"updatedAt"`
	IsDraft   bool      `json:"isDraft"`
	Number    int       `json:"number"`
	Author    struct {
		Login string `json:"login"`
	} `json:"author"`
	Repository struct {
		NameWithOwner string `json:"nameWithOwner"`
	} `json:"repository"`
	Labels []label `json:"labels"`
}

type label struct {
	Name string `json:"name"`
}

type prView struct {
	ID                string              `json:"id"`
	URL               string              `json:"url"`
	Title             string              `json:"title"`
	Body              string              `json:"body"`
	IsDraft           bool                `json:"isDraft"`
	Mergeable         string              `json:"mergeable"`
	ReviewDecision    string              `json:"reviewDecision"`
	MergeStateStatus  string              `json:"mergeStateStatus"`
	StatusCheckRollup []statusRollupEntry `json:"statusCheckRollup"`
	Author            struct {
		Login string `json:"login"`
	} `json:"author"`
	Labels []label `json:"labels"`
}

type statusRollupEntry struct {
	Typename   string `json:"__typename"`
	Name       string `json:"name"`
	Context    string `json:"context"`
	Status     string `json:"status"`     // CheckRun
	Conclusion string `json:"conclusion"` // CheckRun
	State      string `json:"state"`      // StatusContext
}

type runOutput struct {
	Ok         bool        `json:"ok"`
	Error      string      `json:"error,omitempty"`
	StartedAt  string      `json:"startedAt"`
	Org        string      `json:"org"`
	MaxPRs     int         `json:"maxPRs"`
	StaleHours int         `json:"staleHours"`
	DryRun     bool        `json:"dryRun"`
	Discord    *discordOut `json:"discord,omitempty"`
	Results    []prOutcome `json:"results"`
}

type discordOut struct {
	ReportTo string `json:"reportTo,omitempty"`
	AlertsTo string `json:"alertsTo,omitempty"`
	Posted   bool   `json:"posted"`
	Error    string `json:"error,omitempty"`
}

type prOutcome struct {
	URL            string `json:"url"`
	Repo           string `json:"repo"`
	Number         int    `json:"number"`
	Author         string `json:"author"`
	Action         string `json:"action"` // merged|commented|skipped|error
	Reason         string `json:"reason,omitempty"`
	MergeCommitOID string `json:"mergeCommitOid,omitempty"`
	ChecksState    string `json:"checksState,omitempty"`
	Mergeable      string `json:"mergeable,omitempty"`
	ReviewDecision string `json:"reviewDecision,omitempty"`
	ReviewComments string `json:"reviewComments,omitempty"`
	CIFailureType  string `json:"ciFailureType,omitempty"`
}

type mergeMutationResponse struct {
	Data struct {
		MergePullRequest struct {
			PullRequest struct {
				Merged      bool   `json:"merged"`
				MergedAt    string `json:"mergedAt"`
				MergeCommit struct {
					OID string `json:"oid"`
				} `json:"mergeCommit"`
			} `json:"pullRequest"`
		} `json:"mergePullRequest"`
	} `json:"data"`
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors"`
}

// retryConfig for transient error retries.
var retryCfg = RetryConfig{
	MaxAttempts: 3,
	BaseDelay:   500,
	MaxDelay:    5000,
}

func main() {
	var (
		org                = flag.String("org", "misty-step", "GitHub org/owner to scan")
		maxPRs             = flag.Int("max-prs", 5, "max PRs to act on per run (bounded)")
		staleHours         = flag.Int("stale-hours", 72, "stale threshold (hours) applied only to Phaedrus-authored PRs")
		phaedrus           = flag.String("phaedrus-login", "phrazzld", "GitHub login for Phaedrus (stale threshold applies only to this author)")
		kaylee             = flag.String("kaylee-login", "kaylee-mistystep", "GitHub login for Kaylee (act immediately for this author)")
		doNotTouchLabel    = flag.String("do-not-touch-label", "do not touch", "label name that marks a PR as do-not-touch (case-insensitive)")
		dryRun             = flag.Bool("dry-run", false, "do not merge or comment; only report what would happen")
		discordReportTo    = flag.String("discord-report-to", "", "Discord report destination (e.g. channel:<id> or raw id). Requires DISCORD_BOT_TOKEN.")
		discordAlertsTo    = flag.String("discord-alerts-to", "", "Discord alerts destination (e.g. channel:<id> or raw id). Requires DISCORD_BOT_TOKEN.")
		postEmpty          = flag.Bool("post-empty", false, "post a report even when no PRs were acted on")
		postDryRun         = flag.Bool("post-dry-run", false, "allow posting a report when --dry-run is set")
		cbFailureThreshold = flag.Int("cb-failures", 3, "circuit breaker: consecutive failures before skipping a PR")
		cbSkipRuns         = flag.Int("cb-skip-runs", 5, "circuit breaker: number of runs to skip after opening")
	)
	flag.Parse()

	startedAt := time.Now().UTC().Format(time.RFC3339)
	out := runOutput{
		Ok:         true,
		StartedAt:  startedAt,
		Org:        *org,
		MaxPRs:     *maxPRs,
		StaleHours: *staleHours,
		DryRun:     *dryRun,
		Results:    []prOutcome{},
	}

	// Initialize circuit breaker for per-PR error handling
	cb := NewCircuitBreaker(*cbFailureThreshold, *cbSkipRuns)

	prs, err := RetryableWithResult(func() ([]searchPR, error) {
		return ghSearchPRs(*org, 200)
	}, retryCfg)
	if err != nil {
		if IsPermanent(err) {
			// Permanent error - don't retry further
			msg := "scan failed (permanent): " + err.Error()
			postDiscordAlertIfConfigured(*discordAlertsTo, msg)
			fatalJSON(errors.New(msg))
		}
		// Transient error - we've already retried, report failure
		msg := "scan failed (after retries): " + err.Error()
		postDiscordAlertIfConfigured(*discordAlertsTo, msg)
		fatalJSON(errors.New(msg))
	}

	selected := make([]searchPR, 0, len(prs))
	for _, pr := range prs {
		if pr.IsDraft {
			continue
		}
		if isDoNotTouch(*doNotTouchLabel, pr.Title, pr.Body, pr.Labels) {
			continue
		}
		author := strings.TrimSpace(pr.Author.Login)
		if author == "" {
			continue
		}
		if strings.EqualFold(author, *phaedrus) {
			age := time.Since(pr.UpdatedAt)
			if age < time.Duration(*staleHours)*time.Hour {
				continue
			}
		}
		// Kaylee-authored: act immediately (no stale wait)
		// Everyone else: act immediately (no stale wait), per spec.
		_ = kaylee // kept for clarity and future tuning.
		selected = append(selected, pr)
	}

	// Process most-recently-updated PRs first — they're more likely
	// to have fresh CI results and be merge-ready.
	sortByUpdatedAtDesc(selected)

	// Batch-fetch all archived repos upfront to avoid N per-PR API calls.
	archivedRepos, archFetchErr := fetchArchivedRepos(*org)
	if archFetchErr != nil {
		// Log error but continue - will fall back to per-PR checking.
		fmt.Fprintf(os.Stderr, "[archived-repos] batch fetch failed: %v (falling back to per-PR checks)\n", archFetchErr)
		archivedRepos = nil
	} else if *dryRun {
		// Count archived repos for dry-run output.
		archivedCount := 0
		for _, v := range archivedRepos {
			if v {
				archivedCount++
			}
		}
		fmt.Fprintf(os.Stderr, "[archived-repos] batch-checked %d repos, %d archived\n", len(archivedRepos), archivedCount)
	}

	acted := 0
	for _, pr := range selected {
		if acted >= *maxPRs {
			break
		}
		acted++

		outcome := prOutcome{
			URL:    pr.URL,
			Repo:   pr.Repository.NameWithOwner,
			Number: pr.Number,
			Author: pr.Author.Login,
		}

		// Circuit breaker check: skip if this PR is in circuit-open state
		if cb.IsOpen(pr.URL) {
			outcome.Action = "skipped"
			outcome.Reason = "circuit_breaker"
			out.Results = append(out.Results, outcome)
			continue
		}

		view, viewErr := RetryableWithResult(func() (*prView, error) {
			return ghPRView(pr.URL)
		}, retryCfg)
		if viewErr != nil {
			if IsPermanent(viewErr) {
				// Permanent errors - don't use circuit breaker, just skip with permanent flag
				outcome.Action = "error"
				outcome.Reason = "pr view failed (permanent): " + viewErr.Error()
			} else {
				outcome.Action = "error"
				outcome.Reason = "pr view failed (after retries): " + viewErr.Error()
				cb.RecordFailure(pr.URL)
			}
			out.Results = append(out.Results, outcome)
			continue
		}
		outcome.ChecksState = overallChecksState(view.StatusCheckRollup)
		outcome.Mergeable = strings.TrimSpace(view.Mergeable)
		outcome.ReviewDecision = strings.TrimSpace(view.ReviewDecision)

		// Re-check hard stops at point-of-act.
		if view.IsDraft {
			outcome.Action = "skipped"
			outcome.Reason = "draft"
			out.Results = append(out.Results, outcome)
			cb.RecordSuccess(pr.URL)
			continue
		}
		if isDoNotTouch(*doNotTouchLabel, view.Title, view.Body, view.Labels) {
			outcome.Action = "skipped"
			outcome.Reason = "do_not_touch"
			out.Results = append(out.Results, outcome)
			cb.RecordSuccess(pr.URL)
			continue
		}

		mergeOK, mergeReason := mergeAllowed(view)
		if mergeOK {
			if *dryRun {
				outcome.Action = "skipped"
				outcome.Reason = "dry_run_mergeable"
				out.Results = append(out.Results, outcome)
				cb.RecordSuccess(pr.URL)
				continue
			}

			oid, mergeErr := RetryableWithResult(func() (string, error) {
				return ghMergePR(view.ID)
			}, retryCfg)
			if mergeErr != nil {
				if IsPermanent(mergeErr) {
					outcome.Action = "error"
					outcome.Reason = "merge failed (permanent): " + mergeErr.Error()
				} else {
					outcome.Action = "error"
					outcome.Reason = "merge failed (after retries): " + mergeErr.Error()
					cb.RecordFailure(pr.URL)
				}
				out.Results = append(out.Results, outcome)
				continue
			}
			outcome.Action = "merged"
			outcome.MergeCommitOID = oid
			out.Results = append(out.Results, outcome)
			cb.RecordSuccess(pr.URL)
			continue
		}

		// Handle CONFLICTING mergeable state: try auto-update, then post dedup'd comment.
		if mergeReason == "mergeable_conflicting" {
			if *dryRun {
				outcome.Action = "skipped"
				outcome.Reason = "dry_run_" + mergeReason
				out.Results = append(out.Results, outcome)
				cb.RecordSuccess(pr.URL)
				continue
			}

			// Attempt to auto-resolve by merging base into PR branch.
			updateErr := ghPRUpdateBranch(view.URL)
			if updateErr == nil {
				// Success! Branch updated, conflicts may be resolved.
				outcome.Action = "conflict_resolved"
				outcome.Reason = mergeReason
				out.Results = append(out.Results, outcome)
				cb.RecordSuccess(pr.URL)
				continue
			}

			// Update failed - check if we already posted a conflict comment.
			comments, commentsErr := ghPRComments(view.URL)
			conflictMarker := "merge conflict with the base branch"
			alreadyCommented := false
			if commentsErr == nil && len(comments) > 0 {
				// Check if the most recent comment contains our conflict marker.
				for _, c := range comments {
					if strings.Contains(c, conflictMarker) {
						alreadyCommented = true
						break
					}
				}
			}

			if alreadyCommented {
				outcome.Action = "skipped"
				outcome.Reason = mergeReason + "_already_commented"
				out.Results = append(out.Results, outcome)
				cb.RecordSuccess(pr.URL)
				continue
			}

			// Post conflict comment.
			commentBody := buildCommentBody(view, mergeReason)
			commentErr := Retryable(func() error {
				return ghPRComment(view.URL, commentBody)
			}, retryCfg)
			if commentErr != nil {
				if IsArchivedError(commentErr) {
					outcome.Action = "skipped"
					outcome.Reason = "repo_archived"
				} else if IsPermanent(commentErr) {
					outcome.Action = "error"
					outcome.Reason = "conflict comment failed (permanent): " + commentErr.Error()
				} else {
					outcome.Action = "error"
					outcome.Reason = "conflict comment failed (after retries): " + commentErr.Error()
					cb.RecordFailure(pr.URL)
				}
			} else {
				outcome.Action = "commented"
				outcome.Reason = mergeReason
				cb.RecordSuccess(pr.URL)
			}
			out.Results = append(out.Results, outcome)
			continue
		}

		if strings.HasPrefix(mergeReason, "checks_") {
			outcome.CIFailureType = classifyCIFailure(view.StatusCheckRollup)
			if outcome.CIFailureType == "lint" && *discordAlertsTo != "" {
				token := strings.TrimSpace(os.Getenv("DISCORD_BOT_TOKEN"))
				if token != "" {
					alertsTo := normalizeDiscordTarget(*discordAlertsTo)
					msg := fmt.Sprintf("🧹 Lint failure on PR %s (%s#%d). Dispatch lint-fix agent.", view.URL, pr.Repository.NameWithOwner, pr.Number)
					if err := discordSendMessage(token, alertsTo, msg); err != nil {
						fmt.Fprintf(os.Stderr, "lint alert send failed: %v\n", err)
					}
				}
			}
		}

		// Skip archived repos - they're read-only and can't accept comments.
		// Uses batch-fetched archived repo set (fetched once at startup).
		// If batch fetch failed (archivedRepos == nil), allow pipeline to continue.
		repoName := pr.Repository.NameWithOwner
		archived := false
		if archivedRepos != nil {
			archived = archivedRepos[repoName]
			if *dryRun && archived {
				fmt.Fprintf(os.Stderr, "[archived-repos] skipped %s (batch check)\n", repoName)
			}
		}
		if archived {
			outcome.Action = "skipped"
			outcome.Reason = "repo_archived"
			out.Results = append(out.Results, outcome)
			cb.RecordSuccess(pr.URL)
			continue
		}

		// Not mergeable: comment a bounded next action so this run is still end-to-end.
		if *dryRun {
			outcome.Action = "skipped"
			outcome.Reason = "dry_run_" + mergeReason
			out.Results = append(out.Results, outcome)
			cb.RecordSuccess(pr.URL)
			continue
		}

		commentBody := buildCommentBody(view, mergeReason)
		commentErr := Retryable(func() error {
			return ghPRComment(view.URL, commentBody)
		}, retryCfg)
		if commentErr != nil {
			if IsArchivedError(commentErr) {
				// Defense-in-depth: batch pre-check missed this (e.g. batch fetch failed).
				// Downgrade to a skip rather than an error so it doesn't page.
				outcome.Action = "skipped"
				outcome.Reason = "repo_archived"
				fmt.Fprintf(os.Stderr, "[archived-repos] comment fallback detected archived repo %s: %v\n", repoName, commentErr)
			} else if IsPermanent(commentErr) {
				outcome.Action = "error"
				outcome.Reason = "comment failed (permanent): " + commentErr.Error()
			} else {
				outcome.Action = "error"
				outcome.Reason = "comment failed (after retries): " + commentErr.Error()
				cb.RecordFailure(pr.URL)
			}
		} else {
			outcome.Reason = mergeReason
			if outcome.CIFailureType == "lint" {
				outcome.Action = "lint_dispatched"
			} else {
				outcome.Action = "commented"
			}
			if mergeReason == "review_changes_requested" {
				comments, err := ghPRReviewComments(view.URL)
				if err == nil {
					outcome.ReviewComments = comments
					if *discordAlertsTo != "" && comments != "" {
						token := strings.TrimSpace(os.Getenv("DISCORD_BOT_TOKEN"))
						if token != "" {
							alertsTo := normalizeDiscordTarget(*discordAlertsTo)
							msg := fmt.Sprintf("🔧 PR %s has changes requested. Review comments:\n%s\nAction needed: address review feedback.", view.URL, comments)
							_ = discordSendMessage(token, alertsTo, msg)
						}
					}
				}
				outcome.Action = "review_dispatched"
			}
		}
		out.Results = append(out.Results, outcome)
		if commentErr == nil {
			cb.RecordSuccess(pr.URL)
		}
	}

	// Post run summary + alerts if configured.
	if err := maybePostDiscord(out, *discordReportTo, *discordAlertsTo, *postEmpty, *postDryRun); err != nil {
		out.Ok = false
		out.Error = err.Error()
		emitJSON(out)
		os.Exit(1)
	}

	emitJSON(out)
}

func fatalJSON(err error) {
	emitJSON(map[string]any{
		"ok":    false,
		"error": err.Error(),
	})
	os.Exit(1)
}

func emitJSON(v any) {
	enc := json.NewEncoder(os.Stdout)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
}

func maybePostDiscord(out runOutput, reportToRaw string, alertsToRaw string, postEmpty bool, postDryRun bool) error {
	reportTo := normalizeDiscordTarget(reportToRaw)
	alertsTo := normalizeDiscordTarget(alertsToRaw)
	if reportTo == "" && alertsTo == "" {
		return nil
	}
	if out.DryRun && !postDryRun {
		return nil
	}
	if len(out.Results) == 0 && !postEmpty {
		return nil
	}

	token := strings.TrimSpace(os.Getenv("DISCORD_BOT_TOKEN"))
	if token == "" {
		return errors.New("DISCORD_BOT_TOKEN missing (needed for Discord posting)")
	}

	merged, commented, skipped, errs := summarize(out.Results)
	summary := renderDiscordSummary(out, merged, commented, skipped, errs)

	var postErr error
	if reportTo != "" {
		postErr = discordSendMessage(token, reportTo, summary)
	}
	if postErr != nil {
		// Best-effort alert.
		if alertsTo != "" && alertsTo != reportTo {
			_ = discordSendMessage(token, alertsTo, "Kaylee PR pipeline: failed to post report: "+postErr.Error())
		}
		return postErr
	}

	// Separate alert ping on errors (avoid duplication if report already includes it in same channel).
	if errs > 0 && alertsTo != "" && alertsTo != reportTo {
		alert := renderDiscordAlert(out, errs)
		if err := discordSendMessage(token, alertsTo, alert); err != nil {
			return err
		}
	}

	return nil
}

func postDiscordAlertIfConfigured(alertsToRaw string, msg string) {
	alertsTo := normalizeDiscordTarget(alertsToRaw)
	if alertsTo == "" {
		return
	}
	token := strings.TrimSpace(os.Getenv("DISCORD_BOT_TOKEN"))
	if token == "" {
		return
	}
	_ = discordSendMessage(token, alertsTo, "Kaylee PR pipeline error: "+msg)
}

func normalizeDiscordTarget(raw string) string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return ""
	}
	s = strings.TrimPrefix(s, "channel:")
	s = strings.TrimPrefix(s, "<#")
	s = strings.TrimSuffix(s, ">")
	return strings.TrimSpace(s)
}

func summarize(results []prOutcome) (merged int, commented int, skipped int, errs int) {
	for _, r := range results {
		switch r.Action {
		case "merged":
			merged++
		case "commented", "review_dispatched", "lint_dispatched":
			commented++
		case "skipped":
			skipped++
		case "error":
			errs++
		}
	}
	return
}

func renderDiscordSummary(out runOutput, merged int, commented int, skipped int, errs int) string {
	lines := []string{
		"Kaylee PR pipeline run",
		fmt.Sprintf("- startedAt: `%s`", out.StartedAt),
		fmt.Sprintf("- org: `%s` | maxPRs: `%d` | staleHours(phaedrus-only): `%d` | dryRun: `%t`", out.Org, out.MaxPRs, out.StaleHours, out.DryRun),
		fmt.Sprintf("- results: merged=`%d` commented=`%d` skipped=`%d` errors=`%d`", merged, commented, skipped, errs),
	}
	if len(out.Results) == 0 {
		lines = append(lines, "", "No PRs selected.")
		return strings.Join(lines, "\n")
	}
	lines = append(lines, "", "Per PR:")
	for _, r := range out.Results {
		suffix := ""
		if r.Reason != "" {
			suffix = " (" + r.Reason + ")"
		}
		if r.Action == "merged" && r.MergeCommitOID != "" {
			suffix = suffix + " commit:" + r.MergeCommitOID
		}
		lines = append(lines, fmt.Sprintf("- %s %s%s", r.Action, r.URL, suffix))
	}
	msg := strings.Join(lines, "\n")
	// Discord max is 2000 chars.
	if len(msg) <= 1900 {
		return msg
	}
	return msg[:1890] + "\n(truncated)"
}

func renderDiscordAlert(out runOutput, errs int) string {
	lines := []string{
		"Kaylee PR pipeline: errors detected",
		fmt.Sprintf("- startedAt: `%s`", out.StartedAt),
		fmt.Sprintf("- errors: `%d`", errs),
		"",
		"Error PRs:",
	}
	for _, r := range out.Results {
		if r.Action != "error" {
			continue
		}
		reason := r.Reason
		if reason == "" {
			reason = "unknown"
		}
		lines = append(lines, fmt.Sprintf("- %s (%s)", r.URL, reason))
	}
	msg := strings.Join(lines, "\n")
	if len(msg) <= 1900 {
		return msg
	}
	return msg[:1890] + "\n(truncated)"
}

func discordSendMessage(token string, channelID string, content string) error {
	tok := strings.TrimSpace(token)
	ch := strings.TrimSpace(channelID)
	if tok == "" {
		return errors.New("missing token")
	}
	if ch == "" {
		return errors.New("missing channel id")
	}
	body := struct {
		Content string `json:"content"`
	}{Content: content}
	b, err := json.Marshal(body)
	if err != nil {
		return err
	}

	req, err := http.NewRequest("POST", "https://discord.com/api/v10/channels/"+ch+"/messages", bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bot "+tok)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "misty-step/factory/kaylee-pr-pipeline")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		msg := strings.TrimSpace(string(raw))
		if msg == "" {
			msg = resp.Status
		}
		return fmt.Errorf("discord send failed (%d): %s", resp.StatusCode, msg)
	}
	return nil
}

func overallChecksState(entries []statusRollupEntry) string {
	if len(entries) == 0 {
		return ""
	}
	// statusCheckRollup is a mixed array of CheckRun + StatusContext records.
	// We compute a coarse overall state: SUCCESS, FAILURE, PENDING.
	pending := false
	for _, e := range entries {
		typeName := strings.TrimSpace(e.Typename)
		switch typeName {
		case "CheckRun":
			status := strings.ToUpper(strings.TrimSpace(e.Status))
			conclusion := strings.ToUpper(strings.TrimSpace(e.Conclusion))
			if status != "" && status != "COMPLETED" {
				pending = true
				continue
			}
			if conclusion == "" {
				pending = true
				continue
			}
			switch conclusion {
			case "SUCCESS", "NEUTRAL", "SKIPPED":
				// ok
			default:
				return "FAILURE"
			}
		case "StatusContext":
			state := strings.ToUpper(strings.TrimSpace(e.State))
			if state == "" {
				pending = true
				continue
			}
			switch state {
			case "SUCCESS":
				// ok
			case "PENDING":
				pending = true
			case "FAILURE", "ERROR":
				return "FAILURE"
			default:
				pending = true
			}
		default:
			// Unknown type; ignore.
		}
	}
	if pending {
		return "PENDING"
	}
	return "SUCCESS"
}

func classifyCIFailure(entries []statusRollupEntry) string {
	categories := make(map[string]bool)
	for _, e := range entries {
		conclusion := strings.ToUpper(strings.TrimSpace(e.Conclusion))
		if conclusion == "FAILURE" {
			nameLower := strings.ToLower(strings.TrimSpace(e.Name))
			if strings.Contains(nameLower, "lint") ||
				strings.Contains(nameLower, "golangci") ||
				strings.Contains(nameLower, "eslint") ||
				strings.Contains(nameLower, "prettier") {
				categories["lint"] = true
			} else if strings.Contains(nameLower, "test") ||
				strings.Contains(nameLower, "spec") ||
				strings.Contains(nameLower, "jest") ||
				strings.Contains(nameLower, "pytest") {
				categories["test"] = true
			} else if strings.Contains(nameLower, "build") ||
				strings.Contains(nameLower, "compile") ||
				strings.Contains(nameLower, "typecheck") ||
				strings.Contains(nameLower, "tsc") {
				categories["build"] = true
			}
		}
	}
	if len(categories) == 0 {
		return "unknown"
	}
	if len(categories) > 1 {
		return "mixed"
	}
	for cat := range categories {
		return cat
	}
	return "unknown"
}

func ghSearchPRs(owner string, limit int) ([]searchPR, error) {
	if strings.TrimSpace(owner) == "" {
		return nil, errors.New("owner/org required")
	}
	if limit <= 0 {
		limit = 30
	}
	args := []string{
		"search", "prs",
		"--owner", owner,
		"--state", "open",
		"--sort", "updated",
		"--order", "desc",
		"--limit", fmt.Sprintf("%d", limit),
		"--json", "url,title,body,updatedAt,isDraft,author,labels,number,repository",
	}
	stdout, err := runCmd("gh", args...)
	if err != nil {
		return nil, err
	}
	var prs []searchPR
	if err := json.Unmarshal(stdout, &prs); err != nil {
		return nil, fmt.Errorf("parse gh search json: %w", err)
	}
	for i := range prs {
		if prs[i].URL == "" || prs[i].Repository.NameWithOwner == "" {
			// best-effort normalize
			prs[i].Repository.NameWithOwner = repoFromPRURL(prs[i].URL)
		}
	}
	return prs, nil
}

func ghPRView(url string) (*prView, error) {
	if strings.TrimSpace(url) == "" {
		return nil, errors.New("pr url required")
	}
	args := []string{
		"pr", "view", url,
		"--json", "id,url,title,body,isDraft,mergeable,reviewDecision,mergeStateStatus,statusCheckRollup,author,labels",
	}
	stdout, err := runCmd("gh", args...)
	if err != nil {
		return nil, err
	}
	var v prView
	if err := json.Unmarshal(stdout, &v); err != nil {
		return nil, fmt.Errorf("parse gh pr view json: %w", err)
	}
	return &v, nil
}

func mergeAllowed(pr *prView) (bool, string) {
	mergeable := strings.ToUpper(strings.TrimSpace(pr.Mergeable))
	if mergeable != "MERGEABLE" {
		return false, "mergeable_" + strings.ToLower(mergeable)
	}
	state := strings.ToUpper(strings.TrimSpace(overallChecksState(pr.StatusCheckRollup)))
	if state == "" {
		// Some repos don't report rollups; treat as not ready.
		return false, "checks_unknown"
	}
	if state != "SUCCESS" {
		return false, "checks_" + strings.ToLower(state)
	}
	decision := strings.ToUpper(strings.TrimSpace(pr.ReviewDecision))
	if decision == "CHANGES_REQUESTED" {
		return false, "review_changes_requested"
	}
	if decision == "REVIEW_REQUIRED" {
		return false, "review_required"
	}
	// APPROVED or empty => ok.
	return true, ""
}

func ghMergePR(pullRequestNodeID string) (string, error) {
	if strings.TrimSpace(pullRequestNodeID) == "" {
		return "", errors.New("pull request node id required")
	}
	query := `mutation($pullRequestId: ID!) {
  mergePullRequest(input: { pullRequestId: $pullRequestId, mergeMethod: MERGE }) {
    pullRequest {
      merged
      mergedAt
      mergeCommit { oid }
    }
  }
}`
	args := []string{
		"api", "graphql",
		"-f", "query=" + query,
		"-f", "pullRequestId=" + pullRequestNodeID,
	}
	stdout, err := runCmd("gh", args...)
	if err != nil {
		return "", err
	}
	var resp mergeMutationResponse
	if err := json.Unmarshal(stdout, &resp); err != nil {
		return "", fmt.Errorf("parse merge response: %w", err)
	}
	if len(resp.Errors) > 0 {
		return "", errors.New(resp.Errors[0].Message)
	}
	oid := resp.Data.MergePullRequest.PullRequest.MergeCommit.OID
	if oid == "" {
		return "", errors.New("merge mutation returned empty mergeCommit oid")
	}
	return oid, nil
}

func ghPRComment(url string, body string) error {
	if strings.TrimSpace(url) == "" {
		return errors.New("pr url required")
	}
	if strings.TrimSpace(body) == "" {
		return errors.New("comment body required")
	}
	args := []string{
		"pr", "comment", url,
		"--body", body,
	}
	_, err := runCmd("gh", args...)
	return err
}

// ghPRUpdateBranch attempts to update a PR branch from its base branch.
// This can automatically resolve merge conflicts when the base has moved forward.
func ghPRUpdateBranch(url string) error {
	if strings.TrimSpace(url) == "" {
		return errors.New("pr url required")
	}
	args := []string{
		"pr", "update-branch", url,
	}
	_, err := runCmd("gh", args...)
	return err
}

// ghPRComments fetches all comment bodies from a PR, ordered newest first.
func ghPRComments(url string) ([]string, error) {
	if strings.TrimSpace(url) == "" {
		return nil, errors.New("pr url required")
	}
	args := []string{
		"pr", "view", url,
		"--json", "comments",
		"--jq", ".comments | sort_by(.createdAt) | reverse | .[].body",
	}
	stdout, err := runCmd("gh", args...)
	if err != nil {
		return nil, err
	}
	bodies := strings.Split(string(stdout), "\n")
	filtered := make([]string, 0, len(bodies))
	for _, b := range bodies {
		if trimmed := strings.TrimSpace(b); trimmed != "" {
			filtered = append(filtered, trimmed)
		}
	}
	return filtered, nil
}

func ghPRReviewComments(url string) (string, error) {
	if strings.TrimSpace(url) == "" {
		return "", errors.New("pr url required")
	}
	args := []string{
		"pr", "view", url,
		"--json", "reviews",
		"--jq", `.reviews[] | select(.state == "CHANGES_REQUESTED") | .body`,
	}
	stdout, err := runCmd("gh", args...)
	if err != nil {
		return "", err
	}
	bodies := strings.Split(string(stdout), "\n")
	for i := range bodies {
		bodies[i] = strings.TrimSpace(bodies[i])
	}
	filtered := make([]string, 0, len(bodies))
	for _, b := range bodies {
		if b != "" {
			filtered = append(filtered, b)
		}
	}
	if len(filtered) == 0 {
		return "", nil
	}
	return strings.Join(filtered, "\n\n"), nil
}

type repoInfo struct {
	Name          string `json:"name"`
	NameWithOwner string `json:"nameWithOwner"`
	IsArchived    bool   `json:"isArchived"`
}

// fetchArchivedRepos fetches all repos in the org and returns a set of archived repo names.
// Uses: gh repo list <org> --json name,nameWithOwner,isArchived --limit 200
func fetchArchivedRepos(org string) (map[string]bool, error) {
	args := []string{
		"repo", "list", org,
		"--json", "name,nameWithOwner,isArchived",
		"--limit", "200",
	}
	out, err := runCmd("gh", args...)
	if err != nil {
		return nil, err
	}
	var repos []repoInfo
	if err := json.Unmarshal(out, &repos); err != nil {
		return nil, fmt.Errorf("parse gh repo list json: %w", err)
	}
	archived := make(map[string]bool)
	for _, r := range repos {
		if r.IsArchived {
			archived[r.NameWithOwner] = true
		}
	}
	return archived, nil
}

func runCmd(bin string, args ...string) ([]byte, error) {
	cmd := exec.Command(bin, args...)
	cmd.Env = os.Environ()
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = strings.TrimSpace(stdout.String())
		}
		if msg == "" {
			msg = err.Error()
		}
		return nil, fmt.Errorf("%s %s: %s", bin, strings.Join(args, " "), msg)
	}
	return stdout.Bytes(), nil
}

func isDoNotTouch(labelName string, title string, body string, labels []label) bool {
	target := strings.ToLower(strings.TrimSpace(labelName))
	if target != "" {
		for _, l := range labels {
			if strings.ToLower(strings.TrimSpace(l.Name)) == target {
				return true
			}
		}
	}
	needle := "do not touch"
	hay := strings.ToLower(title + "\n" + body)
	return strings.Contains(hay, needle)
}

func buildCommentBody(pr *prView, reason string) string {
	// Distinct message for merge conflicts - auto-update failed, needs manual resolution.
	if reason == "mergeable_conflicting" {
		return "<!-- kaylee-pr-pipeline -->\n" +
			"⚠️ This PR has merge conflict with the base branch. Automatic merge-in failed — please resolve conflicts manually and push."
	}

	// Keep it short and deterministic; this is meant to be machine-run.
	lines := []string{
		"<!-- kaylee-pr-pipeline -->",
		"Kaylee PR pipeline: not merged automatically.",
		"",
		fmt.Sprintf("- mergeable: `%s`", pr.Mergeable),
		fmt.Sprintf("- checks: `%s`", overallChecksState(pr.StatusCheckRollup)),
		fmt.Sprintf("- reviewDecision: `%s`", pr.ReviewDecision),
		fmt.Sprintf("- reason: `%s`", reason),
		"",
		"Next action: make checks green and resolve review blockers; rerun pipeline.",
	}
	if strings.HasPrefix(reason, "checks_") {
		ciType := classifyCIFailure(pr.StatusCheckRollup)
		if ciType == "lint" {
			lines = append(lines, "🧹 Lint-fix subagent dispatched via Discord for batch dispatch.")
		}
	}
	return strings.Join(lines, "\n")
}

func repoFromPRURL(prURL string) string {
	// https://github.com/OWNER/REPO/pull/123
	re := regexp.MustCompile(`^https://github\\.com/([^/]+)/([^/]+)/pull/\\d+/?$`)
	m := re.FindStringSubmatch(strings.TrimSpace(prURL))
	if len(m) == 3 {
		return m[1] + "/" + m[2]
	}
	return ""
}

func sortByUpdatedAtDesc(prs []searchPR) {
	// Simple insertion sort is fine for small lists.
	// Newest-updated first so the maxPRs window hits fresh, merge-ready PRs.
	for i := 1; i < len(prs); i++ {
		j := i
		for j > 0 && prs[j-1].UpdatedAt.Before(prs[j].UpdatedAt) {
			prs[j-1], prs[j] = prs[j], prs[j-1]
			j--
		}
	}
}
