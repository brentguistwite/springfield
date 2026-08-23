# Execution Limits

Per-run safety ceilings that stop a single agent invocation from running away.
Configured in `springfield.toml`.

## `max_turns_per_iteration`

```toml
[project]
max_turns_per_iteration = 40   # default; 0 disables the cap
```

Caps how many agent **turns** a single plan iteration may consume before
Springfield fails that iteration with the structured reason
`iteration-turn-cap-exceeded`. A turn is one assistant ↔ tool round-trip; a
runaway agent can otherwise rack up dozens in one iteration. In dogfooding, an
agent burned 84 turns on a single story before the accumulated context produced
an API 400 — this cap is the early circuit-breaker for exactly that thrash.

### Resolution

| `max_turns_per_iteration` in `[project]` | Effective cap |
| ---------------------------------------- | ------------- |
| omitted                                  | **40** (`DefaultMaxTurnsPerIteration`) |
| explicit positive `n`                    | `n`           |
| explicit `0`                             | disabled (no cap) |
| explicit negative                        | disabled (treated as off) |

Note the deliberate asymmetry: an **omitted** key defaults to 40, but an
**explicit `0`** disables the cap. A negative value is also treated as disabled
rather than rejected, so a fat-fingered `-1` degrades to "off" instead of
aborting the load.

### Enforcement mechanism

Springfield enforces the cap by monitoring the `num_turns` field of the agent's
terminal stream-json `result` event, and synthesizing the
`iteration-turn-cap-exceeded` failure when `num_turns` exceeds the cap **and**
the work for the iteration is not confirmed complete. The synthesized failure
classifies as retryable, so the over-cap agent is cooled down and the next
agent in the priority chain (typically `codex`) gets a turn at the iteration.

For PRD plans, "work complete" means BOTH `<promise>COMPLETE</promise>` was
emitted AND every user story passes (hypothetically applying the iteration's
target-story pass marker). A **premature** `COMPLETE` — emitted before all
stories pass — does NOT defuse the cap; this is the adversarial-review-caught
case where an agent thrashed for 200 turns and emitted `COMPLETE` anyway to
shortcut out. The work-complete check is the authoritative defuse signal, not
the marker alone.

### Plan-format scope

The cap is enforced on **PRD plans only** — plans whose unit path ends in
`prd.json`. Legacy `.md` plans are exempt because the legacy single-shot
dispatch path has no per-story pass oracle a completion check could consult;
forwarding the cap there would demote legitimate long-running clean exits to
"thrash" and fall through to the next agent on an already-mutated worktree.

When `max_turns_per_iteration` is set non-zero and a legacy plan dispatches,
Springfield emits a progress-line warning so operators see the asymmetry
rather than silently losing the safety ceiling:

```
plan <id>: WARNING max_turns_per_iteration=40 configured but plan is legacy
.md format; turn-cap circuit-breaker is PRD-only and will NOT apply to this
plan
```

Migrate the plan to PRD format to get cap coverage.

> **Why monitoring and not a CLI flag?** The Claude Code CLI we target exposes
> no `--max-turns` option (verified via `claude --help`; it offers only
> `--max-budget-usd`). Passing an unsupported flag would abort every run, so the
> cap is enforced from the stream-json output instead. Agents that don't report
> `num_turns` (Codex, Gemini, OpenCode) are never tripped by this monitor. If a future CLI
> adds `--max-turns`, the flag can be wired as a complementary first line of
> defense without changing this monitor.
