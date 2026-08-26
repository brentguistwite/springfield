# Testing Guide

How Springfield's tests are organized and which gates every change must pass. The short rules live in `AGENTS.md` ("Testing Conventions"); this page is the walkthrough.

## Test layout

| Location | Kind | Notes |
|---|---|---|
| `<pkg>/foo_test.go` | Unit | Co-located with source, external `foo_test` package by default |
| `<pkg>/foo_internal_test.go` | White-box unit | Same package as source — suffix is the convention for reaching internals |
| `<pkg>/export_test.go` | Test-only export shims | Compiles only under `go test`; exposes internals to external tests |
| `tests/<area>/...` | Black-box integration/e2e | Mirrors the feature tree (`cmd`, `conductor`, `batch`, …); builds and drives the real binary |

There are no build tags separating "unit" from "integration" — one `go test ./...` runs everything.

## Running tests

```sh
go test ./...                          # everything CI runs (plus -race in CI)
go test ./internal/features/cost/      # one package
go test -race ./internal/...           # race detector locally
go run . --help                        # smoke the CLI without building
```

The e2e helpers that build and invoke the real binary live in `tests/cmd/root_test.go` (`runSpringfield`, `buildBinary`, `runBinaryIn`). Tests need no env vars; `SPRINGFIELD_RELEASE_TAG` optionally unlocks plugin-release assertions that skip otherwise.

## Framework

Stdlib `testing` only — no testify, gomock, or ginkgo anywhere in the module. Table-driven with `t.Run` subtests is the norm.

## Transcript fixtures

Agent stdout parsing is pinned against three fixture tiers:

1. **Real captures** — `tests/realcaptures/{claude,codex,opencode}/*.jsonl`, recorded verbatim from actual agent CLI sessions. Each file has a sibling `.meta.json` (tool, version, args, sha256). Integrity gates in `tests/enforcement/integrity_test.go` enforce byte-immutability, NDJSON well-formedness, no orphaned metadata, and parser coverage of every capture.
2. **Scenario fixtures** — hand-authored event sequences in `tests/agents/fixtures/{claude,codex,gemini,opencode}/` (`success.json`, `hard-error.json`, …).
3. **Replay helper** — `internal/testsupport/fixtures.LoadEvents(t, path)` loads a captured transcript into the production-shaped event stream so parser tests consume real bytes.

**Changing transcript parsing?** Regenerate captures with `go run ./cmd/capture-fixture`. Never hand-edit `.jsonl` files under `tests/realcaptures/` — the sha256 checks will (correctly) fail the build.

## Generated surfaces

Skills and slash commands are rendered from Go definitions, not edited directly:

- Source of truth: `internal/features/skills/types.go`
- Regenerate after editing: `go run ./cmd/regen`
- Rendered outputs: `skills/*/SKILL.md`, `commands/*.md`

Drift between Go definitions and rendered files fails `tests/plugin/` and `tests/docs/format_test.go`.

## Meta-tests pinning docs

`tests/docs/format_test.go` treats certain documentation as load-bearing: it asserts `docs/prd-format.md` matches the envelope schema surface and bans stale phrasing across `README.md` / `AGENTS.md` / `docs/index.html`. If you rename a documented contract, expect a test — update both together.

## CI gates

Every PR runs `.github/workflows/ci.yml`: `go vet ./...`, `golangci-lint run` (config in `.golangci.yml`, formatter: gofmt), `go test -race ./...`, and the `release-sync -check` idempotency guard. Run all four before pushing.
