# PRD Envelope Format

Schema reference for skill authors and operators. Single source of truth for the JSON payload that `springfield plan --prd` accepts.

## Overview

A PRD envelope is a per-plan structured spec that replaces the one-paragraph slice summary used in earlier Springfield versions. Each plan in the envelope carries an ordered list of user stories with acceptance criteria. Springfield uses the stories to build the agent prompt and writes back per-story `passes` state as the agent reports completion via output markers. The runner drives an iteration loop per plan: it re-runs the agent until all stories report `<story-pass>US-XXX</story-pass>` or the configured iteration cap is hit.

## Envelope Shape

Full example with field-by-field commentary:

```json
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
      "review": true,
      "verify": {"command": "bun test", "enabled": true},
      "user_stories": [
        {
          "id": "US-001",
          "title": "Scaffold auth package",
          "description": "Create src/auth/ with package.json + initial types",
          "acceptance_criteria": ["src/auth/package.json present", "bun install exits 0"],
          "priority": 1,
          "passes": false,
          "deps": []
        },
        {
          "id": "US-002",
          "title": "Wire login endpoint",
          "description": "Add POST /login that issues a JWT",
          "acceptance_criteria": ["POST /login returns 200 with valid creds", "GET /verify returns 200 for the issued JWT"],
          "priority": 2,
          "passes": false,
          "deps": ["US-001"]
        }
      ]
    }
  ]
}
```

Field reference:

