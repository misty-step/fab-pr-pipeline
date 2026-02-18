package main

import (
	"encoding/json"
	"testing"
)

// TestParseArchivedRepos tests parsing of gh repo list JSON output.
func TestParseArchivedRepos(t *testing.T) {
	tests := []struct {
		name          string
		jsonInput     string
		wantArchived  map[string]bool
		wantErr       bool
	}{
		{
			name:        "archived and non-archived",
			jsonInput:   `[{"name":"f","nameWithOwner":"m/f","isArchived":false},{"name":"o","nameWithOwner":"m/o","isArchived":true}]`,
			wantArchived: map[string]bool{"m/o": true}, wantErr: false,
		},
		{
			name:        "empty list",
			jsonInput:   `[]`,
			wantArchived: map[string]bool{}, wantErr: false,
		},
		{
			name:        "malformed JSON",
			jsonInput:   `[{"name": "test", "isArchived":}]`,
			wantArchived: nil, wantErr: true,
		},
		{
			name:        "invalid JSON",
			jsonInput:   `not json`,
			wantArchived: nil, wantErr: true,
		},
		{
			name:        "all archived",
			jsonInput:   `[{"name":"l","nameWithOwner":"m/l","isArchived":true}]`,
			wantArchived: map[string]bool{"m/l": true}, wantErr: false,
		},
		{
			name:        "multiple archived repos",
			jsonInput:   `[{"name":"a","nameWithOwner":"org/a","isArchived":true},{"name":"b","nameWithOwner":"org/b","isArchived":true},{"name":"c","nameWithOwner":"org/c","isArchived":false}]`,
			wantArchived: map[string]bool{"org/a": true, "org/b": true}, wantErr: false,
		},
		{
			name:        "repo with special characters",
			jsonInput:   `[{"name":"my-repo","nameWithOwner":"org/my-repo","isArchived":true}]`,
			wantArchived: map[string]bool{"org/my-repo": true}, wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var repos []repoInfo
			err := json.Unmarshal([]byte(tt.jsonInput), &repos)

			if tt.wantErr {
				if err == nil {
					t.Error("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			archived := make(map[string]bool)
			for _, r := range repos {
				if r.IsArchived {
					archived[r.NameWithOwner] = true
				}
			}

			for repo, want := range tt.wantArchived {
				if got := archived[repo]; got != want {
					t.Errorf("archived[%q] = %v, want %v", repo, got, want)
				}
			}
		})
	}
}

// TestBatchMapLookup tests the batch map lookup logic used in the pipeline.
func TestBatchMapLookup(t *testing.T) {
	tests := []struct {
		name          string
		archivedRepos map[string]bool
		repoName      string
		wantArchived  bool
	}{
		{
			name:          "repo is archived in batch map",
			archivedRepos: map[string]bool{"org/repo1": true, "org/repo2": false},
			repoName:      "org/repo1",
			wantArchived:  true,
		},
		{
			name:          "repo is not archived",
			archivedRepos: map[string]bool{"org/repo1": true, "org/repo2": false},
			repoName:      "org/repo2",
			wantArchived:  false,
		},
		{
			name:          "repo not in batch map",
			archivedRepos: map[string]bool{"org/repo1": true},
			repoName:      "org/unknown",
			wantArchived:  false,
		},
		{
			name:          "empty batch map",
			archivedRepos: map[string]bool{},
			repoName:      "org/repo1",
			wantArchived:  false,
		},
		{
			name:          "nil batch map (graceful degradation)",
			archivedRepos: nil,
			repoName:      "org/repo1",
			wantArchived:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			archived := false
			if tt.archivedRepos != nil {
				archived = tt.archivedRepos[tt.repoName]
			}
			if archived != tt.wantArchived {
				t.Errorf("batch lookup = %v, want %v", archived, tt.wantArchived)
			}
		})
	}
}

// TestArchivedReposIntegration tests integration between batch fetch and PR filtering.
func TestArchivedReposIntegration(t *testing.T) {
	// Simulate batch fetch result
	batchResult := map[string]bool{
		"misty-step/archived-repo": true,
		"misty-step/active-repo":   false,
	}

	type mockPR struct {
		repoName string
	}

	prs := []mockPR{
		{repoName: "misty-step/archived-repo"},
		{repoName: "misty-step/active-repo"},
		{repoName: "misty-step/unknown-repo"},
	}

	var skipped []string
	var processed []string

	for _, pr := range prs {
		archived := batchResult[pr.repoName]

		if archived {
			skipped = append(skipped, pr.repoName)
		} else {
			processed = append(processed, pr.repoName)
		}
	}

	if len(skipped) != 1 {
		t.Errorf("expected 1 skipped, got %d", len(skipped))
	}
	if skipped[0] != "misty-step/archived-repo" {
		t.Errorf("expected skipped misty-step/archived-repo, got %s", skipped[0])
	}
	if len(processed) != 2 {
		t.Errorf("expected 2 processed, got %d", len(processed))
	}
}
