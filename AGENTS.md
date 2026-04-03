# Springfield Agent Instructions

Build a shareable, local-first product that unifies the current Ralph script/skill and Ralph Conductor script/skill behind a simpler UX.

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

## Working Rules

- Treat the CLI and TUI as product surfaces, not debug wrappers.
- Design around public module contracts first, then internals.
- In Go, prefer small cohesive packages with explicit exported APIs over grab-bag utility packages.
- Prefer stable project-local state over hidden global machine state.
- Keep docs and examples good enough for a teammate with no prior Ralph context.
