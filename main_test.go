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
