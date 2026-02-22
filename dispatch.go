package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// openclawBin is the path to the openclaw binary used for subagent dispatch.
// It is a package-level variable so tests can override it with a mock binary.
var openclawBin = "/opt/homebrew/bin/openclaw"

// skillNames lists the CI-fix skill files to load, in order.
// These are relative to the skill dir root (e.g. /tmp/codex-config/skills).
var ciFixSkillNames = []string{
	"fix-ci/SKILL.md",
	"address-review/SKILL.md",
	"refactor/SKILL.md",
	"codify-learning/SKILL.md",
	"pr-polish/SKILL.md",
}

// buildSkillContext reads skill files from skillDir and returns a concatenated
// string suitable for injection into subagent prompts. Missing files are logged
// as warnings and skipped (degraded mode — dispatch still happens without them).
func buildSkillContext(skillDir string) string {
	var sb strings.Builder
	for _, rel := range ciFixSkillNames {
		p := filepath.Join(skillDir, rel)
		content, err := os.ReadFile(p)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[skill-context] warning: could not read %s: %v\n", p, err)
			continue
		}
		// Section header so the subagent can locate each skill block.
		skillName := filepath.Base(filepath.Dir(p))
		sb.WriteString(fmt.Sprintf("--- SKILL: %s ---\n", skillName))
		sb.Write(content)
		sb.WriteString("\n\n")
	}
	return sb.String()
}

// ciFixEnvelope is the Contract A JSON envelope dispatched to the CI-fix subagent.
type ciFixEnvelope struct {
	Task      string         `json:"task"`
	Repo      string         `json:"repo"`
	PRNumber  int            `json:"pr_number"`
	PRURL     string         `json:"pr_url"`
	Branch    string         `json:"branch"`
	BaseBranch string        `json:"base_branch"`
	Context   ciFixContext   `json:"context"`
	SkillFiles []string      `json:"skill_files"`
	OutputContract outputContract `json:"output_contract"`
}

type ciFixContext struct {
	CIFailureType  string `json:"ci_failure_type"`
	FailedRunID    string `json:"failed_run_id"`
	FailedRunURL   string `json:"failed_run_url"`
}

type outputContract struct {
	Format          string   `json:"format"`
	Fields          []string `json:"fields"`
	ActionTaken     string   `json:"action_taken,omitempty"`
	Commits         []string `json:"commits,omitempty"`
	ThreadsResolved int      `json:"threads_resolved,omitempty"`
	IssuesCreated   int      `json:"issues_created,omitempty"`
	Notes           string   `json:"notes,omitempty"`
}

// buildCIFixEnvelope constructs the Contract A envelope for the CI-fix subagent.
func buildCIFixEnvelope(repo string, prNumber int, prURL string, branch string, ciFailureType string, skillDir string) ciFixEnvelope {
	skillPaths := make([]string, len(ciFixSkillNames))
	for i, rel := range ciFixSkillNames {
		skillPaths[i] = filepath.Join(skillDir, rel)
	}
	return ciFixEnvelope{
		Task:       "ci-fix",
		Repo:       repo,
		PRNumber:   prNumber,
		PRURL:      prURL,
		Branch:     branch,
		BaseBranch: "main",
		Context: ciFixContext{
			CIFailureType: ciFailureType,
			// FailedRunID and FailedRunURL are populated by spawnCIFixSubagent
			// when available; left empty if not yet fetched.
		},
		SkillFiles: skillPaths,
		OutputContract: outputContract{
			Format: "json",
			Fields: []string{"ok", "action_taken", "commits", "ci_status", "notes"},
		},
	}
}

