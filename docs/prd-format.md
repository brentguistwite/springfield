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
| `plans[].context_md` | string | no | Freeform context injected into the agent prompt header. Max 256 KB (hard error). Warn if > 32 KB. |
| `plans[].user_stories` | array | no | Ordered list of stories. Omitting yields a plan with no story tracking. |
| `user_stories[].id` | string | yes | Story identifier, e.g. `US-001`. Convention: `^US-\d{3,}$` (warning if violated). |
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

## Validation Rules

Springfield validates the envelope at ingest (`springfield plan --prd`). Hard errors abort immediately; warnings are logged but do not abort.

### Hard Errors

- `title` or `source` missing or empty.
- `plans` array absent or empty.
- `phases` array absent or empty.
- A phase references a plan ID not present in `plans`.
- Duplicate plan IDs within the envelope.
- Plan ID does not match `^[a-z0-9][a-z0-9-]*$`.
- A user story has an empty or missing `acceptance_criteria` list.
- A story `deps` entry references a plan ID in another plan (cross-plan story deps are not supported).
- `context_md` exceeds 256 KB.

### Warnings

- Story ID does not match `^US-\d{3,}$` — story tracking still works, but `springfield status` rollup may not display cleanly.
- `context_md` exceeds 32 KB — will be injected but may crowd the agent context window.

## Marker Contract

Agents signal story and plan completion via output markers scanned by the Springfield runner. Markers are exact string matches: case-sensitive, whitespace-sensitive — no surrounding whitespace inside the tag.

### Per-story completion

```
<story-pass>US-001</story-pass>
```

Emit one marker per completed story, in any order. Emit only when the story's acceptance criteria are verifiably met. Springfield sets `passes: true` on the matching story in `prd.json`.

### Plan completion

```
<promise>COMPLETE</promise>
```

Emit exactly once when all stories in the plan are done. This is the signal that terminates the iteration loop for the plan. Emitting before all stories pass is a bug in the agent prompt — the runner will log a warning and continue iterating.

### Iteration cap

If the runner reaches the configured iteration cap before `<promise>COMPLETE</promise>` is seen, the plan is marked `failed`. The cap is set in `springfield.toml` (default: 5 iterations per plan).

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

Agents must not write to `.springfield/`. Writing to `.springfield/` from within an agent run will trigger the tamper-detection guard and abort the current run.
