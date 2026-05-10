---
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

## Step 4 — Confirm and persist

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
- `plans`: each plan has `id`, `title`, `description`, optional `context_md` (project-specific guidance for the agent — replaces per-plan AGENTS.md), and `user_stories`.
- `user_stories`: each story has `id` (`US-NNN`), `title`, `description`, `acceptance_criteria`, `priority` (int), `passes` (false initially), `deps` (story IDs within same plan).

Use `--replace` or `--append` if an active batch exists (per Step 2).

Keep Springfield as the only user-facing surface.

## Invocation Input

User input from the slash command invocation:

$ARGUMENTS

If `$ARGUMENTS` is empty, continue with the default Springfield behavior for this command.
