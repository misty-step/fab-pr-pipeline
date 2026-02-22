package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

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
	Format string   `json:"format"`
	Fields []string `json:"fields"`
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

	_, err = runCmd("/opt/homebrew/bin/openclaw", "agent", "--agent", "eng", "--message", msg)
	if err != nil {
		return fmt.Errorf("spawnCIFixSubagent: openclaw agent dispatch: %w", err)
	}
	return nil
}
