package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestBuildSkillContext_allPresent: when all skill files exist, buildSkillContext
// returns a concatenated string with each skill's content and section headers.
func TestBuildSkillContext_allPresent(t *testing.T) {
	// Set up a mock skill directory with all expected skill files.
	tmpDir := t.TempDir()

	skillContents := map[string]string{
		"fix-ci/SKILL.md":         "# Fix CI\nFix the broken CI.",
		"address-review/SKILL.md": "# Address Review\nAddress review comments.",
		"refactor/SKILL.md":       "# Refactor\nRefactor ruthlessly.",
		"codify-learning/SKILL.md": "# Codify\nCapture learnings.",
		"pr-polish/SKILL.md":      "# Polish\nPolish the PR.",
	}

	for rel, content := range skillContents {
		p := filepath.Join(tmpDir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
			t.Fatalf("mkdir %s: %v", filepath.Dir(p), err)
		}
		if err := os.WriteFile(p, []byte(content), 0644); err != nil {
			t.Fatalf("write %s: %v", p, err)
		}
	}

	ctx := buildSkillContext(tmpDir)

	// Verify all skill section headers are present.
	for _, rel := range ciFixSkillNames {
		skillName := filepath.Base(filepath.Dir(rel))
		header := "--- SKILL: " + skillName + " ---"
		if !strings.Contains(ctx, header) {
			t.Errorf("expected section header %q in skill context, not found", header)
		}
	}

	// Verify actual content is included.
	if !strings.Contains(ctx, "Fix the broken CI.") {
		t.Error("expected fix-ci skill content in output")
	}
	if !strings.Contains(ctx, "Polish the PR.") {
		t.Error("expected pr-polish skill content in output")
	}
}

// TestBuildSkillContext_someMissing: when some skill files are absent,
// buildSkillContext returns content for present skills and logs warnings
// (degraded mode — does not return an error or empty string for present skills).
func TestBuildSkillContext_someMissing(t *testing.T) {
	tmpDir := t.TempDir()

	// Only create one of the five skill files.
	p := filepath.Join(tmpDir, "fix-ci/SKILL.md")
	if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(p, []byte("# Fix CI\nOnly skill present."), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}

	ctx := buildSkillContext(tmpDir)

	// Present skill must appear.
	if !strings.Contains(ctx, "Only skill present.") {
		t.Error("expected fix-ci content in output")
	}

	// Absent skills must not cause a panic or empty the present ones.
	// (No assertion on absent skills — they're simply missing from output.)
}

// TestBuildSkillContext_allMissing: when no skill files exist, buildSkillContext
// returns an empty string (degraded mode).
func TestBuildSkillContext_allMissing(t *testing.T) {
	tmpDir := t.TempDir() // empty directory — no skill files

	ctx := buildSkillContext(tmpDir)
	if ctx != "" {
		t.Errorf("expected empty string when no skill files present, got %q", ctx)
	}
}

// TestSpawnCIFixSubagentContract verifies that buildCIFixEnvelope produces a
// valid Contract A JSON envelope with all required fields.
func TestSpawnCIFixSubagentContract(t *testing.T) {
	skillDir := "/tmp/codex-config/skills"
	envelope := buildCIFixEnvelope(
		"misty-step/my-repo",
		42,
		"https://github.com/misty-step/my-repo/pull/42",
		"feat/my-feature",
		"lint",
		skillDir,
	)

	// Marshal to JSON and back to verify round-trip.
	b, err := json.Marshal(envelope)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}

	var decoded map[string]any
	if err := json.Unmarshal(b, &decoded); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}

	// Top-level fields.
	assertJSONField(t, decoded, "task", "ci-fix")
	assertJSONField(t, decoded, "repo", "misty-step/my-repo")
	assertJSONField(t, decoded, "pr_url", "https://github.com/misty-step/my-repo/pull/42")
	assertJSONField(t, decoded, "branch", "feat/my-feature")
	assertJSONField(t, decoded, "base_branch", "main")

	// pr_number must be 42 (JSON number).
	if prNum, ok := decoded["pr_number"].(float64); !ok || int(prNum) != 42 {
		t.Errorf("expected pr_number=42, got %v", decoded["pr_number"])
	}

	// context.ci_failure_type must be "lint".
	ctx, ok := decoded["context"].(map[string]any)
	if !ok {
		t.Fatalf("expected context object, got %T", decoded["context"])
	}
	assertJSONField(t, ctx, "ci_failure_type", "lint")

	// skill_files must be a non-empty array.
	skillFiles, ok := decoded["skill_files"].([]any)
	if !ok || len(skillFiles) == 0 {
		t.Errorf("expected non-empty skill_files array, got %v", decoded["skill_files"])
	}
	// Each skill file path must be under skillDir.
	for _, sf := range skillFiles {
		sfStr, ok := sf.(string)
		if !ok {
			t.Errorf("skill_files entry is not a string: %v", sf)
			continue
		}
		if !strings.HasPrefix(sfStr, skillDir) {
			t.Errorf("skill file path %q does not start with skillDir %q", sfStr, skillDir)
		}
	}

	// output_contract must have format=json and required fields.
	oc, ok := decoded["output_contract"].(map[string]any)
	if !ok {
		t.Fatalf("expected output_contract object, got %T", decoded["output_contract"])
	}
	assertJSONField(t, oc, "format", "json")
	fields, ok := oc["fields"].([]any)
	if !ok || len(fields) == 0 {
		t.Errorf("expected non-empty output_contract.fields, got %v", oc["fields"])
	}
	// Verify required output fields are present.
	requiredFields := []string{"ok", "action_taken", "commits", "ci_status", "notes"}
	fieldSet := make(map[string]bool)
	for _, f := range fields {
		if fs, ok := f.(string); ok {
			fieldSet[fs] = true
		}
	}
	for _, rf := range requiredFields {
		if !fieldSet[rf] {
			t.Errorf("expected output_contract.fields to contain %q", rf)
		}
	}
}

