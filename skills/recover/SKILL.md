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

## Before you start — verify the Springfield CLI

Run `springfield version` first. It prints one of:
- `springfield vX.Y.Z` — a released build.
- `springfield dev` — a local source build (e.g. `go install .`).

Then:

- If the command is **not found**, the CLI is not installed. Tell the user to install it, then stop:
  - macOS: `brew install brentguistwite/tap/springfield`
  - Linux/Windows: download the matching `springfield_<version>_<os>_<arch>.tar.gz` from the GitHub Releases page and put the `springfield` binary on PATH.
- If the output is `springfield dev`, this is a local development build. Continue without a floor check — the user is responsible for keeping it current.
- If the reported version is **older than v0.11.0** (compare semver-style after stripping the leading `v` from the reported version), tell the user to upgrade, then stop:
  - macOS: `brew upgrade springfield`
  - Linux/Windows: download the latest release tarball and replace the binary on PATH.
- Otherwise continue.

Do not try to work around a missing or too-old CLI — surface the exact command above instead. (A plugin older than the CLI is fine and needs no action; the CLI stays backward-compatible with older skills within a major version.)

## Springfield control plane

Never read, write, edit, or delete files under `.springfield/`. That directory is Springfield's internal state — the CLI is your interface for changing it. Writing there directly will abort the current batch. This applies regardless of which agent is invoking the skill.

Recover a Springfield batch that is stalled, blocked, or has a failed slice.

Read project guidance from AGENTS.md first, then CLAUDE.md, then GEMINI.md when present.

## Step 1 — Read current state

Run `springfield status` to see the active batch, current phase, and slice statuses.

For a failing or stalled plan, run `springfield recover --diagnose --plan <plan-id>` first. The CLI surfaces dirty-worktree state, `exit_reason`, orphan-plan handling, and the available recovery actions — interpret that output rather than re-deriving it from raw files.

Also read `.springfield/run.json` for the last checkpoint and last known error.

## Step 2 — Diagnose

Identify which plan and which story failed or stalled. Check:
- The CLI diagnose output (above) — primary source.
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
