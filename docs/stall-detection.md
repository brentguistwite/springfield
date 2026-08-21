# Stall detection

A slice runs under two independent bounds: the per-iteration **turn cap** (which
trips only after an agent burns its turn budget) and the subprocess **wall-clock
timeout** (which is deliberately long — hours — so a legitimately slow run is not
cut off). Between them sits a blind spot: an agent that goes **silent** — wedged
on a hung tool call, a stuck prompt, a deadlocked subprocess — emits no stream
events and simply waits out the wall clock, burning the whole timeout before
anyone notices.

**Stall detection** closes that gap. It watches event recency on the live output
stream: if no stream event arrives within a configurable **threshold**, the slice
is classified **possibly-wedged** and escalated for operator visibility well
before the wall clock expires.

> **Advisory only — never auto-kill.** A possibly-wedged classification
> **escalates**; it never interrupts, signals, or kills the live subprocess. The
> process still runs to its own completion or the wall-clock timeout. Springfield
> surfaces the wedge so *you* can decide — it does not act on your behalf. This
> is a hard, non-negotiable rule: there is no config key, and no code path, that
> turns a stall into a kill.

## How a wedge is classified

The detector is fed a liveness heartbeat on **every** stream event at the point
the run consumes output. Each event resets an idle timer. When the idle timer
crosses the threshold, the slice is flagged possibly-wedged **once per idle
stretch** — if the agent recovers and then goes silent again, that is a second
occurrence, not a duplicate of the first.

Detection is scoped to a single dispatch: each agent run gets its own fresh idle
timer, and a threshold of `0` disables it entirely (see below).

## What escalation does

A wedge classification surfaces on three seams, none of which touch the
subprocess:

1. **Status** — `springfield status --json` grows a `stall` block on the
   possibly-wedged plan's card (`stale_for`, `since`, `occurrences`). It is shown
   **only while the plan is running**; a terminal transition drops it, so a stale
   signal from a prior run can never leak.
2. **Evidence** — each occurrence is appended to `stalls.jsonl` in the plan's
   evidence directory (one JSON record per line: plan id, iteration, staleness,
   observed-at), so recurring wedges stay diagnosable after the run ends.
3. **Notifications** — when the [`[notify]`](notifications.md) hook is enabled, a
   `stalled` event fires through the same seam the batch terminal-state
   notifications use, naming the plan and the staleness duration. Notifications
   are off by default; an unconfigured hook stays silent. Unlike the terminal
   kinds, `stalled` fires from **inside a live run** and is **not** terminal —
   the batch keeps going.

## Configuration — the `[stall]` block

Stall detection is a project-wide operational policy (a shareable default, not a
machine-personal secret), so — like `[verify]` — its block lives in the
**committed** `springfield.toml`, not the git-ignored `springfield.local.toml`.

```toml
[stall]
threshold = "5m"    # idle ceiling before a silent slice is possibly-wedged
```

| Key | Type | Default | Meaning |
| --- | ---- | ------- | ------- |
| `threshold` | duration string | `5m` | Idle ceiling. A slice that emits no stream event within `threshold` is classified possibly-wedged (advisory; never killed). Accepts any Go duration string (`"90s"`, `"5m"`, `"1h30m"`). |

Unknown keys are rejected: a typo like `treshold = "5m"` fails loudly with an
`unknown keys:` error rather than silently leaving the default in place.

### The default, and the omitted-vs-explicit-zero asymmetry

The `threshold` resolution mirrors the turn cap's deliberate asymmetry:

- **Key omitted** (no `[stall]` block, or `threshold` unset) → the built-in
  default of **5 minutes** applies. Detection is on by default.
- **Explicit `threshold = "0"`** (or any negative duration) → detection is
  **disabled**. See below.
- **Positive duration** → that duration is the idle ceiling.

An empty or unparseable value falls back to the 5-minute default rather than
disabling detection, so a malformed edit fails safe (still monitored).

## Disabling stall detection

Set the threshold explicitly to zero:

```toml
[stall]
threshold = "0"
```

A zero (or negative) threshold turns detection off completely: no idle timer is
attached to any dispatch, nothing is classified, and no status/evidence/notify
escalation fires. Note this is the **only** way to disable it — omitting the key
keeps the 5-minute default, it does not turn detection off.

Disabling changes nothing about how a slice runs; it only removes the early
possibly-wedged signal. A silent slice still runs out its wall-clock timeout
exactly as it would with detection on — because detection never killed it in the
first place.
