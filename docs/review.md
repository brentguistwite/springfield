# Pre-merge review & the `needs-human` recovery flow

Springfield can run a **pre-merge review gate** on each plan: after every story
passes but *before* the plan's worktree is merged, a configured review command
inspects the work and returns a verdict. If the reviewer is satisfied the plan
merges as usual; if it cannot converge, the plan halts in the **`needs-human`**
status with its worktree and branch preserved for an operator to take over.

This document describes the operator exit path out of `needs-human`. For the
`[review]` configuration block (how to enable and configure the gate) see
[`prd-format.md`](./prd-format.md).

## States at a glance

A plan that goes through the gate ends in one of two terminal review states:

| Verdict       | Status        | Outcome                                          |
| ------------- | ------------- | ------------------------------------------------ |
| `approved`    | `completed`   | Worktree merges; batch continues.                |
| `needs-human` | `needs-human` | Merge is **withheld**; worktree/branch preserved. |

`needs-human` is a deliberate halt, not a failure. The plan's stories all still
pass — the reviewer simply judged the result not safe to merge unattended. The
review findings are written to the plan's review evidence and summarized in
`springfield status`.

## The recovery loop

```
needs-human halt
      │
      ▼
operator reads findings   (springfield status  ·  review evidence)
      │
      ▼
operator edits the work   (in the preserved worktree, or revises the plan)
      │
      ▼
springfield recover --plan <id>      → resets the plan to `pending`
      │
      ▼
springfield start                    → re-runs the plan and RE-ENTERS the
      │                                 review gate (does NOT merge unreviewed)
      ▼
approved → merge   |   needs-human → loop again
```

### 1. See what halted

`springfield status` runs the diagnosis and lists every plan awaiting human
review, with the reviewer's findings, the evidence path, and the recover hint:

```
Plans needing human review (1):
  - 04-recovery-and-status: review could not converge
    Evidence: /…/.springfield/execution/plans/04-recovery-and-status/evidence
    Recover: springfield recover --plan 04-recovery-and-status (re-review retry)
```

Within that evidence directory, each review round writes a `review-iter-N`
sub-directory (the reviewer's prompt and output) and each fix-iteration writes
a `review-fix-N` sub-directory (the implementer's prompt and output) so you
can trace what the reviewer saw and how the fix-loop responded.

`springfield recover --plan <id> --diagnose` shows the same plan's available
actions, including the re-review **retry**.

### 2. Address the findings

The halted plan's **worktree and branch are preserved**, so you can:

- edit directly in the preserved worktree to fix what the reviewer flagged, or
- revise the plan and let the next run regenerate the work.

The findings to address are in `springfield status` and in the plan's review
evidence.

> **Commit your edits.** The review gate diffs the plan branch's `HEAD` against
> its base ref; uncommitted working-tree changes are invisible both to the
> re-review and to the eventual merge. After editing the worktree, run
> `git -C <worktree> commit -am '...'` (or equivalent) before `springfield
> recover`/`start`. If you forget, the next review round will see the same
> stale code and loop on the same findings.

### 3. Reset with `springfield recover`

```
springfield recover --plan <id>
```

This performs the **re-review retry**: it resets the plan's status to `pending`
and clears the stale halt bookkeeping (error, exit reason, merge and cleanup
outcomes), recording the transition in the plan's history.

Crucially, `recover` does **not** touch the plan's `prd.json`. The plan
re-enters with all of its stories still passing.

### 4. Re-run with `springfield start`

```
springfield start
```

Because every story still passes, the runner hits its top-of-loop
short-circuit and **re-runs the pre-merge review gate** against the
(now possibly human-edited) worktree — it does not merge the work blind. The
gate runs at both completion sites, so a recovered plan is always re-reviewed
rather than merged unreviewed.

If the reviewer is now satisfied the plan merges and the batch continues. If it
still cannot converge the plan returns to `needs-human` and the loop repeats —
each pass reviewing the latest state of the worktree.

## Why retry means *re-review*, not *re-implement*

The re-review semantics fall out of one deliberate invariant: `recover` resets
the status but leaves `prd.json` untouched. A `needs-human` plan therefore
comes back with all stories already passing, which routes it straight through
the runner's completion path — and that path runs the review gate. There is no
new control flow; the recovery action is a thin, honest surface over the state
the runner already maintains.
