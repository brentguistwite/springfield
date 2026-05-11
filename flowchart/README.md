# Springfield Lifecycle Flowchart

An onboarding diagram of Springfield's three state machines (plan, queue, merge), built as a small ReactFlow app and published to GitHub Pages.

## What this is

- A visual map of every status a plan, queue, or merge can be in, plus how they transition.
- A click-through reference: pick a node and the side panel shows what it means and the CLI command that produces or recovers from it.

## Where the data lives

All node and edge data is in [`src/data/lifecycle.ts`](./src/data/lifecycle.ts). The React app imports the data and renders it; it never hardcodes diagram structure inline.

**Edit `src/data/lifecycle.ts` whenever a state machine changes** — when you add, remove, or rename a `PlanStatus`, `QueueStatus`, or `MergeStatus` const in `internal/features/conductor/types.go`, update the matching node list and the `EXPECTED_*_NODE_COUNT` constant in this file.

## Drift enforcement

A Go test, [`internal/features/conductor/lifecycle_count_test.go`](../internal/features/conductor/lifecycle_count_test.go), AST-parses `types.go`, counts the consts in each status block, then reads `src/data/lifecycle.ts` and asserts the expected counts match. If you change a status enum without updating this flowchart, `go test ./...` fails with a precise mismatch message.

## Local dev

```sh
npm install
npm run dev       # vite dev server
npm test          # vitest
npm run build     # outputs dist/
```

## Deploy

`main` pushes that touch `flowchart/` trigger `.github/workflows/deploy.yml`, which builds and publishes `dist/` to GitHub Pages.
