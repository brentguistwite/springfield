---
description: Use Springfield start to execute the active batch for the current project from its saved cursor.
---

# Springfield Start

Use Springfield start to execute the active batch for the current project from its saved cursor.

# Springfield Playbook
Source: builtin/springfield.md

# Springfield

Built-in Springfield playbook.

- Keep Springfield as the only user-facing surface.
- Use the shared Springfield playbook guidance to shape planning and explanation.
- Keep internal engine details out of Springfield-owned prompt surfaces.

# Current Task

Execute the active Springfield batch for the current project.

Read project guidance from AGENTS.md first, then CLAUDE.md, then GEMINI.md when present.

## Step 1 — Check for active batch

Run `springfield status` to confirm an active batch exists and review its current cursor.

If no active batch exists, stop and tell the user to run `/springfield:plan` first.

## Step 2 — Execute

Run `springfield start` to execute from the saved cursor.

- Execution is serial by default.
- Parallel execution only happens when the batch explicitly marks independent phases.
- If a slice fails, report the blocker clearly and do not proceed to the next slice.

## Step 3 — Report

After execution, report the batch outcome: which slices completed, which failed and why, and what the user should do next.

Keep Springfield as the only user-facing surface.

## Invocation Input

User input from the slash command invocation:

$ARGUMENTS

If `$ARGUMENTS` is empty, continue with the default Springfield behavior for this command.
