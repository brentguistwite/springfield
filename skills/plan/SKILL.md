---
name: plan
description: Use Springfield plan to compile a new work request into a runnable batch for the current project.
---

# Springfield Plan

Use Springfield plan to compile a new work request into a runnable batch for the current project.

# Springfield Playbook
Source: builtin/springfield.md

# Springfield

Built-in Springfield playbook.

- Keep Springfield as the only user-facing surface.
- Use the shared Springfield playbook guidance to shape planning and explanation.
- Keep internal engine details out of Springfield-owned prompt surfaces.

# Current Task

## Before you start — verify the Springfield CLI

Run `springfield version` first. It prints one of:
- `springfield vX.Y.Z` — a released build.
- `springfield dev` — a local source build (e.g. `go install .`).

Then:

- If the command is **not found**, the CLI is not installed. Tell the user to install it, then stop:
  - macOS: `brew install brentguistwite/tap/springfield`
  - Linux: download the matching `springfield_<version>_linux_<arch>.tar.gz` from the GitHub Releases page and put the `springfield` binary on PATH.
  - Windows: build from source with `go install .` inside the Springfield repo (no Windows release tarballs are published yet).
- If the output is `springfield dev`, this is a local development build. Continue without a floor check — the user is responsible for keeping it current.
- If the reported version is **older than v0.11.0** (compare semver-style after stripping the leading `v` from the reported version), tell the user to upgrade, then stop:
  - macOS: `brew upgrade springfield`
  - Linux: download the latest `springfield_<version>_linux_<arch>.tar.gz` from the GitHub Releases page and replace the binary on PATH.
  - Windows: `go install .` inside the Springfield repo (no Windows release tarballs yet).
- Otherwise continue.

Do not try to work around a missing or too-old CLI — surface the exact command above instead. (A plugin older than the CLI is fine and needs no action; the CLI stays backward-compatible with older skills within a major version.)

## Springfield control plane

**Reads are allowed** — recover and status flows specifically inspect `.springfield/run.json` and per-plan `prd.json`. **Never write, edit, or delete** files under `.springfield/`. That directory is Springfield's state — the CLI is your only interface for mutating it. Writing there directly will abort the current batch. This applies regardless of which agent is invoking the skill.

---

Compile a Springfield batch from the user's work request.

Read project guidance from AGENTS.md first, then CLAUDE.md, then GEMINI.md when present.

## Step 1 — Determine source

Ask the user whether they have an existing plan file or want to describe the work directly:

1. **Existing plan file**: ask for the file path, then read it.
2. **Fresh prompt**: ask the user to describe what they want to build.

Do not infer file-vs-prompt from one ambiguous input.

## Step 2 — Check for active batch

Run `springfield status` to check whether an active batch already exists.

- If an active batch exists and any slice is `running`, tell the user to wait before replacing.
- If an active batch exists but nothing is running, ask the user: replace it, append to it, or keep it.

## Step 3 — Read and slice the plan

If the user pointed to a file, read it. If prompt-mode, treat the prompt as source.

Decide slice boundaries based on the plan's meaning, not syntax. A slice should:

- Be independently deliverable in one agent run.
- Map to one coherent outcome (e.g., "scaffold auth package", "wire login endpoint").
- Not span unrelated subsystems.

Markdown cues to consider (in priority order):

1. Explicit `## Task N:` / `## Step N:` headers — honor them.
2. H2/H3 sections that each describe a discrete deliverable.
3. Numbered lists of implementation steps.
4. Prose plans — chunk by responsibility.

If the plan is genuinely one step, emit one slice. Don't pad.

## Step 4 — Definition of Done (per slice)

For each slice, settle how we will know it is done — its `acceptance_criteria`.

- **Criteria already present** (the source plan already lists them): show them back read-only and ask the user "edit any?". Do not silently re-draft what they wrote.
- **Thin or missing**: before drafting anything, ask one focused question per slice — "how do we know this is done — what command or observable proves it?" Then draft concrete checks and show them in one compact block for confirmation.

Aim each criterion at something checkable: a test command (e.g. `go test ./auth passes`), a file path, or an HTTP response (e.g. `GET /health returns 200`). Springfield emits a non-fatal warning at ingest on any criterion with no such signal — vague criteria still compile, they just get a nudge.

