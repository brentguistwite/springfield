# Springfield

![Springfield](docs/images/springfield.png)

Local-state conductor for multi-agent code work, distributed as a Claude Code (and Codex) marketplace plugin.

Springfield turns a plan (file or prompt) into a sequential batch of agent runs, executes each slice in an isolated git worktree, captures per-slice evidence, and falls through `agent_priority` (default: Claude → opt-in Codex → opt-in Gemini) when a run is retryable. State lives under `.springfield/` in the repo; install ships through Claude Code and Codex marketplace plugins.

> **Vendor economics:** `claude -p` headless invocations count against the Claude Max/Pro subscription, so Springfield defaults to Claude (Codex and Gemini are opt-in). Anthropic briefly metered `claude -p` separately (2026-05-14) before reverting; Springfield keeps that response — a Codex-led default plus a `springfield start` billing warning — one flag flip away (`ClaudeHeadlessMetered` in `internal/core/agents`) for if it returns. `--cost-cap $X` aborts the batch when spend hits the threshold regardless.

> "Plugin-distributed" here means Springfield is *installed via* the marketplace plugin flow. Springfield does not currently expose a plugin or extension API of its own.

When you run `springfield start`, the conductor will:

1. Load the next plan from the compiled batch.
2. Cut an isolated worktree on `springfield/<plan-id>` so the host clone stays untouched.
3. Dispatch the first id in `agent_priority` (default: Claude; Codex/Gemini opt-in) against the plan envelope.
4. Stream the agent's output to `.springfield/execution/plans/<plan-id>/evidence/iter-<N>/` and watch for the runner-sole-writer markers that signal pass/fail.
5. Fast-forward merge the plan back into your base branch on success; fall through to the next agent on a retryable failure.
6. Move to the next plan, repeating until the batch is complete or a fatal failure stops it.

> Pre-1.0. Config and state layout may change without migration shims.

## Prerequisites

