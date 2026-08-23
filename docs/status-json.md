# `springfield status --json`

Emits a stable, versioned view-model of the active batch for programmatic
consumers (e.g. the Flightdeck cockpit). The schema is a deliberate contract,
not an internal state dump.

## Envelope

Every response carries `schema_version` and a `state` discriminator
(`active` | `orphan` | `idle` | `archived`). Absent sections are explicit `null`.

| field | meaning |
|-------|---------|
| `schema_version` | contract version (currently `4`; additive changes bump it — v2 added the per-plan `activity` card, v3 the per-plan `stall` card, v4 the batch-level `retro` digest) |
| `state` | `active`, `orphan` (batch.json missing), `idle` (no active batch and no archive), or `archived` (no active batch; the most-recently-archived batch is surfaced) |
| `summary` | human-readable one-liner, always present |
| `batch` | `{id, title}` or null. In the `orphan` state `title` is `""` (batch.json is gone, so the title is unrecoverable) — fall back to `id` for display |
| `progress` | `{completed, total, phase_index, phase_total, all_done, parallel_in_flight}` or null |
| `spend` | `{total_usd, per_adapter, iterations, unpriced_runs, skipped_files}` or null |
| `flags` | `{fatal_error, cost_capped, last_retry}` or null |
| `plans` | array of per-plan cards (always an array, possibly empty, in `active` and `archived` states); `null` in `idle` and `orphan` states |
| `retro` | `{findings, top_pattern, top_count}` batch-level retrospective digest, or null — present only in the `archived` state when the batch carries a readable `retro.json` (see below) |

### `archived` state

When no batch is active but at least one batch has been archived, `status --json`
reports the most-recently-archived batch (`state: "archived"`) instead of `idle`,
so a controller can read back a just-completed batch after its run cursor was
cleared. Each per-plan card carries `id`, `title`, `status`, `branch`,
`base_branch`, and `evidence_path` (the durable path the plan's evidence was
relocated to under `.springfield/archive/<batch-id>/plans/`).

An archived plan is terminal-integrated, so `status` reflects how it landed:
`merged` in consolidate mode (ff-merged into the base, branch deleted) and
`retained` in per-plan mode (the standalone `springfield/<plan>` branch is
standing, awaiting a PR — nothing was merged into a base). A controller polling
for "which branches still need a PR" keys on `retained`. The compact archive
record carries no merge/cleanup ledger, so `merge`, `review`, and `integration`
are their empty/clean projections. A repo that has never archived a batch stays
`idle`.

### `retro` digest

An archived batch that carries a `retro.json` (written on the successful
completion path to `.springfield/archive/<batch-id>/retro.json`) surfaces a
compact retrospective digest under `retro`. It is a summary, not the full report —
read `retro.json` directly for the per-finding detail.

| field | meaning |
|-------|---------|
| `findings` | total number of classifier findings in the report |
| `top_pattern` | the most-prominent finding's pattern key (the one tripped by the most plans; ties break on the classifiers' stable declaration order); omitted when there are no findings |
| `top_count` | how many plans tripped `top_pattern`; omitted when zero (a batch-level finding with no plans, e.g. `cost-overrun`) |

`retro` is `null` for any batch with no readable digest: it is absent outside the
`archived` state (no retro exists yet), when the batch has no `retro.json`, when
that file is corrupt (the projection degrades to silence, never an error), and
when the report has zero findings (a clean batch reports nothing extra). The text
`springfield status` surface renders the same digest as a one-liner —
`retro: 3 findings (top: verify-nonconvergence x2)` — from the same projection.

### `spend` fields

| field | meaning |
|-------|---------|
| `total_usd` | sum of all priced iterations |
| `per_adapter` | per-adapter breakdown of priced spend; adapters with no positive cost are excluded (matching the text `Spend:` line — unpriced adapters surface via `unpriced_runs`, not a `$0.00` entry), so the field is omitted when nothing is priced |
| `iterations` | total iteration count |
| `unpriced_runs` | runs with non-zero tokens but no price (omitted when zero) |
| `skipped_files` | cost.json files that could not be read; non-zero means `total_usd` may under-count (omitted when zero) |

### `progress` fields

| field | meaning |
|-------|---------|
| `completed` | number of integrated plans |
| `total` | total plan count |
| `phase_index` | current phase index (0-based); `-1` when all done **or** when `phase_total == 0` (no phase data) — check `phase_total > 0` before interpreting |
| `phase_total` | total phase count |
| `all_done` | true when every plan is integrated |
| `parallel_in_flight` | true when the current phase runs plans in parallel and 2+ are `running` (same classifier as per-plan `status`, so it is never true while those plans read `stalled`) |

## Per-plan card

`id`, `title`, `status`, `branch`, `base_branch`, `base_head`, `review`,
`attempt`, `last_error`, `evidence_path`, `agent`, `started_at`, `merge`,
`integration`, `activity`.

