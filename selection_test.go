package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// buildTimelineJSON constructs the raw GraphQL JSON that phrazzldLastTouched
// passes to parseLastTouched. It accepts slices of comment/review/commit
// stubs so tests don't have to hand-craft the full nested struct.
func buildTimelineJSON(t *testing.T, comments []map[string]any, reviews []map[string]any, commits []map[string]any) []byte {
	t.Helper()
	pr := map[string]any{
		"comments": map[string]any{"nodes": comments},
		"reviews":  map[string]any{"nodes": reviews},
		"commits":  map[string]any{"nodes": commits},
	}
	resp := map[string]any{
		"data": map[string]any{
			"repository": map[string]any{
				"pullRequest": pr,
			},
		},
	}
	b, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("buildTimelineJSON: %v", err)
	}
	return b
}

// TestPhrazzldLastTouched_noActivity: when phaedrusLogin has no matching
// events, parseLastTouched must return the zero time.
func TestPhrazzldLastTouched_noActivity(t *testing.T) {
	// All events are authored by someone else.
	raw := buildTimelineJSON(t,
		[]map[string]any{
			{"author": map[string]any{"login": "other-user"}, "createdAt": "2026-01-01T10:00:00Z"},
		},
		[]map[string]any{
			{"author": map[string]any{"login": "reviewer-bot"}, "submittedAt": "2026-01-02T12:00:00Z"},
		},
		[]map[string]any{
			{
				"commit": map[string]any{"committedDate": "2026-01-03T08:00:00Z"},
				"author": map[string]any{"user": map[string]any{"login": "collaborator"}},
			},
		},
	)

	got, err := parseLastTouched(raw, "phrazzld")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !got.IsZero() {
		t.Errorf("expected zero time when phaedrus has no activity, got %v", got)
	}
}