// spawnCIFixSubagent dispatches a CI-fix subagent for the given PR using Contract A.
// It injects skill context read from skillDir and constructs a structured prompt.
// This is a fire-and-forget dispatch — the binary does not wait for the subagent.
func spawnCIFixSubagent(prURL, branch, skillCtx string, ciFailureType, skillDir string) error {
	owner, repo, prNumber, err := parsePRURL(prURL)
	if err != nil {
		return fmt.Errorf("spawnCIFixSubagent: %w", err)
	}
	repoFull := owner + "/" + repo

	envelope := buildCIFixEnvelope(repoFull, prNumber, prURL, branch, ciFailureType, skillDir)
	envelopeJSON, err := json.Marshal(envelope)
	if err != nil {
		return fmt.Errorf("spawnCIFixSubagent: marshal envelope: %w", err)
	}

	skillSection := ""
	if skillCtx != "" {
		skillSection = fmt.Sprintf("\nSkill context (read this first):\n%s\n", skillCtx)
	}

	msg := fmt.Sprintf(`You are fixing a CI failure on PR #%d in %s.

Branch: %s
PR URL: %s
CI failure type: %s
%s
Contract (for structured output):
%s

Your job:
1. Check out the branch locally: git fetch origin %s && git checkout %s
2. Fetch CI failure logs: gh run view --repo %s --log-failed | tail -200
3. Classify the failure (Code Issue / Infrastructure / Flaky / Config) per the fix-ci skill
4. Fix the ROOT cause. Never lower quality gates (coverage thresholds, lint strictness, type-check config).
5. Run local verification: the project's test/build/lint commands
6. Commit with: fix(ci): <what was wrong>
7. Push with: git push --force-with-lease origin %s
8. Wait 60s, then verify: gh pr checks %s --json name,state
9. Output JSON: {"ok": true/false, "action_taken": "...", "commits": ["sha..."], "ci_status": "green|red|pending", "notes": "..."}

Hard rules:
- Never re-trigger CI without understanding and fixing the failure
- Never use git push (only git push --force-with-lease)
- No force-push to protected branches
- If failure is infrastructure/flaky: retry once, document, then output ok=true with notes`,
		prNumber, repoFull, branch, prURL, ciFailureType,
		skillSection,
		string(envelopeJSON),
		branch, branch,
		repoFull,
		branch,
		prURL,
	)

	_, err = runCmd(openclawBin, "agent", "--agent", "eng", "--message", msg)
	if err != nil {
		return fmt.Errorf("spawnCIFixSubagent: openclaw agent dispatch: %w", err)
	}
	return nil
}

// conflictFixEnvelope is the Contract B JSON envelope dispatched to the conflict-fix subagent.
type conflictFixEnvelope struct {
	Task           string                `json:"task"`
	Repo           string                `json:"repo"`
	PRNumber       int                   `json:"pr_number"`
	PRURL          string                `json:"pr_url"`
	Branch         string                `json:"branch"`
	BaseBranch     string                `json:"base_branch"`
	Context        conflictFixContext     `json:"context"`
	SkillFiles     []string              `json:"skill_files"`
	OutputContract outputContract        `json:"output_contract"`
}

type conflictFixContext struct {
	ConflictingFiles []string `json:"conflicting_files"`
}

// conflictFixSkillNames lists skill files relevant to conflict resolution.
// These are relative to the skill dir root (e.g. /tmp/codex-config/skills).
var conflictFixSkillNames = []string{
	"git-mastery/SKILL.md",
	"address-review/SKILL.md",
}

// buildConflictFixEnvelope constructs the Contract B envelope for the conflict-fix subagent.
func buildConflictFixEnvelope(repo string, prNumber int, prURL, branch, baseBranch, skillDir string) conflictFixEnvelope {
	// Build skill file paths — missing files are tolerated at runtime by buildSkillContext.
	skillPaths := make([]string, len(conflictFixSkillNames))
	for i, rel := range conflictFixSkillNames {
		skillPaths[i] = filepath.Join(skillDir, rel)
	}
	return conflictFixEnvelope{
		Task:       "conflict-fix",
		Repo:       repo,
		PRNumber:   prNumber,
		PRURL:      prURL,
		Branch:     branch,
		BaseBranch: baseBranch,
		Context: conflictFixContext{
			ConflictingFiles: []string{},
		},
		SkillFiles: skillPaths,
		OutputContract: outputContract{
			Format: "json",
			Fields: []string{"ok", "action_taken", "commits", "notes"},
		},
	}
}

