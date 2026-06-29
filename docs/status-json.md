# `springfield status --json`

Emits a stable, versioned view-model of the active batch for programmatic
consumers (e.g. the Flightdeck cockpit). The schema is a deliberate contract,
not an internal state dump.

## Envelope

Every response carries `schema_version` and a `state` discriminator
(`active` | `orphan` | `idle`). Absent sections are explicit `null`.

| field | meaning |
|-------|---------|
| `schema_version` | contract version (currently `1`; additive changes bump it) |
| `state` | `active`, `orphan` (batch.json missing), or `idle` (no active batch) |
| `summary` | human-readable one-liner, always present |
| `batch` | `{id, title}` or null |
| `progress` | `{completed, total, phase_index, phase_total, all_done, parallel_in_flight}` or null |
| `spend` | `{total_usd, per_adapter, iterations, unpriced_runs, skipped_files}` or null |
| `flags` | `{fatal_error, cost_capped, last_retry}` or null |
| `plans` | array of per-plan cards (always an array, possibly empty, in `active` state); `null` in `idle` and `orphan` states |

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
| `phase_index` | current phase index (0-based); -1 when all done |
| `phase_total` | total phase count |
| `all_done` | true when every plan is integrated |
| `parallel_in_flight` | true when the current phase runs plans in parallel and 2+ are `running` (same classifier as per-plan `status`, so it is never true while those plans read `stalled`) |

## Per-plan card

`id`, `title`, `status`, `branch`, `base_branch`, `base_head`, `review`,
`attempt`, `last_error`, `evidence_path`, `merge`, `integration`.

- `status` (owned enum): `pending` | `running` | `stalled` | `needs-human` |
  `failed` | `done` | `merged`.
  - `running` vs `stalled`: a started-but-non-terminal plan (internally
    `running` or `interrupted`) is `running` only when a live `springfield`
    process owns the control-plane lock; otherwise it is `stalled` — the owning
    process died without recording a terminal result and the plan needs resume
    or abandon. `stalled` is distinct from `needs-human` (mechanical stop vs
    semantic review stop — different remedy).
  - `done` = completed-but-not-fully-integrated, `merged` = fully integrated
    (`count(status=="merged")` equals `progress.completed`). Unknown future
    internal states surface as `needs-human`.
- `branch` is the per-plan worktree branch (deleted on merge success).
  **`base_branch` is the durable integration target — push this to open the PR.**
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
  - `reason`: `cleanup-failed` | `source-sync-failed` when `needs_attention`;
    omitted when `clean`.

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
