---
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

---

Inspect the current Springfield batch for the project and report the current state.

Read project guidance from AGENTS.md first, then CLAUDE.md, then GEMINI.md when present.

Run `springfield status` to get the current Springfield batch state, then summarize:
- The active batch id and title
- The current phase
- Which slices are done, running, blocked, or queued
- Per-plan story progress: e.g. "Plan 03: 5/8 stories pass; failed at US-006"
- The last known error if any
- The clearest next action for the user

Do not invent new work unless the user explicitly asks to re-plan it.
Keep Springfield as the only user-facing surface.

## Invocation Input

User input from the slash command invocation:

$ARGUMENTS

If `$ARGUMENTS` is empty, continue with the default Springfield behavior for this command.
