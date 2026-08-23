# Retrospectives

Every batch leaves a trail — per-iteration evidence, verify rounds, stall
records, the archive entry — but that trail is forensic: it tells you what one
run did, not what *keeps* going wrong across many runs. The **retro loop** is the
read side of that gap. When a batch completes, Springfield replays its finalized
archive, classifies what happened into a small set of stable **pattern keys**,
and folds the result into a single `retro.json`. Roll that up across every batch
and every project on the machine and the recurring failure modes — the ones worth
fixing at the source, not one run at a time — rise to the top.

> **Finalize-time only, completion path only.** Retro runs on the same seam that
> archives a *completed* batch, after `FinalizeBatch` has durably relocated
> evidence. The interrupt, cost-cap, and failure branches stay resumable and
> unarchived, so they are deliberately **not** retroed. Retro is also strictly
> **warning-only**: a failed extraction, a bad file, or an unwritable ticket never
> changes a batch's outcome or the process exit code.

## What the loop does

At completion the loop has two stages, both gated by the [`[retro]`](#configuration--the-retro-block) block:

1. **Extract + classify + persist (always, when enabled).** The just-archived
   batch is read back, its plans classified, and the result written atomically to
   `retro.json` beside the archive entry. This is the per-batch record.
2. **Aggregate + file (only when `items_dir` is set).** Every project root on the
   machine is scanned for its archived `retro.json` files, their findings are
   rolled up per pattern key across batches and projects, and any key that clears
   the [filing threshold](#thresholds) is filed as a convention-compliant ticket
   under the configured vault `items_dir`. Filing is idempotent — a later batch
   updates the existing ticket rather than minting a duplicate.

The project roots for stage 2 are expanded at the call site from the two
`Documents` trees where Springfield projects live — `Documents/flosports/*`
(work) and `Documents/personal/*` (personal) — under the operator's home
directory. Each match's `.springfield/archive` is scanned; a root with no archive
tree contributes nothing.

## Artifact layout

Retro reads and writes inside the archive namespace `FinalizeBatch` already owns:

```
.springfield/archive/<batchID>/
├── archive.json          # the batch's finalized archive entry (header + per-plan)
├── plans/<key>/          # per-plan evidence relocated at finalize (summary.json, iter-N/, …)
└── retro.json            # ← the persisted retrospective for this batch
```

`retro.json` carries a batch header (id, title, archived-at, batch mode, total
USD), one entry per plan (identity, branch trail, final status, and the evidence
tail — iteration count, terminal status, stalls, verify rounds), and the
classifier `findings`. A **degraded** extraction — a missing or corrupt
`archive.json`, evidence that could not be located — still produces a valid,
findings-lean report and records the gaps under `degraded[]` rather than failing.

Filed tickets land under the configured vault `items_dir` as
`YYYY-MM-DD-<pattern-key>-auto.md`, dated by the oldest contributing occurrence.

## Pattern-key taxonomy

A **pattern key** is the classifier's stable machine label for one failure mode.
Pattern keys are the retro package's public contract, declared once as the single
source of truth for the classifier table. There are **nine**; eight are per-plan
signals and one (`cost-overrun`) is batch-level. Keys are batch-mode independent —
a `consolidate` report classifies the same as a `per-plan` one — and are what both
the per-batch findings and the cross-project rollup group on.

| Pattern key | Severity | What it flags |
| ----------- | -------- | ------------- |
| `iteration-cap` | warning | A plan exhausted its iteration budget without emitting a completion marker. |
| `stall-wedge` | warning | A plan wedged: [stall detection](stall-detection.md) recorded no forward progress across iterations. |
| `verify-nonconvergence` | warning | The verify gate never converged — halted for a human, or repeated non-zero rounds. |
| `review-needs-human` | warning | Pre-merge review could not converge and halted for a human. |
| `fallback-storm` | warning | The primary agent repeatedly fell through to backup agents (a fallback chain of two or more). |
| `cost-overrun` | warning | Batch spend ran more than 2× the mean of the prior batches (needs ≥ 2 priors as a baseline). |
| `merge-refused` | error | A completed plan's merge was refused; its branch was retained instead of merged-and-deleted. Read only in consolidate mode, so a normally-kept per-plan branch is not a false positive. |
| `setup-failure` | error | A plan died in preflight setup before its agent loop ran. |
| `tamper-detected` | critical | The tamper guard tripped — changes outside the agent's allowed surface, or the guard itself could not run. |

The taxonomy is additive: adding a key is adding a classifier row, changing
neither the layout above nor the config below.

## Thresholds

A pattern is filed as a ticket only when it is genuinely *recurring*, which takes
**both** bars — occurrence count alone is noise, since a single flaky batch can
trip one key three times in one run:

| Bar | Constant | Value | Meaning |
| --- | -------- | ----- | ------- |
| Occurrences | `MinOccurrences` | `>= 3` | Total findings carrying the key across every in-window report. |
| Batch spread | `MinBatches` | `>= 2` | Distinct batches the key must span, so one noisy batch cannot promote it alone. |

Both must hold. The recency window is applied earlier, by narrowing the corpus
before the threshold is checked (see [v1 limits](#v1-limits)).

## Configuration — the `[retro]` block

Retro is a project-wide operational policy (a shareable default, not a
machine-personal secret), so — like `[verify]` and
[`[stall]`](stall-detection.md#configuration--the-stall-block) — its block lives
in the **committed** `springfield.toml`, not the git-ignored
`springfield.local.toml`.

```toml
[retro]
enabled = true                                                   # default; set false to skip retro entirely
items_dir = "/Users/me/Documents/Obsidian/vault/personal/projects/springfield/items"
```

| Key | Type | Default | Meaning |
| --- | ---- | ------- | ------- |
| `enabled` | bool | `true` | Master switch. Omitted → on. An explicit `enabled = false` skips retro entirely at the completion call site — no `retro.json`, no filing. |
| `items_dir` | string (absolute path) | `""` | Vault items directory the filer writes recurring-pattern tickets into. Empty (the default) leaves filing off: the loop still persists `retro.json`, it just never touches a vault. A non-empty value **must be absolute** — the filer runs unattended, so a relative path would resolve against an unpredictable working directory and is rejected at config load. |

Unknown keys are rejected: a typo like `enabeld = false` fails loudly with an
`unknown keys:` error rather than silently leaving the default in place.

### The omitted-vs-explicit asymmetry

`enabled` is a pointer under the hood, so an **omitted** key (no `[retro]` block,
or `enabled` unset) resolves to the built-in default of **on**, distinguishable
from an **explicit** `enabled = false`. There is no separate "filing on/off"
switch: filing is on exactly when `items_dir` is non-empty, mirroring the filer's
own empty-dir-disables contract so the two can never drift.

## The triage contract

Filed tickets are **inbox items, not commitments.** A freshly filed ticket mirrors
the vault item template — `type: item`, a freshly allocated `SPG-<n>` id,
`status: todo`, a `## Context` with concrete batch receipts, the required
`## Acceptance Criteria` heading, and an auto-managed `## Occurrences` receipt log
— but it is filed **without** `agent: ready`.

That omission is the contract. The filer never sets `agent: ready`; a human triaging
the inbox flips it, and only then does the item route to flightdeck intake for an
unattended agent to pick up. On update, the filer touches **only** the
`## Occurrences` log (appending new batch receipts, dedup by batch id) — it never
rewrites a human's frontmatter status, acceptance criteria, or hand-edited prose.
So promoting an item is a one-way, human-owned decision that the loop will not
undo on the next batch.

## v1 limits

- **Finalize-time, completion path only.** Only a batch that archives as
  *completed* is retroed. Interrupted, cost-capped, failed, and abandoned batches
  are never retroed — they stay resumable and unarchived, and their evidence is
  not classified.
- **No recency window in v1.** The aggregator scans **all** archived history on
  every completion (the zero `since`). Because filing is idempotent, refiling the
  same corpus is a no-op update, but a long-past pattern keeps qualifying until
  its ticket is triaged.
- **Fixed project roots.** The cross-project scan expands the two `Documents`
  trees under the operator's home directory; a project living elsewhere is not
  aggregated.
- **Warning-only, best-effort.** Every failure mode (bad archive, unreadable
  evidence, unwritable ticket) degrades to a `warning: retro:` line and is
  swallowed. Retro never blocks, kills, or fails a run.
