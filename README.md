# fab-pr-pipeline

Automated PR review and merge pipeline for GitHub. This tool scans open PRs in a GitHub organization, checks CI status and review state, and either merges ready PRs or comments with blockers.

## What It Does

The pipeline performs the following actions for each open PR:

1. **Filters PRs**: Skips draft PRs and those marked with the "do not touch" label
2. **Applies stale policy**: For Phaedrus-authored PRs, waits until they're stale (72+ hours without updates) before acting
3. **Checks merge readiness**:
   - Mergeable state (`MERGEABLE`)
   - CI checks passing (`SUCCESS`)
   - Review approved (`APPROVED` or no pending reviews)
4. **Actions taken**:
   - **Merges** PRs that meet all criteria
   - **Comments** on PRs that can't be merged, explaining the blocker
   - **Skips** PRs in circuit-breaker open state, archived repos, or filtered out
5. **Reports**: Posts run summary to Discord (optional)

## Installation

```bash
go install github.com/misty-step/fab-pr-pipeline@latest
```

Requires [GitHub CLI (`gh`)](https://cli.github.com/) installed and authenticated.

## Usage

### Basic Run

```bash
# Scan and process up to 5 PRs from misty-step org
fab-pr-pipeline
```

### Command-Line Flags

| Flag | Default | Description |
|------|---------|-------------|
| `-org` | `misty-step` | GitHub org/owner to scan |
| `-max-prs` | `5` | Maximum PRs to process per run |
| `-stale-hours` | `72` | Hours of inactivity before acting on Phaedrus PRs |
| `-phaedrus-login` | `phrazzld` | GitHub username for Phaedrus (stale policy applies only to this author) |
| `-kaylee-login` | `kaylee-mistystep` | GitHub username for Kaylee (acts immediately, no stale wait) |
| `-do-not-touch-label` | `do not touch` | Label that marks PRs to skip (case-insensitive) |
| `-dry-run` | `false` | Report actions without executing merges or comments |
| `-discord-report-to` | (empty) | Discord channel for run summaries (e.g., `channel:123456` or raw ID) |
| `-discord-alerts-to` | (empty) | Discord channel for error alerts |
| `-post-empty` | `false` | Post report even when no PRs were acted on |
| `-post-dry-run` | `false` | Allow posting report when `--dry-run` is set |
| `-cb-failures` | `3` | Circuit breaker: consecutive failures before skipping a PR |
| `-cb-skip-runs` | `5` | Circuit breaker: runs to skip after opening |

### Examples

```bash
# Dry run to see what would happen
fab-pr-pipeline --dry-run

# Process more PRs per run
fab-pr-pipeline --max-prs 10

# Target a different org
fab-pr-pipeline --org my-org

# Post results to Discord
fab-pr-pipeline --discord-report-to channel:123456789 \
  --discord-alerts-to channel:987654321
```

## Configuration

### Environment Variables

| Variable | Required | Description |
|----------|----------|-------------|
| `DISCORD_BOT_TOKEN` | When using Discord features | Bot token for posting to Discord |

### "Do Not Touch" Logic

A PR is skipped if:
1. It has a label matching the `-do-not-touch-label` flag (case-insensitive), OR
2. Its title or body contains "do not touch" (case-insensitive)

### Circuit Breaker

The circuit breaker prevents one bad PR from consuming the entire error budget:
- After `N` consecutive failures on a PR (default: 3), the circuit "opens"
- The PR is skipped for `M` subsequent runs (default: 5)
- After the skip period, the circuit "closes" and the PR is retried
- Success resets the failure counter

## How It Works

### Processing Order

PRs are processed in order of most recently updated first, prioritizing fresh PRs that are more likely to have up-to-date CI results.

### Merge Criteria

A PR is merged only when ALL of these conditions are met:

1. **Not a draft** (`IsDraft: false`)
2. **Mergeable** (`mergeable: MERGEABLE`)
3. **CI checks passing** (`checks: SUCCESS`)
4. **Review approved** (`reviewDecision: APPROVED` or empty; not `CHANGES_REQUESTED` or `REVIEW_REQUIRED`)

### Comment Content

When a PR can't be merged, the pipeline comments with:

```
Kaylee PR pipeline: not merged automatically.

- mergeable: `MERGEABLE` / `CONFLICTING` / `UNKNOWN`
- checks: `SUCCESS` / `FAILURE` / `PENDING`
- reviewDecision: `APPROVED` / `CHANGES_REQUESTED` / `REVIEW_REQUIRED`
- reason: `<specific blocker>`

Next action: make checks green and resolve review blockers; rerun pipeline.
```

### Archived Repos

PRs in archived repositories are skipped silently (they're read-only and can't accept comments).

### Error Classification

Errors are classified as:
- **Permanent**: Don't retry (e.g., 404, archived, permission denied, already merged)
- **Transient**: Worth retrying (e.g., rate limits, timeouts, network errors)

The pipeline retries transient errors up to 3 times with exponential backoff.

## Integration

### OpenClaw Cron

This tool is called by OpenClaw's daily cron job as part of the factory automation:

```
# Called daily to process pending PRs
fab-pr-pipeline --org misty-step
```

### Discord Reporting

When configured, the pipeline posts:
- **Run summary**: Merged/commented/skipped counts, per-PR results
- **Error alerts**: When errors occur during execution

## Exit Codes

| Code | Meaning |
|------|---------|
| `0` | Success (ran to completion, errors posted to Discord if configured) |
| `1` | Failure (permanent error or Discord posting failed) |

The tool always outputs JSON to stdout with the run result, even on error.

## JSON Output

The tool outputs JSON to stdout for machine parsing:

```json
{
  "ok": true,
  "startedAt": "2025-01-15T10:30:00Z",
  "org": "misty-step",
  "maxPRs": 5,
  "staleHours": 72,
  "dryRun": false,
  "results": [
    {
      "url": "https://github.com/misty-step/repo/pull/123",
      "repo": "misty-step/repo",
      "number": 123,
      "author": "kaylee-mistystep",
      "action": "merged",
      "mergeCommitOid": "abc123"
    }
  ]
}
```

Possible actions: `merged`, `commented`, `skipped`, `error`

## Contributing

Standard Go contribution workflow:

```bash
# Fork and clone
git clone https://github.com/your-fork/fab-pr-pipeline.git
cd fab-pr-pipeline

# Create a feature branch
git checkout -b your-name/feature

# Make changes and test
go build ./...
go test ./...

# Commit with descriptive message
git commit -m "description of changes"

# Push and create PR
git push -u origin your-name/feature
```

### Running Tests

```bash
go test ./...
```

### Code Style

- Run `go fmt` before committing
- Ensure code compiles with `go build ./...`
