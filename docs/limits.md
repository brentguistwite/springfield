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
the iteration did not emit `<promise>COMPLETE</promise>`.

`COMPLETE` always wins: an iteration that finished its work is never capped,
however many turns it took — the cap exists to catch *unproductive* spinning,
not to punish a long-but-successful run.

> **Why monitoring and not a CLI flag?** The Claude Code CLI we target exposes
> no `--max-turns` option (verified via `claude --help`; it offers only
> `--max-budget-usd`). Passing an unsupported flag would abort every run, so the
> cap is enforced from the stream-json output instead. Agents that don't report
> `num_turns` (Codex, Gemini) are never tripped by this monitor. If a future CLI
> adds `--max-turns`, the flag can be wired as a complementary first line of
> defense without changing this monitor.
