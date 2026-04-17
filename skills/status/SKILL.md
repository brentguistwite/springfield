---
name: status
description: Use Springfield status to inspect the active batch and explain where it stands.
---

# Springfield Status

Use Springfield status to inspect the active batch and explain where it stands.

# Springfield Playbook
Source: builtin/springfield.md

# Springfield

Built-in Springfield playbook.

- Keep Springfield as the only user-facing surface.
- Use the shared Springfield playbook guidance to shape planning and explanation.
- Keep internal engine details out of Springfield-owned prompt surfaces.

# Current Task

Inspect the current Springfield batch for the project and report the current state.

Read project guidance from AGENTS.md first, then CLAUDE.md, then GEMINI.md when present.

Run `springfield status` to get the current Springfield batch state, then summarize:
- The active batch id and title
- The current phase
- Which slices are done, running, blocked, or queued
- The last known error if any
- The clearest next action for the user

Do not invent new work unless the user explicitly asks to re-plan it.
Keep Springfield as the only user-facing surface.