- `status` (owned enum): `pending` | `running` | `stalled` | `needs-human` |
  `failed` | `done` | `merged` | `retained`.
  - `running` vs `stalled`: a started-but-non-terminal plan (internally
    `running` or `interrupted`) is `running` only when a live `springfield`
    process owns the control-plane lock; otherwise it is `stalled` — the owning
    process died without recording a terminal result and the plan needs resume
    or abandon. `stalled` is distinct from `needs-human` (mechanical stop vs
    semantic review stop — different remedy).
  - `done` = completed-but-not-fully-integrated; `merged` = fully integrated in
    consolidate mode (ff-merged, branch deleted); `retained` = fully integrated
    in per-plan mode (standalone `springfield/<plan>` branch kept for a PR,
    nothing merged into a base). Both `merged` and `retained` appear in active
    batches (via `ComposeStatus`) and in the `archived` state. The integrated
    invariant is `count(merged) + count(retained)` equals `progress.completed`
    — in a per-plan batch `count(merged)` is 0. Unknown future internal states
    surface as `needs-human`.
- `branch` is the per-plan worktree branch (deleted on merge success).
  **`base_branch` is the durable integration target — push this to open the PR.**
- `agent` (omitted when unset) is the adapter running the plan (`claude` /
  `codex` / `gemini`); `started_at` (omitted until the plan starts) is when the
  current attempt began. Both are surfaced so a live watcher (`status --watch`)
  shows which agent holds each plan and its elapsed time from the same
  projection — never a second source.
- `review.verdict` is `halt` (with a `reason` excerpt) only when the plan
  halted at the pre-merge review gate; otherwise null.
- `merge.status`: `pending` | `refused` | `succeeded` | `failed`.
- `merge.reason`: machine slug (e.g. `drift-detected`); null until a merge is attempted or when no reason was recorded.
- `merge.error`: human-readable failure detail (e.g. the raw git error); null when empty or no merge attempted.
- `integration`: `{state, reason}` rollup of post-merge disposition.
  - `state`: `clean` | `needs_attention`. `needs_attention` means the merge
    succeeded but the plan is **not** integrated (cleanup or source-sync failed)
    — a stuck plan that `merge.status:"succeeded"` alone would mask. Everything
    else (not yet merged, merge pending/refused/failed, or cleanly integrated)
    is `clean`.
  - `reason` (when `needs_attention`; omitted when `clean`):
    - `cleanup-failed` — cleanup ran and failed (artifacts preserved; investigate).
    - `cleanup-unrecorded` — the cleanup ledger was never persisted (a save
      failure); cleanup may have run in memory. An audit-trail gap to verify,
      **not** an active cleanup failure — distinct remediation.
    - `source-sync-failed` — the source resync left the checkout phantom-dirty.
- `activity`: `{phase, detail, round, updated_at}` in-flight progress card, or
  **`null`** for any plan not in the `running` state. It is truthful by
  construction — a stale phase is worse than none, so it is dropped off every
  non-running path (including `stalled`) and stays `null` rather than surfacing a
  value durable truth would contradict.
  - `phase` (coarse lifecycle stage): `implementing` | `reviewing` | `verifying`.
    It is a sub-field *under* the `running` status, not a new status enum value.
    (`merging` is a RESERVED phase value: it is declared in the vocabulary but the
    merge/integration path is not yet instrumented, so it is never emitted today —
    do not rely on observing it.)
  - `detail`: the current story ID (story loop) or an optional human phrase;
    omitted when empty.
  - `round`: per-phase fine counter (iteration / review round / verify round);
    omitted when zero.
  - `updated_at`: RFC-3339 timestamp of the activity.
  - The coarse phase is derived from durable truth (persisted `prd.json` passes →
    current story) so it cannot go stale; a written stamp that the durable state
    contradicts is suppressed. The text `springfield status` surface renders the
    same activity through the same projection, so the two cannot disagree.

### `flags` fields

| field | meaning |
|-------|---------|
| `fatal_error` | batch-level halt reason; null when suppressed (plan recovered) or no halt |
| `cost_capped` | true when spend ceiling was reached |
| `last_retry` | active-batch retry log (mirrors text "Recent retries:"); omitted when empty |

## Consumer rules

- **Trust `integration.state`, not raw merge fields.** A plan can be `done` +
  `merge.status:"succeeded"` yet still need attention (cleanup or source-sync
  failed → not integrated). Don't re-derive that from primitives — read
  `integration.state`: `needs_attention` is the single signal that a merged
  plan is actually stuck. (`merge.status` `refused`/`failed` are also attention
  states, but those are already obvious from `merge.status` itself.)
- **`stalled` means the process died, not the work.** A `stalled` plan needs a
  `springfield` resume/recover, not human review — distinct from `needs-human`.
- **History is out of scope.** This command reports only the active batch for
  the polled repo. Completed/cross-repo run history is the consumer's job.