// assertJSONField is a helper that checks a string field in a JSON-decoded map.
func assertJSONField(t *testing.T, m map[string]any, key, want string) {
	t.Helper()
	got, ok := m[key].(string)
	if !ok {
		t.Errorf("field %q: expected string, got %T (%v)", key, m[key], m[key])
		return
	}
	if got != want {
		t.Errorf("field %q: got %q, want %q", key, got, want)
	}
}

// TestBuildConflictFixEnvelope verifies that buildConflictFixEnvelope produces a
// valid Contract B JSON envelope with all required fields.
func TestBuildConflictFixEnvelope(t *testing.T) {
	skillDir := "/tmp/codex-config/skills"
	envelope := buildConflictFixEnvelope(
		"misty-step/my-repo",
		42,
		"https://github.com/misty-step/my-repo/pull/42",
		"feat/my-feature",
		"main",
		skillDir,
	)

	b, err := json.Marshal(envelope)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}

	var decoded map[string]any
	if err := json.Unmarshal(b, &decoded); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}

	// Top-level fields — Contract B spec.
	assertJSONField(t, decoded, "task", "conflict-fix")
	assertJSONField(t, decoded, "repo", "misty-step/my-repo")
	assertJSONField(t, decoded, "pr_url", "https://github.com/misty-step/my-repo/pull/42")
	assertJSONField(t, decoded, "branch", "feat/my-feature")
	assertJSONField(t, decoded, "base_branch", "main")

	// pr_number must be 42.
	if prNum, ok := decoded["pr_number"].(float64); !ok || int(prNum) != 42 {
		t.Errorf("expected pr_number=42, got %v", decoded["pr_number"])
	}

	// context must have conflicting_files as an array (may be empty).
	ctx, ok := decoded["context"].(map[string]any)
	if !ok {
		t.Fatalf("expected context object, got %T", decoded["context"])
	}
	conflictingFiles, ok := ctx["conflicting_files"].([]any)
	if !ok {
		t.Errorf("expected conflicting_files array in context, got %T", ctx["conflicting_files"])
	}
	// Contract B spec: conflicting_files starts as empty slice.
	if len(conflictingFiles) != 0 {
		t.Errorf("expected empty conflicting_files, got %v", conflictingFiles)
	}

	// skill_files must be a non-empty array of strings under skillDir.
	skillFiles, ok := decoded["skill_files"].([]any)
	if !ok || len(skillFiles) == 0 {
		t.Errorf("expected non-empty skill_files array, got %v", decoded["skill_files"])
	}
	for _, sf := range skillFiles {
		sfStr, ok := sf.(string)
		if !ok {
			t.Errorf("skill_files entry is not a string: %v", sf)
			continue
		}
		if !strings.HasPrefix(sfStr, skillDir) {
			t.Errorf("skill file path %q does not start with skillDir %q", sfStr, skillDir)
		}
	}

	// output_contract: format=json, fields include ok/action_taken/commits/notes.
	oc, ok := decoded["output_contract"].(map[string]any)
	if !ok {
		t.Fatalf("expected output_contract object, got %T", decoded["output_contract"])
	}
	assertJSONField(t, oc, "format", "json")
	fields, ok := oc["fields"].([]any)
	if !ok || len(fields) == 0 {
		t.Errorf("expected non-empty output_contract.fields, got %v", oc["fields"])
	}
	requiredFields := []string{"ok", "action_taken", "commits", "notes"}
	fieldSet := make(map[string]bool)
	for _, f := range fields {
		if fs, ok := f.(string); ok {
			fieldSet[fs] = true
		}
	}
	for _, rf := range requiredFields {
		if !fieldSet[rf] {
			t.Errorf("expected output_contract.fields to contain %q", rf)
		}
	}
}