- A git repository for the project Springfield runs against.
- At least one supported agent CLI installed and authenticated:
  - [Claude Code](https://docs.anthropic.com/en/docs/claude-code) (`npm install -g @anthropic-ai/claude-code`)
  - [Codex CLI](https://github.com/openai/codex)
  - [Gemini CLI](https://github.com/google-gemini/gemini-cli) (opt-in; set `GEMINI_API_KEY` or sign in headless)
- macOS (amd64/arm64) for the `brew` install path. Linux/Windows install the CLI via [Alternate Install Paths](#alternate-install-paths).
- Go 1.26+ if you're building the CLI from source (`go install .`); not needed for the brew install path.

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

Springfield ships as two pieces: the **CLI** (the `springfield` binary that does all the work) and a **plugin** (the skills/slash-commands your agent runs). Install both.

### 1. CLI — Homebrew (macOS)

```bash
brew install brentguistwite/tap/springfield
springfield version
```

`brew upgrade springfield` is the one upgrade you normally think about — new features land in the CLI and arrive here. Linux/Windows: install via [Alternate Install Paths](#alternate-install-paths).

### 2. Plugin — skills + slash commands

Claude Code:

```
/plugin marketplace add brentguistwite/springfield
/plugin install springfield@brentguistwite
```

Slash commands available after install: `/springfield:plan`, `/springfield:plan-from-issue`, `/springfield:status`, `/springfield:recover`. To execute a batch, run `springfield start` in a terminal. (For the Codex plugin, see [Codex Plugin Directory](#codex-plugin-directory).)

The plugin is a thin, stable shim over the CLI verbs (`plan`, `start`, `status`, `recover`), so it changes rarely. **Plugin updates are manual on both platforms** — run them only if a skill tells you to:

- Claude Code: `/plugin marketplace update springfield`, then `/plugin install springfield@brentguistwite`
- Codex: `codex plugin marketplace upgrade brentguistwite`

A plugin older than the CLI is the normal steady state and needs no action; the CLI stays backward-compatible with older skills within a major version. If a skill ever needs a newer CLI than you have, it will tell you to run `brew upgrade springfield`.

Manage the plugin:

```
/plugin list                               # verify install
/plugin uninstall springfield@brentguistwite
```

`/plugin marketplace add` accepts the `owner/repo` GitHub shorthand; use the full `https://github.com/brentguistwite/springfield.git` URL if your environment needs it.

### Alternate Install Paths

Install the CLI off the Homebrew path:

- Linux/Windows tarball: download from [Releases](https://github.com/brentguistwite/springfield/releases), then `tar -xzf springfield_<version>_<os>_<arch>.tar.gz && install -m 0755 springfield /usr/local/bin/springfield` (or place the binary anywhere on `PATH`). Upgrade by downloading the latest tarball and replacing the binary.
- Homebrew formula from a release asset (no tap): `brew install --formula https://github.com/brentguistwite/springfield/releases/download/vX.Y.Z/springfield.rb`.
- From source: `go install .` inside this repo.

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

For local slash-command / skill sync outside the plugin install flow (or as a fallback):

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
agent_priority = ["claude"]

[agents.claude]
model = "claude-sonnet-4-6"
permission_mode = "bypassPermissions"

# Opt in to Codex by adding "codex" to agent_priority; scaffold below.
# [agents.codex]
# model = "gpt-5-codex"
# sandbox_mode = "danger-full-access"
# approval_policy = "never"

# Opt in to Gemini by adding "gemini" to agent_priority; scaffold below.
# [agents.gemini]
# approval_mode = "yolo"
# sandbox_mode = "sandbox-exec"
```

Notes:

- `springfield init` runs an interactive TUI: multi-select agents (Claude is pre-checked; Codex and Gemini are opt-in), pick a model per agent (or take the adapter default), then confirm a summary before write. Shift+Tab navigates back; Esc edits any answer. For non-interactive installs, pass `--agents claude,codex` and optionally `--model claude=<id>,codex=<id>,gemini=<id>` — or pipe answers on stdin and Springfield falls through to huh's accessible plain-text mode.
- `springfield init` scaffolds `springfield.toml` + `.springfield/` with recommended execution settings for each selected agent.
- Gemini is execution-supported but opt-in. Pass `--agents claude,codex,gemini` (or edit `agent_priority`) to include it. See [`docs/release.md`](docs/release.md#2026-04-gemini-cli-execution-support) for the migration note.
- Primary install is two pieces: the CLI via Homebrew (or tarball/source on non-mac) and the plugin via the Claude or Codex marketplace — see [Install](#install).
- `springfield install` is an additive local-host sync (writes slash-command/skill helpers into `~/.claude/` and `~/.agents/`); use it when the plugin install flow isn't available, or as a fallback.
- Re-running `init` preserves existing config, only filling in missing recommended defaults and agent priority. Use `springfield init --reset` when `springfield.toml` has drifted (stale agent blocks, manual edits you want gone): it backs up the current `springfield.toml` and regenerates it from your `--agents`/`--model` selection. `--reset` rewrites `springfield.toml` and updates the execution config's primary tool to match your new agent priority; your registered plans are preserved.
- `auto_branch = true` (default) auto-cuts a feature branch (`springfield/batch-<id>`) as a bare ref when you run `springfield start` from `main` or `master` and lands the batch on it — without switching your worktree, so you stay on `main` for the whole run. See [Recommended Workflow](#recommended-workflow). Override the name with `auto_branch_pattern = "feat/{id}"` (only `{id}` is supported). Set `auto_branch = false` to disable.
- `allow_protected_base = false` (default) refuses to ff-merge plan results into `main` or `master`. Only consulted when `auto_branch = false` (otherwise the auto-cut feature branch becomes the base and the guard does not apply).
- `branch_mode = "consolidate"` (default) merges every plan in a batch onto one shared base (one PR for the batch). Set `branch_mode = "per-plan"` to instead leave one standalone `springfield/<plan>` branch per plan (one PR per ticket) — nothing merges into the base. Override per-run with `springfield start --per-plan-branches`. See [Per-plan branches](#per-plan-branches).
- `base_branch = "develop"` sets the base each per-plan branch is cut from (per-plan mode only). Precedence: `--base` flag > `base_branch` > the current branch. Unattended controllers should set this so the current-branch fallback stays a deliberate, manual-only choice.
- Runtime state under `.springfield/` is local project state and should not be committed. `springfield.local.toml` (per-operator review overrides) is also git-ignored by `springfield init`.

### `agent_priority` semantics

> **Billing note:** `claude -p` currently counts against the Claude Max/Pro subscription, so new projects default `agent_priority` to `["claude"]` and `springfield start` prints no billing warning. If Anthropic re-meters `claude -p` separately (as briefly happened 2026-05-14), flip `ClaudeHeadlessMetered` (`internal/core/agents`) to restore the `["codex"]` default and the stderr warning. See [Cost visibility](#cost-visibility) for the cost-capture surface and `--cost-cap` flag.

`agent_priority` is an ordered list of agent IDs. Springfield always starts with the **first** id for each plan dispatch and only advances down the list on a retryable failure.

**Failover triggers, evaluated in this order:**

1. **Rate-limit cooldown** — the Claude adapter (only) implements the `Cooldowner` interface and, on a rate-limited response, extracts a "do-not-retry-before" timestamp from the error. Claude is then skipped on subsequent plan dispatches until that timestamp passes. The parsed cooldown is capped at 24h beyond "now". Codex and Gemini do not implement `Cooldowner`, so neither is ever skipped on cooldown.
2. **Retryable error** — all three adapters (Claude, Codex, Gemini) implement `ErrorClassifier` and classify their own retryable patterns (rate-limit without an extractable cooldown, transient API errors, etc.). When `ClassifyError` returns `retryable`, the runner advances to the next id in `agent_priority` for the same plan.
3. **Fatal error** — when `ClassifyError` returns `fatal` (the default), the batch stops; no further fallback. The plan is left in `failed` state and the run records the error.

**What it is NOT:**

- **Not round-robin.** Order is deterministic; the first eligible agent wins every dispatch.
- **Not sticky-per-plan.** A plan that failed over to Codex once does not stay on Codex for retries — each retry re-evaluates from the top of `agent_priority`.
- **Not load-balanced.** No cost-aware or capacity-aware routing.

**Operator override:** there is no per-run flag to skip the failover chain. To force a single agent, list only that agent in `agent_priority`.

## Cost visibility

Springfield captures per-iteration spend for Claude and Codex and surfaces it in two places:

- **Per iteration:** every agent dispatch writes `cost.json` alongside `meta.json` under `.springfield/execution/plans/<plan-id>/evidence/iter-<N>/`. Fields: `adapter`, `model`, `input_tokens`, `output_tokens`, `cost_usd`, `captured_at`. Token counts come from the agent's own stream events; USD is computed against a static pricing table (`internal/features/cost/pricing.go`). Claude's terminal `total_cost_usd` wins over the table when present, so prompt-caching discounts are reflected.
- **Live rollup:** `springfield status` prints a `Spend: $X.YZ (claude $A.BC, codex $D.EF)` line for the active batch. `springfield start` prints a `Total spend:` line after `Status: completed`.

Gemini cost capture is not implemented (the CLI does not expose a token usage surface today). Gemini iterations contribute zero cost; when present they appear as `(N unpriced — likely gemini)`.

### Capping spend

`springfield start --cost-cap $X` aborts the batch when the cumulative rollup reaches `$X` USD. The check fires immediately after each iteration's `cost.json` is written: the iteration that crossed the threshold finishes (its evidence is already on disk), the next iteration does not dispatch, and the plan is persisted as `interrupted` for clean resumption.

Cap-aborted batches are **resumable**, not terminal:

```
$ springfield start --cost-cap 5.00
Status: cost-capped
Spend: $5.12 (cap: $5.00)
Info: rerun with --cost-cap $Y to continue (Y > current spend) or remove claude from agent_priority to reduce spend

$ springfield start --cost-cap 10.00   # resumes from where it stopped
```

The resume cap must be strictly greater than the current spend. Without `--cost-cap`, a cost-capped batch refuses to start so the operator must make a deliberate choice.

`springfield status` on a cost-capped batch prints `Status: cost-capped` rather than `Fatal error:` — the two are intentionally distinct: a fatal error needs `springfield recover`; a cost cap just needs a higher threshold.

### Historical estimates

Archived batches retain a `total_usd` field; when the `claude -p` billing warning is active (see the billing note under [`agent_priority` semantics](#agent_priority-semantics)) it reads the last 5 archives' per-plan mean and renders a `~$X.XX–$Y.YY` range so the cost message has a concrete number. Fresh projects render `(no prior batches)` rather than fabricate a number.

When that warning is active, silence it per-operator with `SPRINGFIELD_SUPPRESS_CLAUDE_BILLING_WARNING=1`. The warning is dormant by default while `claude -p` is subscription-billed.

## Recommended Workflow

Springfield runs each plan in an isolated git worktree, then ff-merges the result back into a base branch on your **local** clone. Nothing is pushed and no PR is opened — that step is yours.

The base branch defaults to whatever you have checked out when `springfield start` runs. Because most teams gate `main` and `master` behind PR review, Springfield's default behavior is to **auto-cut a feature branch for you** when you start from one of them and land the batch on that branch. Your worktree never leaves the branch you started on — the auto-branch is created as a bare ref and the merges are published to it directly, so you can keep working on `main` while the batch runs.

```bash
git switch main                     # any starting point is fine
springfield plan --prd payload.json
springfield start
# Springfield prints:
#   auto-cut branch springfield/batch-<id> from main
#     → slice work merges here; you stay on main
#     → push + open PR by hand when the batch finishes
#
# ... batch runs (you stay on main the whole time) ...
#
#   batch complete on springfield/batch-<id> (you are on main)
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

### Per-plan branches

The default consolidate mode lands a whole batch on one branch (one PR). **Per-plan mode** instead leaves one standalone `springfield/<plan>` branch per plan, each cut from the same base — so a batch of N tickets yields N independent branches you open as N PRs. Nothing is merged into the base, so auto-branching and the protected-base guard are both suppressed (there is nothing to consolidate onto).

Enable it per-run or as a project default:

```bash
springfield start --per-plan-branches --base develop
```

```toml
[project]
branch_mode = "per-plan"
base_branch = "develop"
```

Base precedence is `--base` > `[project] base_branch` > the branch you have checked out. On a detached HEAD with no `--base`/`base_branch`, Springfield refuses rather than guess a base.

On clean success each plan's worktree is removed but its branch is **kept** (Springfield never pushes — `git push` + PR is yours):

```bash
git push -u origin springfield/<plan-a>
git push -u origin springfield/<plan-b>
gh pr create --base develop --head springfield/<plan-a>
```

The mode is fixed when the batch first starts and is **not** flippable on resume — a re-passed `--per-plan-branches` (or its absence) cannot flip a batch already underway, since that would merge the front half and retain the back half. A re-passed `--base` on resume only re-bases plans that have not run yet.

**Controller / dashboard guidance:** always set `base_branch` so the current-branch fallback never silently picks an unexpected base in an unattended run. After a batch completes, `springfield status --json` reports the archived batch (`state: "archived"`) with one card per ticket — branch, base, and durable evidence path — so a controller can read back each ticket's result and branch even though the run cursor has been cleared.

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
5. Stream the agent's output to `.springfield/execution/plans/<plan-id>/evidence/iter-<N>/`.
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

# Drop into the plan's per-iteration evidence files directly.
# `springfield status` prints the live evidence path after a run settles;
# otherwise ls the runner's output directly:
ls .springfield/execution/plans/<plan-id>/evidence/iter-<N>/
#   meta.json          — runner verdict, timing, exit code
#   events.jsonl       — every dispatched event (one per line)
#   assistant_text.txt — what the agent actually said
#   prompt.txt         — the exact prompt the runner built

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
| `.springfield/execution/plans/<plan-id>/evidence/iter-<N>/` | no | Per-iteration agent output: `meta.json`, `events.jsonl`, `assistant_text.txt`, `prompt.txt` |
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

The Homebrew formula (`springfield.rb`) and the tap consume these tarballs; `brew install brentguistwite/tap/springfield` resolves to the matching one. Manual install instructions live under [Alternate Install Paths](#alternate-install-paths).

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

Springfield uses [release-please](https://github.com/googleapis/release-please) on `main` to maintain a single open release PR driven by [Conventional Commits](https://www.conventionalcommits.org/). A hydration workflow on that PR runs `go run ./cmd/release-sync` to keep `version.txt` and every plugin/marketplace manifest in lock-step. Merging the release PR creates the tag, which triggers the publish workflow (build tarballs, render the Homebrew formula, push it to the tap). Maintainer details live in [docs/release.md](docs/release.md).

## License

MIT — see [LICENSE](LICENSE).
