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
```

Field reference:

| Field | Type | Required | Notes |
|-------|------|----------|-------|
| `title` | string | yes | Human-readable batch title. |
| `source` | string | yes | Verbatim plan markdown that originated this batch. Stored as `source.md` for audit. |
| `phases` | array | yes | Execution ordering. At least one phase required. |
| `phases[].mode` | string | yes | `"serial"` (only supported value today). |
| `phases[].plans` | []string | yes | Ordered list of plan IDs to run in this phase. Must reference IDs present in `plans`. |
| `plans` | array | yes | One entry per atomic work unit. |
| `plans[].id` | string | yes | Slug: `^[a-z0-9][a-z0-9-]*$`. Must be unique within the envelope. |
| `plans[].title` | string | yes | Short display title. |
| `plans[].description` | string | no | One-paragraph task summary passed to the agent prompt. |
| `plans[].context_md` | string | no | Plan-specific context injected into the agent prompt. Project-wide guidance (root `AGENTS.md` / `CLAUDE.md` / `GEMINI.md`) is auto-loaded by the runner — do **not** duplicate it here. Max 256 KB (hard error). Warn if > 32 KB. |
| `plans[].review` | boolean | no | Per-plan pre-merge review toggle. Omit to inherit the project default (`[review].enabled` in `springfield.local.toml`). `true` forces review on for this plan even when globally disabled; `false` suppresses it even when globally enabled. See [Per-plan review toggle](#per-plan-review-toggle). |
| `plans[].user_stories` | array | yes | Ordered list of stories. At least one required (hard error if empty). |
| `user_stories[].id` | string | yes | Story identifier matching `^US-\d{3,}$` (hard error otherwise — runtime marker scanner only matches this shape). |
| `user_stories[].title` | string | yes | One-line story title. |
| `user_stories[].description` | string | no | Narrative description of the story. |
| `user_stories[].acceptance_criteria` | []string | yes | List of verifiable conditions. At least one required (hard error if empty). |
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
      "acceptance_criteria": ["src/auth/package.json present", "bun install succeeds"],
      "priority": 1,
      "passes": true,
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

### `context_md` scope

Each agent prompt is assembled as: header + per-plan `context.md` (verbatim, if present) + project-wide guidance (concatenated `AGENTS.md` / `CLAUDE.md` / `GEMINI.md` from the project root) + footer. Both layers are sent on every iteration.

Use `context_md` for plan-specific guidance only — the package being built, test patterns local to that subsystem, files the agent should read first. Project-wide conventions (build/lint commands, top-level architecture, repo-wide testing rules) belong in root `AGENTS.md` and are auto-loaded; duplicating them into `context_md` doubles the prompt-token cost of that material on every iteration.

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
- A user story ID does not match `^US-\d{3,}$` (runtime marker scanner only matches this shape).
- A story `deps` entry references a story ID in another plan (cross-plan story deps are not supported).
- `context_md` exceeds 256 KB.

### Warnings

- `context_md` exceeds 32 KB — will be injected but may crowd the agent context window.

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
