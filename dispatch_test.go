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