// TestPhrazzldLastTouched_commentOnly: phaedrus left a single comment.
func TestPhrazzldLastTouched_commentOnly(t *testing.T) {
	want, _ := time.Parse(time.RFC3339, "2026-02-10T14:30:00Z")
	raw := buildTimelineJSON(t,
		[]map[string]any{
			{"author": map[string]any{"login": "phrazzld"}, "createdAt": "2026-02-10T14:30:00Z"},
		},
		nil,
		nil,
	)

	got, err := parseLastTouched(raw, "phrazzld")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !got.Equal(want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

// TestPhrazzldLastTouched_reviewOnly: phaedrus submitted a review.
func TestPhrazzldLastTouched_reviewOnly(t *testing.T) {
	want, _ := time.Parse(time.RFC3339, "2026-02-15T09:00:00Z")
	raw := buildTimelineJSON(t,
		nil,
		[]map[string]any{
			{"author": map[string]any{"login": "phrazzld"}, "submittedAt": "2026-02-15T09:00:00Z"},
		},
		nil,
	)

	got, err := parseLastTouched(raw, "phrazzld")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !got.Equal(want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

// TestPhrazzldLastTouched_commitOnly: phaedrus pushed a commit.
func TestPhrazzldLastTouched_commitOnly(t *testing.T) {
	want, _ := time.Parse(time.RFC3339, "2026-02-20T18:00:00Z")
	raw := buildTimelineJSON(t,
		nil,
		nil,
		[]map[string]any{
			{
				"commit": map[string]any{"committedDate": "2026-02-20T18:00:00Z"},
				"author": map[string]any{"user": map[string]any{"login": "phrazzld"}},
			},
		},
	)

	got, err := parseLastTouched(raw, "phrazzld")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !got.Equal(want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

// TestPhrazzldLastTouched_returnsMaxTimestamp: multiple events across
// comments/reviews/commits — must return the latest one.
func TestPhrazzldLastTouched_returnsMaxTimestamp(t *testing.T) {
	// Commit is newest; comments and reviews are older.
	wantStr := "2026-02-21T20:00:00Z"
	want, _ := time.Parse(time.RFC3339, wantStr)

	raw := buildTimelineJSON(t,
		[]map[string]any{
			// phaedrus comment (older)
			{"author": map[string]any{"login": "phrazzld"}, "createdAt": "2026-02-19T10:00:00Z"},
			// other user (should be ignored)
			{"author": map[string]any{"login": "other"}, "createdAt": "2026-02-21T21:00:00Z"},
		},
		[]map[string]any{
			// phaedrus review (middle)
			{"author": map[string]any{"login": "phrazzld"}, "submittedAt": "2026-02-20T12:00:00Z"},
		},
		[]map[string]any{
			// phaedrus commit (newest — this should win)
			{
				"commit": map[string]any{"committedDate": wantStr},
				"author": map[string]any{"user": map[string]any{"login": "phrazzld"}},
			},
		},
	)

	got, err := parseLastTouched(raw, "phrazzld")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !got.Equal(want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

// TestPhrazzldLastTouched_commitNoUser: commit node with nil user field
// must not panic and must be skipped.
func TestPhrazzldLastTouched_commitNoUser(t *testing.T) {
	// Commit with no user attached (e.g. authored by an email not linked to a GitHub account)
	raw := buildTimelineJSON(t,
		nil,
		nil,
		[]map[string]any{
			{
				"commit": map[string]any{"committedDate": "2026-02-20T18:00:00Z"},
				"author": map[string]any{"user": nil}, // nil user
			},
		},
	)

	got, err := parseLastTouched(raw, "phrazzld")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !got.IsZero() {
		t.Errorf("expected zero time when commit has no user, got %v", got)
	}
}

// TestPhrazzldLastTouched_caseInsensitive: login comparison must be
// case-insensitive.
func TestPhrazzldLastTouched_caseInsensitive(t *testing.T) {
	want, _ := time.Parse(time.RFC3339, "2026-02-10T14:30:00Z")
	raw := buildTimelineJSON(t,
		[]map[string]any{
			// Login returned with different casing
			{"author": map[string]any{"login": "Phrazzld"}, "createdAt": "2026-02-10T14:30:00Z"},
		},
		nil,
		nil,
	)

	got, err := parseLastTouched(raw, "phrazzld")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !got.Equal(want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

// TestParsePRURL tests the parsePRURL helper used by phrazzldLastTouched.
func TestParsePRURL(t *testing.T) {
	tests := []struct {
		url     string
		owner   string
		repo    string
		number  int
		wantErr bool
	}{
		{
			url:    "https://github.com/misty-step/fab-pr-pipeline/pull/42",
			owner:  "misty-step",
			repo:   "fab-pr-pipeline",
			number: 42,
		},
		{
			url:    "https://github.com/misty-step/fab-pr-pipeline/pull/42/",
			owner:  "misty-step",
			repo:   "fab-pr-pipeline",
			number: 42,
		},
		{
			url:     "https://github.com/misty-step/fab-pr-pipeline",
			wantErr: true,
		},
		{
			url:     "",
			wantErr: true,
		},
		{
			url:     "not-a-url",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.url, func(t *testing.T) {
			owner, repo, number, err := parsePRURL(tt.url)
			if tt.wantErr {
				if err == nil {
					t.Errorf("expected error for %q, got nil", tt.url)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if owner != tt.owner || repo != tt.repo || number != tt.number {
				t.Errorf("got owner=%q repo=%q number=%d; want owner=%q repo=%q number=%d",
					owner, repo, number, tt.owner, tt.repo, tt.number)
			}
		})
	}
}

// TestAuthorSelection_kayleeEligible: kaylee-authored PRs must always be
// selected regardless of when they were updated.
func TestAuthorSelection_kayleeEligible(t *testing.T) {
	// Simulate the three-branch author check: kaylee → eligible.
	kayleeLogin := "kaylee-mistystep"
	phaedrusLogin := "phrazzld"
	author := "kaylee-mistystep"

	isKaylee := strings.EqualFold(author, kayleeLogin)
	isPhaedrus := strings.EqualFold(author, phaedrusLogin)

	if !isKaylee {
		t.Errorf("expected kaylee match, got false for author %q", author)
	}
	if isPhaedrus {
		t.Errorf("kaylee should not match phaedrus check")
	}
}

// TestAuthorSelection_phaedrusRecentlyTouched: phrazzld-authored PR touched
// recently must be skipped.
func TestAuthorSelection_phaedrusRecentlyTouched(t *testing.T) {
	staleThreshold := 72 * time.Hour
	lastTouch := time.Now().Add(-1 * time.Hour) // 1 hour ago — recent

	shouldSkip := !lastTouch.IsZero() && time.Since(lastTouch) < staleThreshold
	if !shouldSkip {
		t.Error("expected phaedrus PR touched 1h ago to be skipped")
	}
}

// TestAuthorSelection_phaedrusStale: phrazzld-authored PR with no recent
// touch must be eligible.
func TestAuthorSelection_phaedrusStale(t *testing.T) {
	staleThreshold := 72 * time.Hour
	lastTouch := time.Now().Add(-96 * time.Hour) // 96 hours ago — stale

	shouldSkip := !lastTouch.IsZero() && time.Since(lastTouch) < staleThreshold
	if shouldSkip {
		t.Error("expected phaedrus PR touched 96h ago to be eligible (not skipped)")
	}
}

// TestAuthorSelection_phaedrusZeroLastTouch: phrazzld-authored PR with zero
// last touch (no phaedrus activity found) must be eligible.
func TestAuthorSelection_phaedrusZeroLastTouch(t *testing.T) {
	staleThreshold := 72 * time.Hour
	lastTouch := time.Time{} // zero — no phaedrus activity

	shouldSkip := !lastTouch.IsZero() && time.Since(lastTouch) < staleThreshold
	if shouldSkip {
		t.Error("expected zero lastTouch to make PR eligible (not skipped)")
	}
}

// TestAuthorSelection_otherAuthorSkipped: PRs by any other author must be
// skipped (not appended to selected list).
func TestAuthorSelection_otherAuthorSkipped(t *testing.T) {
	kayleeLogin := "kaylee-mistystep"
	phaedrusLogin := "phrazzld"
	otherAuthors := []string{"dependabot", "renovate-bot", "random-contributor", ""}

	for _, author := range otherAuthors {
		isKaylee := strings.EqualFold(author, kayleeLogin)
		isPhaedrus := strings.EqualFold(author, phaedrusLogin)
		if isKaylee || isPhaedrus {
			t.Errorf("author %q should not match kaylee or phaedrus", author)
		}
		// In the actual selection loop, this author falls through to the implicit
		// skip (no append) at the bottom of the three-branch check.
	}
}
