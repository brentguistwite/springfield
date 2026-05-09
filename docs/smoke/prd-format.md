# Smoke: PRD format end-to-end

Captured 2026-05-09 against `feat/prd-userstories-parity`.

## Setup

```bash
go build -o /tmp/springfield-smoke .
SMOKE=$(mktemp -d -t sf-smoke)
cd "$SMOKE" && git init -q && git commit -q --allow-empty -m init
/tmp/springfield-smoke init --agents claude
```

## Pipe PRD envelope to `springfield plan --prd -`

```bash
cat <<'JSON' | /tmp/springfield-smoke plan --prd -
{
  "title": "smoke test batch",
  "source": "Smoke test the PRD/userStories format end-to-end.",
  "phases": [{"mode": "serial", "plans": ["01-smoke"]}],
  "plans": [
    {
      "id": "01-smoke",
      "title": "Smoke plan",
      "description": "Verify PRD ingest writes per-plan dir.",
      "context_md": "Test context",
      "user_stories": [
        {"id": "US-001", "title": "First story", "description": "Do thing one",
         "acceptance_criteria": ["thing one done"], "priority": 1, "passes": false, "deps": []},
        {"id": "US-002", "title": "Second story", "description": "Do thing two",
         "acceptance_criteria": ["thing two done"], "priority": 2, "passes": false, "deps": ["US-001"]}
      ]
    }
  ]
}
JSON
```

Output: `Compiled batch "smoke-test-batch" with 1 plan(s).`

## On-disk layout

```
.springfield/plans/smoke-test-batch/batch.json
.springfield/plans/smoke-test-batch/source.md
.springfield/plans/01-smoke/prd.json
.springfield/plans/01-smoke/context.md
.springfield/run.json
```

`batch.json` holds `{id, title, phases, plan_ids}`. Per-plan `prd.json` holds the inner plan + user_stories with `passes: false` initialized. Sibling `context.md` written when envelope `context_md` non-empty.

## `springfield status`

```
Batch: smoke-test-batch
Title: smoke test batch
Phase: 1 of 1
Plans:
  01-smoke
```

## Iteration loop + marker scan

Real agent dispatch not exercised here (no live agent in smoke). The runner iteration loop with `<story-pass>US-XXX</story-pass>` + `<promise>COMPLETE</promise>` marker scan is covered by `internal/features/conductor/planrun/runner_iteration_test.go`:

- `TestSinglePlanIterationThreeStoryFullPass` — 3 stories, agent emits markers across 3 iterations, on-disk `prd.json` ends with all `passes: true`, `progress.md` records each pass + iteration log entry, per-iter evidence at `iter-N/`, summary.json shows `iteration_count=3`.
- `TestSinglePlanIterationCapExhaustion` — no marker, 3-iter cap, fails with `iteration cap reached without completion marker`.
- `TestSinglePlanIterationCompleteMarkerWithStoriesPending` — premature `<promise>COMPLETE</promise>` ignored; warning written to `progress.md`; loop continues.
- `TestSinglePlanIterationAppendProgressNonFatal` — `progress.md` chmod 0444; iteration loop completes successfully (errors logged to stderr).

## Verified

- PRD envelope ingest writes the documented per-plan layout.
- `prd.json` is the inner per-plan shape (envelope `context_md` lives in sibling `context.md`).
- `--prd -` reads stdin; legacy `--slices` shape rejected with explicit error.
- `springfield status` reads the new layout cleanly.
- Full test suite green: `go test ./... -count=1` (24 packages).
