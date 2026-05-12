# Springfield

![Springfield](docs/images/springfield.png)

Plugin-first, local-state conductor for multi-agent code work.

Springfield turns a plan (file or prompt) into a sequential batch of agent runs, executes each slice in an isolated git worktree, captures per-slice evidence, and falls through `agent_priority` (Claude → Codex → Gemini) when a run is retryable. State lives under `.springfield/` in the repo; install ships through Claude Code and Codex marketplace plugins.

When you run `springfield start`, the conductor will:

1. Load the next plan from the compiled batch.
2. Cut an isolated worktree on `springfield/<plan-id>` so the host clone stays untouched.
3. Dispatch the first id in `agent_priority` (Claude → Codex → Gemini) against the plan envelope.
4. Stream the agent's output to `.springfield/plans/<batch-id>/evidence/<slice-id>/` and watch for the runner-sole-writer markers that signal pass/fail.
5. Fast-forward merge the plan back into your base branch on success; fall through to the next agent on a retryable failure.
6. Move to the next plan, repeating until the batch is complete or a fatal failure stops it.

> Pre-1.0. Config and state layout may change without migration shims.

## Prerequisites

- A git repository for the project Springfield runs against.
- At least one supported agent CLI installed and authenticated:
  - [Claude Code](https://docs.anthropic.com/en/docs/claude-code) (`npm install -g @anthropic-ai/claude-code`)
  - [Codex CLI](https://github.com/openai/codex)
  - [Gemini CLI](https://github.com/google-gemini/gemini-cli) (opt-in; set `GEMINI_API_KEY` or sign in headless)
- macOS or Linux (amd64/arm64) for the plugin auto-install path. Windows installs via [Alternate Install Paths](#alternate-install-paths).
- `~/.local/bin` on `PATH` if installing through the Claude marketplace plugin (the SessionStart hook symlinks the binary there).
- Go 1.26+ if you're building from source (`go install .`); not needed for the plugin install path.

## Public CLI

```bash
springfield            # Show help and next-step guidance
springfield init       # Scaffold springfield.toml and .springfield/
springfield install    # Sync local Claude Code/Codex host artifacts
springfield doctor     # Check local agent CLI availability
springfield plan       # Compile a work request into a runnable batch
springfield plans      # Manage the registered plan-unit registry
springfield start      # Execute the active batch from its saved cursor
springfield status     # Inspect the active batch or work
springfield recover    # Recover from a failed plan or orphaned batch
springfield version    # Print build version
```

Supported agents (all fully executable):

- Claude Code
- Codex CLI
- Gemini CLI (opt-in via `--agents ...,gemini` or by adding `"gemini"` to `agent_priority`; set `GEMINI_API_KEY` or sign in to the Gemini CLI before running headless)

## Install

Primary path (one step for both plugin skills and CLI binary):

```
/plugin marketplace add brentguistwite/springfield
/plugin install springfield@brentguistwite
```

The plugin ships a `SessionStart` hook that downloads the matching `springfield` CLI binary from the GitHub release pinned by the installed plugin version, verifies it against the plugin-shipped `hooks/checksums.txt` manifest, caches it under `~/.cache/springfield/<version>/`, and symlinks it to `~/.local/bin/springfield`. Add `~/.local/bin` to your `PATH` once; afterwards every `/plugin update springfield@brentguistwite` refreshes the plugin skills **and** the CLI binary in a single step — no `go install` or `brew upgrade` needed.

Slash commands available after install: `/springfield:plan`, `/springfield:status`, `/springfield:recover`. To execute a batch, run `springfield start` in a terminal.

Manage the install:

```
/plugin list                               # verify install
/plugin update springfield@brentguistwite  # pull latest plugin + CLI
/plugin uninstall springfield@brentguistwite
```

`/plugin marketplace add` accepts the `owner/repo` GitHub shorthand; use the full `https://github.com/brentguistwite/springfield.git` URL if your environment needs it.

### Alternate Install Paths

Use these only if you need the CLI outside the plugin flow:

- Tarball: download from [Releases](https://github.com/brentguistwite/springfield/releases), then `tar -xzf springfield_<version>_<os>_<arch>.tar.gz && install -m 0755 springfield /usr/local/bin/springfield`.
- Homebrew formula from release asset: `brew install --formula https://github.com/brentguistwite/springfield/releases/download/vX.Y.Z/springfield.rb`.
- From source: `go install .` inside this repo.

The SessionStart hook currently supports macOS and Linux (amd64/arm64). Windows CLI users must install via the alternate paths above.

### Codex Plugin Directory

Inside Codex, add the GitHub-backed marketplace, then install Springfield from the plugin directory:

```bash
codex plugin marketplace add brentguistwite/springfield --sparse .agents/plugins
codex
```

Then inside Codex:

```text
/plugins
```

Choose the `Brent Guistwite` marketplace, then install `springfield`.

Manage the marketplace with:

```bash
codex plugin marketplace upgrade
codex plugin marketplace upgrade brentguistwite
codex plugin marketplace remove brentguistwite
```

Notes:

- `codex plugin marketplace add` accepts GitHub shorthand (`owner/repo`), Git URLs, SSH Git URLs, and local marketplace roots.
- For Codex GitHub installs, this repo exposes the marketplace from `.agents/plugins/marketplace.json` and resolves the actual plugin from the repo root as a Git-backed plugin source.
- This repo also ships `.claude-plugin/marketplace.json` for Claude-style marketplace discovery.
- If the marketplace or plugin does not appear immediately, restart Codex once and reopen `/plugins`.

## Quick Start

Build from source:

```bash
go install .
springfield version
```

Inside a project:

```bash
springfield init
springfield doctor
```

If you need local host integration instead of marketplace/catalog install:

```bash
springfield install
springfield doctor
```

By default `springfield install` writes deterministic local artifacts:

- `~/.claude/commands/springfield.md`
- `~/.agents/skills/springfield/SKILL.md`

These local artifacts carry the shared Springfield playbook plus project context from `AGENTS.md`, `CLAUDE.md`, or `GEMINI.md` when present.

## Configuration

Springfield resolves the project root from `springfield.toml` at the repo root.
Run `springfield init` and follow the prompt to scaffold one.

Project-level agent execution settings live in `springfield.toml`. Example:

```toml
[project]
agent_priority = ["claude", "codex"]

[agents.claude]
model = "claude-sonnet-4-6"
permission_mode = "bypassPermissions"

[agents.codex]
model = "gpt-5-codex"
sandbox_mode = "danger-full-access"
approval_policy = "never"

# Opt in to Gemini by adding "gemini" to agent_priority; scaffold below.
# [agents.gemini]
# approval_mode = "yolo"
# sandbox_mode = "sandbox-exec"
```

Notes:

- `springfield init` runs an interactive TUI: multi-select agents, pick a model per agent (or take the adapter default), then confirm a summary before write. Shift+Tab navigates back; Esc edits any answer. For non-interactive installs, pass `--agents claude,codex` and optionally `--model claude=<id>,codex=<id>,gemini=<id>` — or pipe answers on stdin and Springfield falls through to huh's accessible plain-text mode.
- `springfield init` scaffolds `springfield.toml` + `.springfield/` with recommended execution settings for each selected agent.
- Gemini is execution-supported but opt-in. Pass `--agents claude,codex,gemini` (or edit `agent_priority`) to include it. See [`docs/release.md`](docs/release.md#2026-04-gemini-cli-execution-support) for the migration note.
- Primary end-user install is the Claude marketplace or Codex plugin/catalog flow.
- `springfield install` is the local sync/bootstrap/fallback path after `init`.
- Re-running `init` preserves existing config, only filling in missing recommended defaults and agent priority. Use `springfield init --reset` to back up the current config and rewrite it from scratch.
- `auto_branch = true` (default) auto-cuts a feature branch (`springfield/batch-<id>`) when you run `springfield start` from `main` or `master`, switches to it for the run, and switches you back when the batch finishes. See [Recommended Workflow](#recommended-workflow). Override the name with `auto_branch_pattern = "feat/{id}"` (only `{id}` is supported). Set `auto_branch = false` to disable.
- `allow_protected_base = false` (default) refuses to ff-merge plan results into `main` or `master`. Only consulted when `auto_branch = false` (otherwise the auto-cut feature branch becomes the base and the guard does not apply).
- Runtime state under `.springfield/` is local project state and should not be committed.

## Recommended Workflow

Springfield runs each plan in an isolated git worktree, then ff-merges the result back into a base branch on your **local** clone. Nothing is pushed and no PR is opened — that step is yours.

The base branch defaults to whatever you have checked out when `springfield start` runs. Because most teams gate `main` and `master` behind PR review, Springfield's default behavior is to **auto-cut a feature branch for you** when you start from one of them, run the batch on that branch, and switch you back to where you started when the batch finishes.

```bash
git switch main                     # any starting point is fine
springfield plan --prd payload.json
springfield start
# Springfield prints:
#   auto-cut branch springfield/batch-<id> from main
#     → all slice work will merge here
#     → switching back to main on finish (push + PR by hand)
#
# ... batch runs ...
#
#   batch complete on springfield/batch-<id>
#   switched back to main
#   push + open PR:
#     git push -u origin springfield/batch-<id>
#     gh pr create
git push -u origin springfield/batch-<id>
gh pr create --base main
```

Each plan still gets its own `springfield/<plan-key>` branch and its own `.worktrees/<plan-key>` worktree; the merges just land on the auto-cut batch branch. Reviewers see clean ff-merged history on the batch branch and review the work as one PR.

The auto-cut branch is local-only. Springfield never pushes — that step is yours.

Customize the branch name in `springfield.toml`:

```toml
[project]
auto_branch_pattern = "feat/{id}"   # only {id} (batch ID) is supported
```

To disable auto-branching (and fall back to the legacy refuse-or-allow guard):

```toml
[project]
auto_branch = false                  # turn off auto-cut
allow_protected_base = true          # only consulted when auto_branch = false
```

With `auto_branch = false` and `allow_protected_base = false`, Springfield refuses to start on `main`/`master` with `preflight-protected-base` and asks you to switch to a feature branch first.

Per-plan override: set `Ref = "feat/other"` on a `PlanUnit` to integrate that one plan into a different feature branch.

> Drift caveat: ff-only merge refuses if the base branch advanced between plan start and integrate. Pick a base branch you control during the run; don't `git pull` mid-batch.

## Critical Concepts

- **PRD with user stories** — one or more atomic stories per plan; runner loops until `<promise>COMPLETE</promise>` or iteration cap. Story completion is reported via `<story-pass>US-XXX</story-pass>` output markers. Springfield writes back per-story `passes` state; agents never write to `.springfield/`.

## Runtime Flow

Use `plan` to compile a work request into a runnable batch, then `start` to execute it.

The `springfield:plan` skill reads your plan (file or prompt), decides plan boundaries, and emits a PRD envelope to `springfield plan --prd -`:

```bash
# Skill pipes the payload for you; direct usage looks like:
springfield plan --prd path/to/payload.json

# Or via stdin:
springfield plan --prd - <<'JSON'
{
  "title": "auth scaffold",
  "source": "<plan markdown verbatim>",
  "phases": [
    {"mode": "serial", "plans": ["01-auth"]}
  ],
  "plans": [
    {
      "id": "01-auth",
      "title": "Auth scaffold",
      "description": "Bootstrap the auth package and wire one login endpoint.",
      "context_md": "Project uses TypeScript + Bun. Follow existing test patterns in src/auth/__tests__.",
      "user_stories": [
        {
          "id": "US-001",
          "title": "Scaffold auth package",
          "description": "Create src/auth/ with package.json + initial types",
          "acceptance_criteria": ["src/auth/package.json present", "bun install succeeds"],
          "priority": 1,
          "passes": false,
          "deps": []
        },
        {
          "id": "US-002",
          "title": "Wire login endpoint",
          "description": "Add POST /login that issues a JWT",
          "acceptance_criteria": ["POST /login returns 200 with valid creds", "JWT verifies with shared secret"],
          "priority": 2,
          "passes": false,
          "deps": ["US-001"]
        }
      ]
    }
  ]
}
JSON

# Execute the compiled batch
springfield start

# Check progress
springfield status
```

When you run `springfield start`, Springfield will:

1. Load the active batch from `.springfield/run.json`.
2. Pick the next `queued` slice in the active phase.
3. Snapshot the control plane (`batch.json`, `run.json`, `source.md`) for tamper detection.
4. Spawn a fresh agent run for that slice using the first id in `agent_priority`.
5. Stream the agent's output to `.springfield/plans/<batch-id>/evidence/<slice-id>/`.
6. If the agent fails with a retryable error, fall through to the next id in `agent_priority`. Fatal failures stop the batch immediately.
7. Mark the slice `done` (or `failed`) and persist the result to disk.
8. Repeat until every slice is terminal, then archive the batch and clear the run cursor.

Execution is serial by default. Parallel execution only happens when the batch explicitly marks independent phases — this is rare and must be intentional.

`springfield status` shows the per-slice evidence path after a run settles, and `springfield recover --diagnose` points at the batch evidence directory for orphaned runs.

If a batch already exists, use `--replace` to archive it and start fresh, or `--append` to add new plans:

```bash
springfield plan --replace --prd new-payload.json
springfield plan --append  --prd extra-plans.json
```

Use `springfield doctor` whenever local agent tooling looks unhealthy or a host CLI is missing.

## Debugging a stuck run

When a batch stalls or an agent run fails in a way you don't recognise, work from these commands. Everything below is read-only — only `springfield recover --plan <id>` *without* `--diagnose` mutates state.

```bash
# Where is the run? What's in flight? Which agent is up next?
springfield status

# Read-only triage for one plan: surfaces the most recent evidence,
# the plan's status, and what next-step Springfield would take.
springfield recover --diagnose --plan <plan-id>

# Read-only triage for an orphaned batch (run.json points at a batch
# directory that's been deleted or never wrote):
springfield recover --diagnose

# Drop into the slice's evidence files directly. `springfield status`
# prints the live evidence path after a run settles; otherwise:
ls .springfield/plans/<batch-id>/evidence/<slice-id>/
#   meta.json          — runner verdict, timing, exit code
#   events.jsonl       — every dispatched event (one per line)
#   assistant_text.txt — what the agent actually said
#   prompt.txt         — the exact prompt the runner built
#
# Note: the conductor's per-plan runner can additionally write
# per-iteration evidence under .springfield/execution/plans/<plan-id>/
# evidence/iter-<N>/ — check both paths when triaging a stuck run.

# Inspect the active-run cursor (which batch + phase + plans are live):
cat .springfield/run.json
```

`springfield recover --plan <plan-id>` (without `--diagnose`) is the only one of these that mutates state — it resets a failed plan back to `queued` so `springfield start` can re-dispatch it.

## Key Files

| Path | Tracked | Purpose |
|------|---------|---------|
| `springfield.toml` | yes | Project config: `agent_priority`, per-agent model, execution settings |
| `.springfield/plans/<id>.md` | yes | Plan source files registered with `springfield plans add` |
| `.springfield/run.json` | no | Active run cursor: which batch + phase is in flight |
| `.springfield/plans/<batch-id>/batch.json` | no | Compiled batch state (per-slice statuses) |
| `.springfield/plans/<batch-id>/source.md` | no | Frozen plan source for the batch |
| `.springfield/plans/<batch-id>/evidence/<slice-id>/` | no | Per-slice agent output: `meta.json`, `events.jsonl`, `assistant_text.txt`, `prompt.txt` |
| `.springfield/archive/<batch-id>.json` | no | Compact summary written when a batch completes or is replaced |
| `.springfield/execution/config.json` | no | Internal conductor config (derived from `springfield.toml`) |
| `.springfield/.lock` | no | Process lock for the active run |

Tracked files commit alongside your code; runtime files live under `.springfield/` and are gitignored except `.springfield/plans/<id>.md`.

## Release Assets

Tagged releases publish:

- `springfield_<version>_darwin_amd64.tar.gz`
- `springfield_<version>_darwin_arm64.tar.gz`
- `springfield_<version>_linux_amd64.tar.gz`
- `springfield_<version>_linux_arm64.tar.gz`
- `springfield.rb`

The SessionStart hook downloads the matching tarball automatically on `/plugin install` / `/plugin update`, then verifies the extracted binary against plugin-shipped `hooks/checksums.txt`. Manual install instructions live under [Alternate Install Paths](#alternate-install-paths).

## Development

```bash
go test ./...
go run . --help
go run . install --help
go run ./cmd/regen        # regenerate skills/*/SKILL.md + commands/*.md from internal/features/skills/types.go
```

### Pre-commit hook

A pre-commit hook in `.githooks/pre-commit` runs `go test ./...` before every commit. Activate once per clone:

```bash
git config core.hooksPath .githooks
```

## Release Workflow

Springfield uses [release-please](https://github.com/googleapis/release-please) on `main` to maintain a single open release PR driven by [Conventional Commits](https://www.conventionalcommits.org/). A hydration workflow on that PR runs `go run ./cmd/release-sync` to keep `version.txt`, every plugin/marketplace manifest, and `hooks/checksums.txt` in lock-step. Merging the release PR creates the tag, which triggers the publish workflow. Maintainer details live in [docs/release.md](docs/release.md).

## License

MIT — see [LICENSE](LICENSE).