How criteria are actually used — be honest with the user, don't oversell:

- They sharpen the agent and reviewer prompts. They are NOT a deterministic done-gate.
- The runner re-runs a plan until the agent self-emits `<story-pass>US-NNN</story-pass>` or it hits the iteration cap (default 50). That marker — the agent's own judgment — is what gates completion, not the criteria themselves.
- The optional pre-merge review (next step) is the only independent check that the work actually meets the criteria.

## Step 5 — Offer pre-merge review

Ask, as its own question: **"Enable independent pre-merge review for this batch?"**

- The default lever is per-plan: set `plans[].review` in the envelope to `true` (force review on) or `false` (force it off). Leave it unset to inherit the project default.
- The project-wide default lives in `springfield.local.toml` (`[review].enabled`, an operator-wide concern) — mention it only if the user wants every batch reviewed rather than choosing per-plan.

**Serialize the answer.** Write the **same** `review` value onto *every* plan object — all `true` if the user enabled review, all `false` if they declined (the example below shows `true` on each plan). Only use different values per plan if the user explicitly asks for per-plan control. Omitting `review` means "inherit the project default", so dropping it after the user opted in would silently ship an unreviewed batch.

## Step 6 — Confirm and persist

Show the user the proposed plans (title + one-line intent per plan).
Ask for confirmation before writing.

Once confirmed, compile a **PRD envelope** and pipe it to `springfield plan --prd -`:

```bash
springfield plan --prd - <<'JSON'
{
  "title": "<batch title>",
  "source": "<original plan text, verbatim>",
  "phases": [
    {"mode": "serial", "plans": ["plan-01", "plan-02"]}
  ],
  "plans": [
    {
      "id": "plan-01",
      "title": "<plan 1 title>",
      "description": "<plan 1 description>",
      "context_md": "Project uses TypeScript + Bun. Follow existing test patterns.",
      "review": true,
      "user_stories": [
        {
          "id": "US-001",
          "title": "<story title>",
          "description": "<story description>",
          "acceptance_criteria": ["<criterion 1>"],
          "priority": 1,
          "passes": false,
          "deps": []
        }
      ]
    },
    {
      "id": "plan-02",
      "title": "<plan 2 title>",
      "description": "<plan 2 description>",
      "context_md": "Project uses TypeScript + Bun. Follow existing test patterns.",
      "review": true,
      "user_stories": [
        {
          "id": "US-001",
          "title": "<story title>",
          "description": "<story description>",
          "acceptance_criteria": ["<criterion 1>"],
          "priority": 1,
          "passes": false,
          "deps": []
        }
      ]
    }
  ]
}
JSON
```

Schema notes:
- `phases`: execution ordering. Each phase has `mode` (`"serial"` or `"parallel"`) and `plans` (list of plan IDs in that phase).
- `plans`: each plan has `id`, `title`, `description`, optional `context_md`, optional `review` (the per-plan pre-merge review toggle from Step 5 — omit to inherit the project default), and `user_stories`.
- `context_md`: plan-specific context only. Project-wide guidance (build commands, repo conventions) lives in root `AGENTS.md` and is auto-loaded by the runner — do not duplicate it into `context_md` or you double the prompt-token cost of that material every iteration.
- `user_stories`: each story has `id` (`US-NNN`), `title`, `description`, `acceptance_criteria`, `priority` (int, lower = runs first), `passes` (false initially), `deps` (story IDs within same plan).
- See `docs/prd-format.md` for full field semantics, validation rules, and stop conditions.

> **Constraint — documentation acceptance criteria must name an explicit target file.**
> Any `acceptance_criteria` entry that prescribes writing or updating documentation MUST name the exact target as a `path/to/file.md` (add a `:line` anchor when pointing at an existing section). If no canonical file exists yet, the criterion must say to create it at a named path.
> Rationale: in a dogfood batch an agent burned ~75 turns hunting for a "review docs" file that never existed because the criterion never named one — vague targets cause thrash.
> - Allowed: `"Document the off-target marker rule in docs/prd-format.md under the 'Stop Conditions' section"`
> - Forbidden: `"Document the off-target marker rule in the review docs"` and `"note this in the relevant section"` — no file path, so the agent has nothing concrete to target.

Use `--replace` or `--append` if an active batch exists (per Step 2).

Keep Springfield as the only user-facing surface.
