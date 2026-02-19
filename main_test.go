package main

import (
	"testing"
)

func TestSummarize_review_dispatched(t *testing.T) {
	results := []prOutcome{
		{Action: "review_dispatched"},
	}
	merged, commented, skipped, errs := summarize(results)
	if merged != 0 {
		t.Errorf("expected merged=0, got %d", merged)
	}
	if commented != 1 {
		t.Errorf("expected commented=1, got %d", commented)
	}
	if skipped != 0 {
		t.Errorf("expected skipped=0, got %d", skipped)
	}
	if errs != 0 {
		t.Errorf("expected errs=0, got %d", errs)
	}
}

func TestSummarize_lint_dispatched(t *testing.T) {
	results := []prOutcome{
		{Action: "lint_dispatched"},
	}
	merged, commented, skipped, errs := summarize(results)
	if merged != 0 {
		t.Errorf("expected merged=0, got %d", merged)
	}
	if commented != 1 {
		t.Errorf("expected commented=1, got %d", commented)
	}
	if skipped != 0 {
		t.Errorf("expected skipped=0, got %d", skipped)
	}
	if errs != 0 {
		t.Errorf("expected errs=0, got %d", errs)
	}
}

func TestSummarize_ciFailureType(t *testing.T) {
	// Tests that CIFailureType is populated (via classifyCIFailure integration)
	entries := []statusRollupEntry{
		{Typename: "CheckRun", Name: "golangci-lint", Conclusion: "FAILURE"},
	}
	ciType := classifyCIFailure(entries)
	if ciType != "lint" {
		t.Errorf("expected 'lint', got %q", ciType)
	}
	
	entries2 := []statusRollupEntry{
		{Typename: "CheckRun", Name: "unit tests", Conclusion: "FAILURE"},
	}
	ciType2 := classifyCIFailure(entries2)
	if ciType2 != "test" {
		t.Errorf("expected 'test', got %q", ciType2)
	}
}
