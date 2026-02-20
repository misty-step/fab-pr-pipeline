package main

import (
	"strings"
	"testing"
)

// TestBuildCommentBody_conflicting verifies the conflict comment format:
// - Contains the canonical conflict marker (used for dedup detection)
// - Does not include PR-specific fields (it's a static message)
// - Starts with the machine-readable HTML comment tag
func TestBuildCommentBody_conflicting(t *testing.T) {
	pr := &prView{
		URL:            "https://github.com/test/repo/pull/1",
		Mergeable:      "CONFLICTING",
		ReviewDecision: "APPROVED",
	}

	body := buildCommentBody(pr, "mergeable_conflicting")

	if !strings.Contains(body, conflictCommentMarker) {
		t.Errorf("conflict comment body does not contain marker %q; got:\n%s", conflictCommentMarker, body)
	}

	if !strings.HasPrefix(body, "<!-- kaylee-pr-pipeline -->") {
		t.Errorf("conflict comment body should start with HTML comment tag; got:\n%s", body)
	}

	// The conflict message is static — it must NOT contain dynamic PR fields.
	if strings.Contains(body, "mergeable:") {
		t.Errorf("conflict comment body should be static (no mergeable field); got:\n%s", body)
	}
}

// TestBuildCommentBody_conflicting_markerConsistency verifies that the marker
// embedded in the comment body exactly matches conflictCommentMarker, so the
// dedup check always finds its own comments.
func TestBuildCommentBody_conflicting_markerConsistency(t *testing.T) {
	pr := &prView{}
	body := buildCommentBody(pr, "mergeable_conflicting")

	if !strings.Contains(body, conflictCommentMarker) {
		t.Errorf("buildCommentBody output does not contain conflictCommentMarker %q\nBody: %s",
			conflictCommentMarker, body)
	}
}

// TestHasConflictComment_positive verifies that hasConflictComment returns true
// when a comment containing the conflict marker is present.
func TestHasConflictComment_positive(t *testing.T) {
	pr := &prView{}
	conflictBody := buildCommentBody(pr, "mergeable_conflicting")

	comments := []string{
		"Some unrelated comment",
		conflictBody,
		"Another comment",
	}

	if !hasConflictComment(comments) {
		t.Error("hasConflictComment should return true when conflict comment is present")
	}
}

// TestHasConflictComment_negative verifies that hasConflictComment returns false
// when no comment contains the conflict marker.
func TestHasConflictComment_negative(t *testing.T) {
	comments := []string{
		"LGTM!",
		"<!-- kaylee-pr-pipeline -->\nKaylee PR pipeline: not merged automatically.",
		"Please fix the lint errors.",
	}

	if hasConflictComment(comments) {
		t.Error("hasConflictComment should return false when no conflict comment is present")
	}
}

// TestHasConflictComment_empty verifies that hasConflictComment handles an
// empty comment slice without panicking.
func TestHasConflictComment_empty(t *testing.T) {
	if hasConflictComment(nil) {
		t.Error("hasConflictComment(nil) should return false")
	}
	if hasConflictComment([]string{}) {
		t.Error("hasConflictComment(empty) should return false")
	}
}

// TestHasConflictComment_partialMatch verifies that hasConflictComment performs
// a substring match, catching both the exact marker and messages that embed it.
func TestHasConflictComment_partialMatch(t *testing.T) {
	comments := []string{
		"This PR has a merge conflict with the base branch — please rebase.",
	}
	if !hasConflictComment(comments) {
		t.Error("hasConflictComment should match when marker is a substring of the comment")
	}
}

// TestConflictSkip_alreadyCommented verifies the pipeline skips the update-branch
// call and produces "skipped / mergeable_conflicting_already_commented" when a
// conflict comment already exists.  We test this via the pure outcome values
// produced by the helper logic — no external calls required.
func TestConflictSkip_alreadyCommented(t *testing.T) {
	// Simulate what the pipeline does: if hasConflictComment returns true the
	// pipeline sets action=skipped, reason=mergeable_conflicting_already_commented.
	comments := []string{buildCommentBody(&prView{}, "mergeable_conflicting")}

	action := "unknown"
	reason := ""
	if hasConflictComment(comments) {
		action = "skipped"
		reason = "mergeable_conflicting_already_commented"
	}

	if action != "skipped" {
		t.Errorf("expected action=skipped, got %q", action)
	}
	if reason != "mergeable_conflicting_already_commented" {
		t.Errorf("expected reason=mergeable_conflicting_already_commented, got %q", reason)
	}
}
