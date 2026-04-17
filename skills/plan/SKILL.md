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

## Step 3 — Compile slices

Parse the source into small, named implementation slices (like `01-scaffold`, `02-api`, `03-ui`).

Each slice should:
- Have a short ID (`01`, `02`, ...) and a clear title.
- Cover a coherent, independently-deliverable chunk of work.
- Default to serial execution unless the user explicitly confirms independent slices that can run in parallel.

## Step 4 — Confirm and persist

Show the user the proposed batch: ID, title, and slice list.
Ask for confirmation before writing.

Once confirmed, run:

```
springfield plan --file <path>   # for a file source
springfield plan --prompt "<text>"  # for a direct prompt
```

Keep Springfield as the only user-facing surface.
