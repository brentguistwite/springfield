---
description: Use Springfield recover to diagnose stuck Springfield work and restore a safe next step.
---

# Springfield Recover

Use Springfield recover to diagnose stuck Springfield work and restore a safe next step.

# Springfield Playbook
Source: builtin/springfield.md

# Springfield

Built-in Springfield playbook.

- Keep Springfield as the only user-facing surface.
- Use the shared Springfield playbook guidance to shape planning and explanation.
- Keep internal engine details out of Springfield-owned prompt surfaces.

# Current Task

Recover Springfield work that is stalled, failed, or missing expected state.

Read project guidance from AGENTS.md first, then CLAUDE.md, then GEMINI.md when present.
Identify the break in state, explain what is recoverable, and drive toward the safest concrete next step to resume progress.
Prefer recovery and continuation over starting a fresh plan unless the existing state cannot be salvaged.
Keep Springfield as the only user-facing surface.

## Invocation Input

User input from the slash command invocation:

$ARGUMENTS

If `$ARGUMENTS` is empty, continue with the default Springfield behavior for this command.
