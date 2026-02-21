package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestHashResults(t *testing.T) {
	t.Run("empty results returns empty hash", func(t *testing.T) {
		hash := hashResults([]prOutcome{})
		if hash != "" {
			t.Errorf("expected empty hash for empty results, got %q", hash)
		}
	})

	t.Run("same results produce same hash", func(t *testing.T) {
		results := []prOutcome{
			{URL: "https://github.com/test/repo/pull/1", Action: "merged", Reason: ""},
			{URL: "https://github.com/test/repo/pull/2", Action: "skipped", Reason: "draft"},
		}
		hash1 := hashResults(results)
		hash2 := hashResults(results)
		if hash1 != hash2 {
			t.Errorf("same results should produce same hash: %q vs %q", hash1, hash2)
		}
	})

	t.Run("different results produce different hash", func(t *testing.T) {
		results1 := []prOutcome{
			{URL: "https://github.com/test/repo/pull/1", Action: "merged", Reason: ""},
		}
		results2 := []prOutcome{
			{URL: "https://github.com/test/repo/pull/1", Action: "skipped", Reason: "draft"},
		}
		hash1 := hashResults(results1)
		hash2 := hashResults(results2)
		if hash1 == hash2 {
			t.Errorf("different results should produce different hash")
		}
	})

	t.Run("order doesn't affect hash", func(t *testing.T) {
		results1 := []prOutcome{
			{URL: "https://github.com/test/repo/pull/1", Action: "merged", Reason: ""},
			{URL: "https://github.com/test/repo/pull/2", Action: "skipped", Reason: "draft"},
		}
		results2 := []prOutcome{
			{URL: "https://github.com/test/repo/pull/2", Action: "skipped", Reason: "draft"},
			{URL: "https://github.com/test/repo/pull/1", Action: "merged", Reason: ""},
		}
		hash1 := hashResults(results1)
		hash2 := hashResults(results2)
		if hash1 != hash2 {
			t.Errorf("reordered results should produce same hash: %q vs %q", hash1, hash2)
		}
	})
}

func TestShouldPostToDiscord(t *testing.T) {
	// Create a temp file for state
	tmpDir := t.TempDir()
	statePath := filepath.Join(tmpDir, "state.json")

	t.Run("no prior state always posts", func(t *testing.T) {
		should, _ := shouldPostToDiscord(statePath, "hash123")
		if !should {
			t.Error("expected to post when no prior state")
		}
	})

	t.Run("empty hash always posts", func(t *testing.T) {
		// Save state first
		_ = saveState(statePath, "previous-hash")
		should, _ := shouldPostToDiscord(statePath, "")
		if !should {
			t.Error("expected to post when current hash is empty")
		}
	})

	t.Run("changed hash always posts", func(t *testing.T) {
		_ = saveState(statePath, "old-hash")
		should, _ := shouldPostToDiscord(statePath, "new-hash")
		if !should {
			t.Error("expected to post when hash changed")
		}
	})

	t.Run("same hash within window skips", func(t *testing.T) {
		_ = saveState(statePath, "same-hash")
		should, reason := shouldPostToDiscord(statePath, "same-hash")
		if should {
			t.Error("expected to skip when same hash within window")
		}
		if reason == "" {
			t.Error("expected skip reason")
		}
	})
}

func TestLoadSaveState(t *testing.T) {
	tmpDir := t.TempDir()
	statePath := filepath.Join(tmpDir, "state.json")

	t.Run("loadState returns empty for missing file", func(t *testing.T) {
		state := loadState("/nonexistent/path/state.json")
		if state.Hash != "" || state.LastPostedAt != "" {
			t.Errorf("expected empty state, got %+v", state)
		}
	})

	t.Run("saveState and loadState roundtrip", func(t *testing.T) {
		err := saveState(statePath, "test-hash-123")
		if err != nil {
			t.Fatalf("saveState failed: %v", err)
		}

		state := loadState(statePath)
		if state.Hash != "test-hash-123" {
			t.Errorf("expected hash 'test-hash-123', got %q", state.Hash)
		}
		if state.LastPostedAt == "" {
			t.Error("expected LastPostedAt to be set")
		}
	})

	t.Run("loadState handles corrupt JSON", func(t *testing.T) {
		// Write invalid JSON
		_ = os.WriteFile(statePath, []byte("not valid json"), 0644)
		state := loadState(statePath)
		if state.Hash != "" || state.LastPostedAt != "" {
			t.Errorf("expected empty state for corrupt file, got %+v", state)
		}
	})
}

func TestDedupIntegration(t *testing.T) {
	// Integration test: create two identical runOutput values,
	// call shouldPostToDiscord twice, verify second returns skip.
	tmpDir := t.TempDir()
	statePath := filepath.Join(tmpDir, "state.json")

	results := []prOutcome{
		{URL: "https://github.com/test/repo/pull/1", Action: "skipped", Reason: "no_changes"},
		{URL: "https://github.com/test/repo/pull/2", Action: "skipped", Reason: "no_changes"},
	}

	// First call - should post
	hash := hashResults(results)
	should1, _ := shouldPostToDiscord(statePath, hash)
	if !should1 {
		t.Fatal("first call should always post")
	}

	// Simulate saving state after post
	if err := saveState(statePath, hash); err != nil {
		t.Fatalf("saveState failed: %v", err)
	}

	// Second call with same hash - should skip
	should2, reason := shouldPostToDiscord(statePath, hash)
	if should2 {
		t.Error("second call with same hash should skip")
	}
	if reason == "" {
		t.Error("expected skip reason")
	}
	t.Logf("skip reason: %s", reason)
}

func TestDedupAfterTwoHours(t *testing.T) {
	// Test that we post again after 2 hours even with same hash.
	tmpDir := t.TempDir()
	statePath := filepath.Join(tmpDir, "state.json")

	// Create state with LastPostedAt 3 hours ago
	oldTime := time.Now().Add(-3 * time.Hour).UTC().Format(time.RFC3339)
	state := runState{
		Hash:         "same-hash",
		LastPostedAt: oldTime,
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	_ = os.WriteFile(statePath, data, 0644)

	// Should post because > 2 hours
	should, _ := shouldPostToDiscord(statePath, "same-hash")
	if !should {
		t.Error("expected to post after 2+ hours even with same hash")
	}
}