| Field | Type | Required | Notes |
|-------|------|----------|-------|
| `title` | string | yes | Human-readable batch title. |
| `source` | string | yes | Verbatim plan markdown that originated this batch. Stored as `source.md` for audit. |
| `phases` | array | yes | Execution ordering. At least one phase required. |
| `phases[].mode` | string | yes | `"serial"` or `"parallel"`. Serial phases run their plans one at a time in declared order. Parallel phases declare the plans independent; in per-plan-branches mode they actually execute concurrently (up to `[project] max_parallel`, default 3) — in consolidate mode they still run sequentially, since consolidate merges require a stable base. Port-based test-resource conflicts are handled mechanically: every slice gets a disjoint [per-slice port block](#per-slice-port-blocks) (`SPRINGFIELD_PORT` / `SPRINGFIELD_PORT_RANGE`), so plans that bind to those need no manual coordination. Only mark a phase parallel when its plans touch disjoint files and share no *non-port* test resources — containers, databases, shared files — since those are not partitioned for you. |
| `phases[].plans` | []string | yes | Ordered list of plan IDs to run in this phase. Must reference IDs present in `plans`. |
| `plans` | array | yes | One entry per atomic work unit. |
| `plans[].id` | string | yes | Slug: `^[a-z0-9][a-z0-9-]*$`. Must be unique within the envelope. |
| `plans[].title` | string | yes | Short display title. |
| `plans[].description` | string | no | One-paragraph task summary passed to the agent prompt. |
| `plans[].context_md` | string | no | Plan-specific context injected into the agent prompt. Project-wide guidance (root `AGENTS.md` / `CLAUDE.md` / `GEMINI.md`) is auto-loaded by the runner — do **not** duplicate it here. Max 256 KB (hard error). Warn if > 32 KB. |
| `plans[].review` | boolean | no | Per-plan pre-merge review toggle. Omit to inherit the project default (`[review].enabled` in `springfield.local.toml`). `true` forces review on for this plan even when globally disabled; `false` suppresses it even when globally enabled. See [Per-plan review toggle](#per-plan-review-toggle). |
| `plans[].verify` | object | no | Per-plan `{command, enabled}` override for the verify completion gate. Omit to inherit the global `[verify]` block in `springfield.toml`. A non-empty `command` replaces the global command; `enabled` (`true`/`false`) forces the gate on/off for this plan in either direction. See [Per-plan verify override](#per-plan-verify-override). |
| `plans[].user_stories` | array | yes | Ordered list of stories. At least one required (hard error if empty). |
| `user_stories[].id` | string | yes | Story identifier matching `^US-\d{3,}$` (hard error otherwise — runtime marker scanner only matches this shape). |
| `user_stories[].title` | string | yes | One-line story title. |
| `user_stories[].description` | string | no | Narrative description of the story. |
| `user_stories[].acceptance_criteria` | []string | yes | List of verifiable conditions. At least one required (hard error if empty); each is also checked for a verifiable signal (warning only). Prompt input, not a done-gate — see [Acceptance criteria semantics](#acceptance-criteria-semantics). |
| `user_stories[].priority` | int | no | Execution hint; lower = higher priority. |
| `user_stories[].passes` | bool | no | Set to `false` in the envelope. Springfield updates this field as markers arrive. |
| `user_stories[].deps` | []string | no | Story IDs (within the same plan) that must pass before this story is considered. Cross-plan deps are an error. |

## Per-Plan prd.json

Springfield persists the plan inner object (minus `context_md`) at:

```
.springfield/plans/<plan-id>/prd.json
```

This file is the runner's working copy of story state. Springfield is the sole writer — agents must not modify it. The file evolves as story-pass markers are detected:

```json
{
  "id": "01-auth",
  "title": "Auth scaffold",
  "description": "Bootstrap the auth package and wire one login endpoint.",
  "user_stories": [
    {
      "id": "US-001",
      "title": "Scaffold auth package",
      "description": "Create src/auth/ with package.json + initial types",
      "acceptance_criteria": ["src/auth/package.json present", "bun install exits 0"],
      "priority": 1,
      "passes": true,
      "deps": []
    },
    {
      "id": "US-002",
      "title": "Wire login endpoint",
      "description": "Add POST /login that issues a JWT",
      "acceptance_criteria": ["POST /login returns 200 with valid creds", "GET /verify returns 200 for the issued JWT"],
      "priority": 2,
      "passes": false,
      "deps": ["US-001"]
    }
  ]
}
```

The optional `context.md` file (sibling of `prd.json`) holds the raw `context_md` string for prompt injection. It is not part of `prd.json` to avoid duplicating potentially large blobs in the state file.

### Per-plan review toggle

The optional `plans[].review` field controls whether Springfield runs pre-merge review for that plan. It is tri-state at the schema level (the key may be omitted) even though the JSON type is `boolean`:

| Envelope value | Resolved behavior |
|----------------|-------------------|
| key omitted | Inherit the global default: `[review].enabled` in `springfield.local.toml` (defaults to `false`). |
| `true` | Force review on for this plan, even when the project-global default is disabled. |
| `false` | Suppress review for this plan, even when the project-global default is enabled. |

Per-plan values always win over the global default in both directions. The global review configuration lives in `springfield.local.toml` (git-ignored, per-operator) beside `springfield.toml`; a missing local file is equivalent to `[review] enabled = false`, so review is opt-in by default.

When `omitempty` strips the field from `prd.json`, the runner re-reads it as the inherit case. Set `false` explicitly to lock review off for a plan regardless of how a teammate has the global flag set.

### Per-plan verify override

The verify gate requires a command (e.g. `go test ./...`) to exit 0 before a plan is honored complete. On a non-zero exit the runner drives a capped implementer fix loop, escalating to `needs-human` if the budget is exhausted. It runs **before** the review gate, so a reviewer still sees the diff (and can catch a gutted or `.skip`-ed test suite). The gate is opt-in: a project with no `[verify]` command configured keeps its prior marker-only completion behavior unchanged.

**Global block (`springfield.toml`).** Unlike `[review]` (which lives in the git-ignored `springfield.local.toml` because it may reference personal skills), the verify command is team-shareable and belongs in the committed config:

```toml
[verify]
enabled = true                 # opt-in master switch; default false
command = "go test ./..."      # run via `sh -c` in the worktree root; must exit 0
timeout = "20m"                # per-round wall-clock ceiling (Go duration); default 20m
max_verify_iterations = 3      # fix-loop budget before needs-human; default 3
```

A missing `[verify]` block, or `enabled = true` with no `command`, leaves the gate inert.

**Per-plan override (`plans[].verify`).** The envelope carries a `{command, enabled}` object; each field resolves independently and per-plan wins over the global block:

| Field | Resolved behavior |
|-------|-------------------|
| `command` omitted / empty | Inherit the global `[verify].command`. |
| `command` non-empty | Replace the global command for this plan only. |
| `enabled` omitted | Inherit the global `[verify].enabled`. |
| `enabled: true` | Force the gate on for this plan, even when globally disabled. |
| `enabled: false` | Force the gate off for this plan, even when globally enabled — the escape hatch for a plan whose work legitimately can't pass the shared command yet. |

`timeout` and `max_verify_iterations` are not per-plan overridable — they always resolve from the global block. When `omitempty` strips the `verify` object from `prd.json`, the runner re-reads it as the full inherit case.

### Worktree setup and teardown

Slice worktrees are created bare — a fresh checkout with no installed dependencies, no untracked files (`.env`, local config), and no generated artifacts. The optional `[setup]` command closes that gap: it runs once in each freshly created slice worktree **after checkout and before any agent dispatch**, so agents don't burn turns on `npm install` or fail on a missing `.env`.

**Global block (`springfield.toml`).** Like `[verify]` (and unlike `[review]`, which lives in the git-ignored `springfield.local.toml` because it may reference personal skills), a setup command such as `npm install` is team-shareable and belongs in the committed config — every teammate's slice worktree needs the same preparation:

```toml
[setup]
enabled = true                      # opt-in master switch; default false
command = "npm ci && cp .env.example .env"  # run via `sh -c` in the slice worktree root
teardown = "docker compose down"    # optional; run at slice cleanup (see below)
timeout = "20m"                     # wall-clock ceiling for command AND teardown (Go duration); default 20m
```

A missing `[setup]` block, or `enabled = true` with no `command`, leaves setup inert — a project with no setup keeps its prior create-and-dispatch behavior byte-identical.

**Environment.** The setup command (like the agent run and `[verify]`) receives the [per-slice port block](#per-slice-port-blocks) variables `SPRINGFIELD_PORT` / `SPRINGFIELD_PORT_RANGE`, plus two path variables:

| Variable | Value |
|----------|-------|
| `SPRINGFIELD_SOURCE_ROOT` | The main checkout — copy untracked source files (`.env`, local config) from here. |
| `SPRINGFIELD_WORKTREE` | The slice worktree the command runs in (also its working directory). |

**Evidence.** Every setup run persists to `<evidence>/setup/`: `setup.json` (command, cwd, exit code, duration, timed_out) plus tail-truncated `stdout.txt` / `stderr.txt`. A setup command that exits non-zero — or is killed at `timeout` — fails the slice with reason `setup-failed` **before any agent runs**, so a broken environment surfaces immediately instead of as a confusing mid-run agent failure.

**Teardown (`[setup] teardown`).** An optional counterpart run at slice cleanup — in the same worktree root, with the same environment, immediately before the execution worktree is removed. Use it to release resources that live *outside* the worktree and so survive git-tracked removal: a database container, a docker-compose stack, a bound port that `command` spun up. It is strictly best-effort — a failing teardown is logged and never blocks cleanup or changes the plan outcome — and is independent of `command` (a project may configure `teardown` alone). It shares the `enabled` toggle and `timeout`. Empty (the common case) skips teardown entirely.

### Per-slice port blocks

Every slice is assigned a deterministic, collision-free block of 10 ports so parallel slices can run servers and tests without colliding. The agent run **and** the `[setup]` / `[verify]` commands all receive two environment variables:

| Variable | Value |
|----------|-------|
| `SPRINGFIELD_PORT` | The first port of the slice's block — the obvious "bind here" default. |
| `SPRINGFIELD_PORT_RANGE` | The full `start-end` span (e.g. `42010-42019`) for a slice that needs several ports. |

The block is a pure function of the plan's 1-based ordinal (`plan_units[].order`) and a configurable base: slice *N* owns `[base+(N-1)*10 … base+(N-1)*10+9]`. Because it depends only on the ordinal, two concurrently running slices always get disjoint blocks, and a single slice's block is identical across every iteration of a run.

**Global block (`springfield.toml`).** Like `[verify]` and `[setup]`, the port scheme is team-shareable and belongs in the committed config:

```toml
[ports]
base = 42000   # first port of slice ordinal 1's block; default 42000
```

A missing `[ports]` block selects the default base — every slice still gets a `SPRINGFIELD_PORT` / `SPRINGFIELD_PORT_RANGE` assignment.

**Out of scope — deterministic assignment, not liveness probing.** Springfield never opens a socket to check whether a port in the assigned block is already free. A port in a slice's block that an unrelated process on the host already occupies is the operator's concern: raise `[ports] base` off the busy range. Probing would trade determinism — the property that makes blocks reproducible and merge-neutral — for a race against every other process on the machine.

### `context_md` scope

Each agent prompt is assembled as: header + per-plan `context.md` (verbatim, if present) + project-wide guidance (concatenated `AGENTS.md` / `CLAUDE.md` / `GEMINI.md` from the project root) + footer. Both layers are sent on every iteration.

Use `context_md` for plan-specific guidance only — the package being built, test patterns local to that subsystem, files the agent should read first. Project-wide conventions (build/lint commands, top-level architecture, repo-wide testing rules) belong in root `AGENTS.md` and are auto-loaded; duplicating them into `context_md` doubles the prompt-token cost of that material on every iteration.

### Acceptance criteria semantics

Acceptance criteria are **prompt input, not a deterministic done-gate.** Be clear about this when authoring plans:

- Criteria are injected into the executor prompt and (when enabled) the reviewer prompt. They sharpen what the agent builds and what the reviewer checks.
- What actually gates completion is the agent self-emitting `<story-pass>US-NNN</story-pass>` (see [Marker Contract](#marker-contract)). The runner trusts that marker and re-runs the plan until every story emits it or the iteration cap (default 50) is hit. Nothing mechanically verifies the criteria held.
- The only **independent** check that the work meets its criteria is the optional pre-merge review (`plans[].review` / `[review].enabled`), which feeds the criteria to a separate reviewer agent. It runs as a post-step, not as a per-story gate.

Practical consequence: vague criteria don't fail a build — they just produce weaker prompts and a softer review. The ingest warning above nudges toward checkable phrasing, but enforcement is the marker plus (optionally) review, not the criteria text itself.

## Validation Rules

Springfield validates the envelope at ingest (`springfield plan --prd`). Hard errors abort immediately; warnings are logged but do not abort.

### Hard Errors

- `title` or `source` missing or empty.
- `plans` array absent or empty.
- `phases` array absent or empty.
- A phase references a plan ID not present in `plans`.
- A plan in `plans` is not referenced by any phase (orphan plans).
- Duplicate plan IDs within the envelope.
- Plan ID does not match `^[a-z0-9][a-z0-9-]*$`.
- A plan has an empty or missing `user_stories` list.
- A user story has an empty or missing `acceptance_criteria` list.
- An `acceptance_criteria` element is blank or whitespace-only (e.g. `[""]` or `["   "]`) — an element with no content is equivalent to having no criterion. (Distinct from the warning below, which fires on a non-empty but hard-to-verify criterion.)
- A user story ID does not match `^US-\d{3,}$` (runtime marker scanner only matches this shape).
- A story `deps` entry references a story ID in another plan (cross-plan story deps are not supported).
- `context_md` exceeds 256 KB.

### Warnings

- `context_md` exceeds 32 KB — will be injected but may crowd the agent context window.
- A non-empty `acceptance_criteria` entry has no verifiable signal — no command/outcome keyword (`test`, `passes`, `returns`, `exists`, …), HTTP verb, file path or extension, number, or code token. The criterion still compiles; the warning is a nudge to phrase it as something checkable (e.g. `go test ./auth passes`, `GET /health returns 200`, `src/auth/package.json present`). See [Acceptance criteria semantics](#acceptance-criteria-semantics) for why this is advisory, not a gate.

## Marker Contract

Agents signal story and plan completion via output markers scanned by the Springfield runner. Markers are exact string matches: case-sensitive, whitespace-sensitive — no surrounding whitespace inside the tag.

### Per-story completion

```
<story-pass>US-NNN</story-pass>
```

`US-NNN` is a placeholder — substitute the actual story ID assigned to the current iteration (e.g. the id Springfield names in the iteration prompt). Emit one marker for that story, only when its acceptance criteria are verifiably met. Springfield sets `passes: true` on the matching story in `prd.json`.

**Off-target markers are ignored.** The runner only marks the story it assigned to the current iteration. If the agent emits a marker for any story ID *other than* the current iteration target, the marker is logged as a warning to `progress.md` and discarded — the named story is not marked passed. This prevents a misbehaving agent from skipping future stories.

### Plan completion

```
<promise>COMPLETE</promise>
```

Emit exactly once when all stories in the plan are done. This is the signal that terminates the iteration loop for the plan. Emitting before all stories pass is a bug in the agent prompt — the runner will log a warning to `progress.md` and continue iterating.

## Stop Conditions

The iteration loop terminates on:

| Condition | Outcome | Notes |
|-----------|---------|-------|
| All stories `passes: true` | Plan marked `completed`; merge integration follows. | Runner re-checks via `NextStory` after each iteration. |
| Iteration cap reached | Plan marked `failed`; `exit_reason = "iteration cap reached without completion marker"`. | Cap from `single_workstream_iterations` in `springfield.toml`; default 50. |
| Story dependency graph blocked | Plan marked `failed`; `exit_reason = "story dependency graph blocked: no eligible story"`. | Cycles like `US-001 → US-002 → US-001` detected before agent dispatch. |
| `MarkPassed` write error | Plan marked `failed`; iteration aborted. | Atomic temp+rename — original `prd.json` intact on failure. |
| Tamper detected on `.springfield/` | Plan marked `failed`; control-plane files restored from snapshot. | Agent must not write to `.springfield/`. |
| `SIGINT` / `SIGTERM` | Batch left intact; `springfield start` resumes from current cursor on next invocation. | `run.json` and per-plan state preserved; batch is NOT archived. |

## Operator Workflows

### `--prd` (skill-driven, canonical path)

```bash
springfield plan --prd - < envelope.json
```

Reads envelope from stdin (or a file path). Validates the envelope, writes per-plan PRD dirs under `.springfield/plans/<plan-id>/`, and registers each plan in the conductor config.

Legacy `--slices` shape is rejected with an explicit error pointing to this document.

### `--from-dir` (operator escape hatch)

```bash
springfield plan --from-dir <path>
```

Reads `<path>/batch.json` (envelope shape). Same write path as `--prd`. Mutually exclusive with `--prd`.

### `--replace` (recompile active batch)

```bash
springfield plan --replace --prd - < envelope.json
```

Compiles the new envelope first; only after validation succeeds does it archive the prior batch and clear `run.json`. A malformed envelope leaves the prior batch unchanged.

When a plan ID is reused across `--replace`, the per-plan directory is wiped and recreated — old `context.md` and `progress.md` from the prior batch never leak into the new run.

`--replace` is refused while any plan in the active batch is `running`.

### `--append` (extend active batch)

```bash
springfield plan --append --prd - < envelope.json
```

Adds new envelope plans to the active batch. Plan ID collisions are rejected. Original `source.md` is preserved (only the appended envelope's plans are written; the batch's audit source remains the original).

`--append` is refused while any plan in the active batch is `running`.

### Legacy batch shape

Pre-PRD batches with `phases[].slices` (instead of `phases[].plans`) are rejected at load time with an explicit error. Run `rm -rf .springfield/plans/<batch-id>/` and recompile via `--prd` or `--from-dir`.

## Operator Override of Prompt Templates

Springfield embeds default prompt header and footer templates. Operators can override them per project:

```
<projectRoot>/springfield/prompts/header.tmpl
<projectRoot>/springfield/prompts/footer.tmpl
```

- Missing override file → silent fallback to the embedded default.
- Present override file with a parse error → loud fail (runner aborts before spawning the agent).

Templates receive a `Plan` value with `.Title`, `.Description`, `.ContextMD`, and `.UserStories` for interpolation. See `internal/runner/prompts/` for the embedded defaults.

## Runner as Sole Writer

The Springfield runner is the sole writer of:

- `.springfield/plans/<plan-id>/prd.json`
- `.springfield/plans/<plan-id>/progress.md`
- `.springfield/plans/<plan-id>/context.md`
- `.springfield/run.json`

Agents must not write to `.springfield/`. Writing to `.springfield/` from within an agent run will trigger the tamper-detection guard, restore the snapshot, and abort the current run with `exit_reason = "tamper-detected: <details>"`. The guard covers per-plan directories AND `run.json`.