// TestSpawnConflictFixSubagent verifies that spawnConflictFixSubagent dispatches
// openclaw agent with a Contract B envelope and the correct branch/repo info.
// It mocks the openclaw binary with a shell script that records its invocation.
func TestSpawnConflictFixSubagent(t *testing.T) {
	// Create a mock openclaw binary that records its arguments and exits 0.
	tmpDir := t.TempDir()
	mockBin := filepath.Join(tmpDir, "openclaw")
	recordFile := filepath.Join(tmpDir, "invocation.txt")

	mockScript := `#!/bin/sh
echo "$@" > ` + recordFile + `
exit 0
`
	if err := os.WriteFile(mockBin, []byte(mockScript), 0755); err != nil {
		t.Fatalf("write mock openclaw: %v", err)
	}

	// Patch the openclaw binary path used by spawnConflictFixSubagent.
	origBin := openclawBin
	openclawBin = mockBin
	t.Cleanup(func() { openclawBin = origBin })

	pr := searchPR{
		URL:    "https://github.com/misty-step/my-repo/pull/42",
		Number: 42,
		Repository: struct {
			NameWithOwner string `json:"nameWithOwner"`
		}{NameWithOwner: "misty-step/my-repo"},
	}
	view := &prView{
		URL:         "https://github.com/misty-step/my-repo/pull/42",
		HeadRefName: "feat/my-feature",
		BaseRefName: "main",
	}

	skillDir := t.TempDir() // empty — degraded mode, no skills loaded

	err := spawnConflictFixSubagent(pr, view, skillDir)
	if err != nil {
		t.Fatalf("spawnConflictFixSubagent: unexpected error: %v", err)
	}

	// Verify the mock was invoked and the message contains Contract B markers.
	recorded, readErr := os.ReadFile(recordFile)
	if readErr != nil {
		t.Fatalf("mock openclaw was not invoked (record file missing): %v", readErr)
	}
	invocation := string(recorded)

	// Must have dispatched to openclaw agent --agent eng --message ...
	if !strings.Contains(invocation, "agent") {
		t.Errorf("expected 'agent' subcommand in invocation, got: %s", invocation)
	}
	if !strings.Contains(invocation, "--agent") {
		t.Errorf("expected '--agent' flag in invocation, got: %s", invocation)
	}

	// The message must contain conflict-fix Contract B markers.
	if !strings.Contains(invocation, "conflict-fix") {
		t.Errorf("expected 'conflict-fix' task in invocation message, got: %s", invocation)
	}
	if !strings.Contains(invocation, "feat/my-feature") {
		t.Errorf("expected branch name in invocation message, got: %s", invocation)
	}
	if !strings.Contains(invocation, "misty-step/my-repo") {
		t.Errorf("expected repo in invocation message, got: %s", invocation)
	}
}

// TestSpawnConflictFixSubagent_dispatchErrorOnBadPRURL verifies that
// spawnConflictFixSubagent returns an error when the PR URL is unparseable.
func TestSpawnConflictFixSubagent_dispatchErrorOnBadPRURL(t *testing.T) {
	pr := searchPR{URL: "not-a-valid-url"}
	view := &prView{URL: "not-a-valid-url", HeadRefName: "feat/x", BaseRefName: "main"}

	err := spawnConflictFixSubagent(pr, view, t.TempDir())
	if err == nil {
		t.Error("expected error for bad PR URL, got nil")
	}
}

// TestSpawnConflictFixSubagent_openclaw_failure verifies that
// spawnConflictFixSubagent propagates openclaw dispatch errors.
func TestSpawnConflictFixSubagent_openclaw_failure(t *testing.T) {
	tmpDir := t.TempDir()
	mockBin := filepath.Join(tmpDir, "openclaw")

	// Mock that always fails.
	if err := os.WriteFile(mockBin, []byte("#!/bin/sh\nexit 1\n"), 0755); err != nil {
		t.Fatalf("write mock: %v", err)
	}

	origBin := openclawBin
	openclawBin = mockBin
	t.Cleanup(func() { openclawBin = origBin })

	pr := searchPR{URL: "https://github.com/misty-step/repo/pull/1"}
	view := &prView{
		URL:         "https://github.com/misty-step/repo/pull/1",
		HeadRefName: "feat/x",
		BaseRefName: "main",
	}

	err := spawnConflictFixSubagent(pr, view, t.TempDir())
	if err == nil {
		t.Error("expected error when openclaw exits non-zero, got nil")
	}
}
