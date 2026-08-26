# Springfield Agent Instructions

Build a shareable, local-first conductor that turns a plan into a phase-ordered batch of agent runs across Claude Code, Codex, Gemini, and OpenCode — phases run in declared order, with parallel phases executing concurrently (up to `[project] max_parallel`, default 3) in per-plan-branches mode and running sequentially in consolidate mode — with isolated worktrees and per-slice evidence. Surface power-user capability through a simple plugin-first UX.

## Product Priorities

- Make first-run setup easy. Prefer guided flows over hand-editing config.
- Keep install/distribution simple. Favor patterns that are easy for teammates to adopt.
- Preserve current power-user capabilities where they matter, but hide incidental complexity.
- Prefer adapter boundaries over agent-specific branching spread through the codebase.

## Architecture Principles

### 1. Deep Modules
Design cohesive chunks of functionality that encapsulate complex internals behind simple public interfaces. Avoid a tangled web of tiny, interconnected "shallow" modules. A module should do a lot of work but be easy to call.

### 2. Feature-Based File Structure
The filesystem reflects the logical mental map of features. Each feature is a self-contained directory. Don't jumble unrelated concerns together.

### 3. Progressive Disclosure of Complexity
Each module should expose a small, obvious public surface through an idiomatic Go package entry file — `index.go` holding the primary exported API (the dominant convention here: agents, config, exec, conductor, doctor, storage), a `doc.go` package comment, or a tight set of top-level exported types and functions. A developer (or AI) should be able to inspect the package's public API and trust what it does without reading every internal file.

Keep file and package names idiomatic for Go. Prefer clear package boundaries, exported contracts, and internal helpers that stay hidden behind those boundaries.

### 4. Graybox Boundaries and Testing
Strict boundaries between modules. Tests should target the exported package interface and observable behavior, not unexported internals. This locks down expected behavior at the boundary so internals can be safely refactored or delegated.

### 5. Required-by-Default Capabilities
A capability that production correctness depends on belongs in a **required** interface method, not an **optional** one discovered by `x.(SomeInterface)`. A type assertion that misses falls back silently — the compiler can't see it, and a graybox test that hands the boundary a capable collaborator will pass while the production object graph (often a thin wrapper or adapter) quietly takes the fallback. Make a capability optional only when some implementations *legitimately* don't provide it — never merely to spare test doubles from implementing it (give the doubles a trivial impl instead). When optionality is genuinely warranted: (a) **embed** the concrete type in delegating wrappers rather than hand-forwarding a subset of methods, so new capabilities propagate automatically; and (b) pin each production type that must satisfy the optional interface with a compile-time assertion (`var _ Iface = (*T)(nil)`), so an omission fails the build, not a shipped run. The boundary test alone is insufficient here — also assert at the point where the production object is assembled.

## Working Rules

- Treat the CLI as a product surface, not a debug wrapper.
- Design around public module contracts first, then internals.
- In Go, prefer small cohesive packages with explicit exported APIs over grab-bag utility packages.
- Prefer stable project-local state over hidden global machine state.
- Keep docs and examples good enough for a teammate with no prior Springfield context.
- Editing skill/command definitions in `internal/features/skills/types.go` requires regenerating the rendered surfaces: `go run ./cmd/regen` (updates `skills/*/SKILL.md` and `commands/*.md`; drift is caught by tests).

## Testing Conventions

Full walkthrough: `docs/testing.md`. The rules agents violate most:

- Unit tests live beside the code (`foo_test.go`, external `foo_test` package by default).
- White-box tests needing internals use the `_internal_test.go` suffix (same package); test-only exports go in `export_test.go`.
- Black-box CLI/integration tests live in `tests/`, mirroring the feature tree; they build and drive the real binary.
- Stdlib `testing` only — no testify/gomock. Table-driven with `t.Run`.
- Replay captured agent transcripts via `testsupport/fixtures.LoadEvents`. Real captures live under `tests/realcaptures/` guarded by sha256 integrity checks — changing transcript parsing requires regenerating them with `go run ./cmd/capture-fixture`; never hand-edit `.jsonl` fixtures.
- Gates: `go vet ./...`, `golangci-lint run` (config: `.golangci.yml`), `go test -race ./...` — all enforced in CI.

## Plan Skill and PRD Envelopes

The `plan` skill emits PRD envelopes (not legacy slice payloads); the envelope shape is documented in `docs/prd-format.md`. Agents do NOT author per-plan `AGENTS.md` files — per-plan context lives in the envelope's `context_md` field and is injected by the runner at prompt-build time.

## Auto-branching on protected base

When `springfield start` runs from `main` or `master`, Springfield auto-cuts a feature branch (`springfield/batch-<id>` by default) as a bare ref and lands the batch on it. The main worktree is never switched — it stays on the original branch for the whole run (slices base off the auto-branch ref; merges publish to it via `git update-ref`), so the operator can keep working on `main` while the batch runs. Agents must NOT do `git switch -c` themselves before invoking start — the wrapper handles the branch. The auto-cut branch is local-only; pushing and opening PRs is the operator's job.

<!-- springfield:guardrail -->
## Springfield control plane

Never read, write, edit, or delete files under `.springfield/`. That directory is Springfield's internal state. Writing to it will abort the current run.