package main

import (
	"testing"
)

// TestReviewHasActionableBlockers is a table-driven test for reviewHasActionableBlockers.
// It covers critical/security/major keyword matching, nitpick-only reviews,
// APPROVED reviews, empty review lists, and Cerberus (github-actions) exclusion.
func TestReviewHasActionableBlockers(t *testing.T) {
	cases := []struct {
		name    string
		reviews []prReview
		want    bool
	}{
		{
			name: "empty_reviews",
			reviews: []prReview{},
			want: false,
		},
		{
			name: "approved_only",
			reviews: []prReview{
				{State: "APPROVED", Body: "LGTM!", Author: struct{ Login string `json:"login"` }{Login: "phaedrus"}},
			},
			want: false,
		},
		{
			name: "commented_only_no_changes_requested",
			reviews: []prReview{
				{State: "COMMENTED", Body: "This is a critical bug!", Author: struct{ Login string `json:"login"` }{Login: "phaedrus"}},
			},
			want: false, // COMMENTED state — not CHANGES_REQUESTED
		},
		{
			name: "changes_requested_with_critical_keyword",
			reviews: []prReview{
				{State: "CHANGES_REQUESTED", Body: "This is a critical security issue.", Author: struct{ Login string `json:"login"` }{Login: "phaedrus"}},
			},
			want: true,
		},
		{
			name: "changes_requested_with_major_keyword",
			reviews: []prReview{
				{State: "CHANGES_REQUESTED", Body: "Major logic error in the auth flow.", Author: struct{ Login string `json:"login"` }{Login: "reviewer"}},
			},
			want: true,
		},
		{
			name: "changes_requested_with_security_keyword",
			reviews: []prReview{
				{State: "CHANGES_REQUESTED", Body: "Security vulnerability: SQL injection in query builder.", Author: struct{ Login string `json:"login"` }{Login: "reviewer"}},
			},
			want: true,
		},
		{
			name: "changes_requested_with_high_keyword",
			reviews: []prReview{
				{State: "CHANGES_REQUESTED", Body: "High risk change without tests.", Author: struct{ Login string `json:"login"` }{Login: "reviewer"}},
			},
			want: true,
		},
		{
			name: "changes_requested_with_FAIL_keyword",
			reviews: []prReview{
				{State: "CHANGES_REQUESTED", Body: "Test suite FAIL: missing mock.", Author: struct{ Login string `json:"login"` }{Login: "reviewer"}},
			},
			want: true,
		},
		{
			name: "changes_requested_with_blocking_keyword",
			reviews: []prReview{
				{State: "CHANGES_REQUESTED", Body: "This is blocking the release.", Author: struct{ Login string `json:"login"` }{Login: "reviewer"}},
			},
			want: true,
		},
		{
			name: "changes_requested_with_must_fix_keyword",
			reviews: []prReview{
				{State: "CHANGES_REQUESTED", Body: "must fix before merge", Author: struct{ Login string `json:"login"` }{Login: "reviewer"}},
			},
			want: true,
		},
		{
			name: "changes_requested_with_must-fix_keyword",
			reviews: []prReview{
				{State: "CHANGES_REQUESTED", Body: "must-fix: handle nil pointer dereference", Author: struct{ Login string `json:"login"` }{Login: "reviewer"}},
			},
			want: true,
		},
		{
			name: "changes_requested_nitpick_only",
			reviews: []prReview{
				{State: "CHANGES_REQUESTED", Body: "nit: consider renaming this variable for clarity", Author: struct{ Login string `json:"login"` }{Login: "reviewer"}},
			},
			want: false,
		},
		{
			name: "changes_requested_style_only",
			reviews: []prReview{
				{State: "CHANGES_REQUESTED", Body: "Please use camelCase for this method name.", Author: struct{ Login string `json:"login"` }{Login: "reviewer"}},
			},
			want: false,
		},
		{
			name: "changes_requested_info_warn_only",
			reviews: []prReview{
				{State: "CHANGES_REQUESTED", Body: "INFO: this pattern works but there may be a simpler approach. WARN: double-check the timeout value.", Author: struct{ Login string `json:"login"` }{Login: "reviewer"}},
			},
			want: false,
		},
		{
			name: "changes_requested_cerberus_github_actions_excluded",
			reviews: []prReview{
				// Cerberus posts as github-actions with CHANGES_REQUESTED + critical keywords
				// — these should be excluded from keyword matching (use reviewDecision instead).
				{State: "CHANGES_REQUESTED", Body: "critical: FAIL: security check failed", Author: struct{ Login string `json:"login"` }{Login: "github-actions"}},
			},
			want: false,
		},
		{
			name: "cerberus_excluded_but_human_reviewer_has_blocker",
			reviews: []prReview{
				{State: "CHANGES_REQUESTED", Body: "FAIL: security scan failed", Author: struct{ Login string `json:"login"` }{Login: "github-actions"}},
				{State: "CHANGES_REQUESTED", Body: "This looks good but please fix the critical null check.", Author: struct{ Login string `json:"login"` }{Login: "phaedrus"}},
			},
			want: true, // human reviewer has critical keyword
		},
		{
			name: "mixed_approved_and_changes_requested_with_blocker",
			reviews: []prReview{
				{State: "APPROVED", Body: "LGTM!", Author: struct{ Login string `json:"login"` }{Login: "approver1"}},
				{State: "CHANGES_REQUESTED", Body: "security: token is leaked in logs", Author: struct{ Login string `json:"login"` }{Login: "reviewer2"}},
			},
			want: true,
		},
		{
			name: "keyword_case_insensitive_critical_uppercase",
			reviews: []prReview{
				{State: "CHANGES_REQUESTED", Body: "CRITICAL: data loss possible on rollback", Author: struct{ Login string `json:"login"` }{Login: "reviewer"}},
			},
			want: true,
		},
		{
			name: "keyword_case_insensitive_security_mixed_case",
			reviews: []prReview{
				{State: "CHANGES_REQUESTED", Body: "Security issue: CSRF token missing", Author: struct{ Login string `json:"login"` }{Login: "reviewer"}},
			},
			want: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := reviewHasActionableBlockers(tc.reviews)
			if got != tc.want {
				t.Errorf("reviewHasActionableBlockers(%+v) = %v, want %v", tc.reviews, got, tc.want)
			}
		})
	}
}

// TestBuildReviewFixEnvelope verifies that buildReviewFixEnvelope produces a
// valid Contract C JSON envelope with all required fields.
func TestBuildReviewSkillContext_allMissing(t *testing.T) {
	tmpDir := t.TempDir() // empty dir

	ctx := buildReviewSkillContext(tmpDir)
	if ctx != "" {
		t.Errorf("expected empty string for missing skills, got %q", ctx)
	}
}