// buildConflictSkillContext reads conflict-relevant skill files from skillDir.
// It is a narrower variant of buildSkillContext: it only reads git-mastery and
// address-review skills. Missing files are logged as warnings and skipped.
func buildConflictSkillContext(skillDir string) string {
	var sb strings.Builder
	for _, rel := range conflictFixSkillNames {
		p := filepath.Join(skillDir, rel)
		content, err := os.ReadFile(p)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[skill-context] warning: could not read %s: %v\n", p, err)
			continue
		}
		skillName := filepath.Base(filepath.Dir(p))
		sb.WriteString(fmt.Sprintf("--- SKILL: %s ---\n", skillName))
		sb.Write(content)
		sb.WriteString("\n\n")
	}
	return sb.String()
}

// spawnConflictFixSubagent dispatches a conflict-fix subagent for the given PR using Contract B.
// It is called after ghPRUpdateBranch fails (non-trivial merge conflict). This is a
// fire-and-forget dispatch — the binary does not block waiting for the subagent.
func spawnConflictFixSubagent(pr searchPR, view *prView, skillDir string) error {
	owner, repo, prNumber, err := parsePRURL(view.URL)
	if err != nil {
		return fmt.Errorf("spawnConflictFixSubagent: %w", err)
	}
	repoFull := owner + "/" + repo
	branch := strings.TrimSpace(view.HeadRefName)
	baseBranch := strings.TrimSpace(view.BaseRefName)
	if baseBranch == "" {
		baseBranch = "main"
	}

	envelope := buildConflictFixEnvelope(repoFull, prNumber, view.URL, branch, baseBranch, skillDir)
	envelopeJSON, err := json.Marshal(envelope)
	if err != nil {
		return fmt.Errorf("spawnConflictFixSubagent: marshal envelope: %w", err)
	}

	// Read skill context at dispatch time so subagent doesn't need local filesystem access.
	skillCtx := buildConflictSkillContext(skillDir)
	skillSection := ""
	if skillCtx != "" {
		skillSection = fmt.Sprintf("\nSkill context (read this first):\n%s\n", skillCtx)
	}

	msg := fmt.Sprintf(`You are resolving merge conflicts on PR #%d in %s.

Branch: %s, base: %s
PR URL: %s
%s
Contract (for structured output):
%s

Your job:
1. git fetch origin
2. git checkout %s
3. git rebase origin/%s
4. For each conflict: resolve SEMANTICALLY based on PR purpose (read the PR description first).
   Never blindly accept ours/theirs. Understand the intent of both sides.
5. git rebase --continue (repeat for each conflict file)
6. Run local verification (project test/build/lint commands)
7. git push --force-with-lease origin %s
8. Output JSON: {"ok": true/false, "action_taken": "...", "commits": ["sha..."], "notes": "..."}

Hard rules:
- Read the PR description before resolving — semantic context drives decisions
- Never force-push to main/master
- If conflict is too complex to resolve safely: output ok=false with explanation`,
		prNumber, repoFull,
		branch, baseBranch,
		view.URL,
		skillSection,
		string(envelopeJSON),
		branch,
		baseBranch,
		branch,
	)

	_, err = runCmd(openclawBin, "agent", "--agent", "eng", "--message", msg)
	if err != nil {
		return fmt.Errorf("spawnConflictFixSubagent: openclaw agent dispatch: %w", err)
	}
	return nil
}

