# Springfield Agent Instructions

Build a shareable, local-first conductor that turns a plan into a sequential batch of agent runs across Claude Code, Codex, and Gemini, with isolated worktrees and per-slice evidence. Surface power-user capability through a simple plugin-first UX.

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
Each module should expose a small, obvious public surface through idiomatic Go package entry files such as `package.go`, `doc.go`, or a tight set of top-level exported types and functions. A developer (or AI) should be able to inspect the package's public API and trust what it does without reading every internal file.

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

## Plan Skill and PRD Envelopes

The `plan` skill emits PRD envelopes (not legacy slice payloads); the envelope shape is documented in `docs/prd-format.md`. Agents do NOT author per-plan `AGENTS.md` files — per-plan context lives in the envelope's `context_md` field and is injected by the runner at prompt-build time.

## Auto-branching on protected base

When `springfield start` runs from `main` or `master`, Springfield auto-cuts a feature branch (`springfield/batch-<id>` by default) before the batch and switches back to the original branch when it finishes. Agents must NOT do `git switch -c` themselves before invoking start — the wrapper handles it. The auto-cut branch is local-only; pushing and opening PRs is the operator's job.

<!-- springfield:guardrail -->
## Springfield control plane

Never read, write, edit, or delete files under `.springfield/`. That directory is Springfield's internal state. Writing to it will abort the current run.