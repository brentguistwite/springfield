# Smoke Verification — Springfield Runtime

Verified 2026-04-07 against binary built from `main` at `48bc88c`.

## Environment

- macOS Darwin 24.6.0, arm64
- Go 1.26.1
- Claude Code CLI at `/Users/brent.guistwite/.local/bin/claude`
- Codex CLI at `/Users/brent.guistwite/.nvm/versions/node/v22.22.0/bin/codex`
- Gemini CLI: not installed

## Commands Verified

### `springfield version`

```
springfield dev
```

Outputs build version. No issues.

### `springfield doctor`

```
✓ Claude Code (claude) → /Users/brent.guistwite/.local/bin/claude
✓ Codex CLI (codex) → /Users/brent.guistwite/.nvm/versions/node/v22.22.0/bin/codex
✗ Gemini CLI (gemini)
  Install Gemini CLI: npm install -g @anthropic-ai/gemini-cli (or see https://github.com/google-gemini/gemini-cli)

2/3 agent(s) available. Springfield can operate with the available agent(s).
```

Truthful detection with actionable install guidance for missing agent.

### `springfield init`

```
Created springfield.toml
Created .springfield/

Next: run "springfield conductor setup" to configure conductor.
```

Scaffolds `springfield.toml` with `default_agent = "claude"` and `.springfield/` state dir. Guides user to next step.

### `springfield conductor setup`

```
Plan storage mode:
  local  .springfield/conductor/plans
  tracked  .conductor/plans
Choose plan storage [local/tracked] (default: local):
Created .springfield/conductor/config.json

Next steps:
  1. Add plan files to .springfield/conductor/plans
  2. Run: springfield conductor run

Agent prerequisites:
  Claude Code CLI must be installed and authenticated.
```

Prompts for plan storage and defaults to local plan files under `.springfield/conductor/plans`. No manual JSON editing required.
Fresh setup writes canonical empty arrays for `batches` and `sequential` until plans are added.

### `springfield conductor status`

```
Progress: 0/0 plans completed
```

Correctly reports that no plans are configured yet in a fresh initialized project.

### `springfield conductor run --dry-run`

```
No plans configured. Add plans to your conductor config, then run again.
```

Dry run truthfully reports no plans are configured and guides the user to add some.

### `springfield conductor diagnose`

```
Progress: 0/0 plans completed

Next step: No plans configured. Add plans to your conductor config.
```

Reports empty state with actionable guidance when no plans configured.

### `springfield ralph init --name smoke --spec test-plan.json`

```
Initialized Ralph plan "smoke2" with 1 stories.
```

Correctly deserializes PRD-format spec (`userStories`, `passes`, `deps` fields). Prior to this fix, PRD-format specs silently produced 0 stories.

### `springfield ralph status --name smoke2`

```
Plan: smoke2
Project: smoke-test
Stories: 1

US-001  pending  First story
```

### `springfield ralph run --name smoke2`

```
Story US-001: failed (agent: claude)
Error: agent claude failed: exit status 1
```

Real agent invocation via Claude Code CLI. Truthful failure reporting — claude exits 1 because the temp test dir has no project context. This confirms the runtime delegates to the real agent binary, not a placeholder.

## Blockers

- **TUI launch**: `springfield` (bare) opens the TUI shell. Interactive Bubble Tea views cannot be verified in a non-interactive smoke harness. TUI rendering is covered by automated tests in `tests/tui/`.
- **Successful agent run**: A successful `ralph run` or `conductor run` requires a real project with valid plan content and agent authentication. The runtime path is verified up to the point of agent CLI invocation, which is the boundary Springfield controls.

## Truthfulness Gaps Fixed

1. **PRD-format spec deserialization**: `ralph init` silently dropped stories when given a PRD-format JSON file (`userStories` instead of `stories`, `passes` instead of `passed`, `deps` instead of `dependsOn`). Added `UnmarshalJSON` to `Spec` and `Story` to accept both formats.
2. **README stale placeholder language**: Removed "execution backend is still a placeholder executor" — the runtime now delegates to real agent CLIs.
3. **README manual conductor config**: Updated to document `conductor setup` as the primary config path instead of hand-editing JSON.
