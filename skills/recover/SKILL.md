---
name: recover
description: Use Springfield recover to diagnose a stuck batch or failed slice and restore a safe next step.
---

# Springfield Recover

Use Springfield recover to diagnose a stuck batch or failed slice and restore a safe next step.

# Springfield Playbook
Source: builtin/springfield.md

# Springfield

Built-in Springfield playbook.

- Keep Springfield as the only user-facing surface.
- Use the shared Springfield playbook guidance to shape planning and explanation.
- Keep internal engine details out of Springfield-owned prompt surfaces.

# Current Task

Recover a Springfield batch that is stalled, blocked, or has a failed slice.

Read project guidance from AGENTS.md first, then CLAUDE.md, then GEMINI.md when present.

## Step 1 — Read current state

Run `springfield status` to see the active batch, current phase, and slice statuses.

Also read `.springfield/run.json` for the last checkpoint and last known error.

## Step 2 — Diagnose

Identify which plan and which story failed or stalled. Check:
- The last error in `run.json`
- Per-story `passes` state in each plan's `prd.json` (`.springfield/plans/<plan-id>/prd.json`)
- Any blockers mentioned in the batch source

## Step 3 — Recover

Propose the safest concrete next step:
- For a failed plan: fix the underlying issue, then run `springfield start` to resume from cursor. Already-passed stories (`passes: true`) are skipped on re-entry — `prd.json` is preserved across retries.
- For a blocked plan: explain what needs to happen before execution can continue.
- For a corrupt batch: use `springfield plan --replace` to start fresh with a new batch.

Story-level retry is not supported in v1; recovery operates at plan grain (retry plan or retry merge).

Prefer recovery and continuation over starting a fresh plan unless the existing state cannot be salvaged.
Keep Springfield as the only user-facing surface.