// spawnReviewFixSubagent dispatches a subagent to fix review comments on a PR.
// It uses Contract C and includes skill context from the skill dir.
func spawnReviewFixSubagent(pr searchPR, branch string, reviews []prReview, skillDir string) error {
	// Build skill context
	skillCtx := buildReviewSkillContext(skillDir)

	// Build the review context for the subagent
	var reviewItems []map[string]string
	for _, r := range reviews {
		reviewItems = append(reviewItems, map[string]string{
			"author":  r.Author.Login,
			"state":   r.State,
			"body":    r.Body,
			"severity": classifyReviewSeverity(r.Body),
		})
	}

	// Build contract payload
	contract := reviewFixEnvelope{
		Task:       "Fix review comments on PR. Analyze the review feedback and address all critical, major, and high-severity issues.",
		Repo:       pr.Repository.NameWithOwner,
		PRNumber:   pr.Number,
		PRURL:       pr.URL,
		Branch:     branch,
		BaseBranch: "main",
		Context: reviewFixContext{
			Reviews:       reviewItems,
			PRTitle:       pr.Title,
			PRBody:        pr.Body,
		},
		SkillFiles: reviewFixSkillNames,
		OutputContract: outputContract{
			Format: "json",
			Fields:  []string{"status", "summary", "artifacts"},
		},
	}

	// Serialize and invoke
	contractJSON, err := json.Marshal(contract)
	if err != nil {
		return fmt.Errorf("marshal contract: %w", err)
	}

	// Build the agent command
	contractB64 := base64.StdEncoding.EncodeToString(contractJSON)
	cmd := exec.Command("openclaw", "agent", "run",
		"--model", "openrouter/x-ai/grok-code-fast-1",
		"--system-prompt", skillCtx,
		"--prompt", "You are fixing review comments on PR "+pr.URL+". Contract: "+contractB64+". Output JSON: {\"status\":\"success\",\"summary\":\"...\",\"artifacts\":\"...\"}",
	)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// classifyReviewSeverity returns a severity classification based on review body keywords.
func classifyReviewSeverity(body string) string {
	bodyLower := strings.ToLower(body)
	if strings.Contains(bodyLower, "critical") || strings.Contains(bodyLower, "blocking") || strings.Contains(bodyLower, "must fix") {
		return "critical"
	}
	if strings.Contains(bodyLower, "major") || strings.Contains(bodyLower, "high") || strings.Contains(bodyLower, "security") {
		return "major"
	}
	if strings.Contains(bodyLower, "medium") || strings.Contains(bodyLower, "warning") {
		return "minor"
	}
	return "nitpick"
}

// reviewFixContext contains the PR review data for the subagent.
type reviewFixContext struct {
	Reviews                []map[string]string `json:"reviews"`
	PRTitle                string              `json:"pr_title"`
	PRBody                 string              `json:"pr_body"`
	ReviewCommentsSummary  string              `json:"review_comments_summary"`
	OpenThreadCount        int                 `json:"open_thread_count"`
}

// reviewFixEnvelope is the Contract C JSON envelope for review-fix dispatch.
type reviewFixEnvelope struct {
	Task           string              `json:"task"`
	Repo           string              `json:"repo"`
	PRNumber       int                 `json:"pr_number"`
	PRURL          string              `json:"pr_url"`
	Branch         string              `json:"branch"`
	BaseBranch     string              `json:"base_branch"`
	Context        reviewFixContext    `json:"context"`
	SkillFiles     []string            `json:"skill_files"`
	OutputContract outputContract      `json:"output_contract"`
}

// reviewFixSkillNames lists the skill files for the review-fix subagent.
var reviewFixSkillNames = []string{
	"address-review/SKILL.md",
	"code-review-checklist/SKILL.md",
	"review-and-fix/SKILL.md",
}

// buildReviewSkillContext reads review-fix skill files from skillDir and returns a concatenated string.
func buildReviewSkillContext(skillDir string) string {
	return buildSkillContext(skillDir) // Use existing function for now
}

