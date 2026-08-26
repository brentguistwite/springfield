# Architecture

A one-page map of how Springfield is wired: the layers, the execution path of a batch, and the recipes for common extensions. For test layout and gates see [testing.md](testing.md); for the PRD envelope contract see [docs/prd-format.md](prd-format.md).

## Layers

| Layer | Path | Role |
|---|---|---|
| Entry | `main.go` | Calls `cmd.Execute()`; nothing else |
| Commands | `cmd/` | One file per cobra command (`start`, `plan`, `status`, …), all registered in a single list in `cmd/root.go`. Also hosts auxiliary mains that are *not* wired into the CLI: `cmd/capture-fixture` (records transcript captures), `cmd/regen` (re-renders skills/commands), `cmd/release-sync` (propagates version.txt into plugin manifests) |
| Core machinery | `internal/core/` | Feature-agnostic engines: `internal/core/agents` (adapter boundary + per-agent adapters), `core/config` (springfield.toml, strict unknown-key rejection), `internal/core/exec` (subprocess streaming), `core/runtime` (agent dispatch loop), `core/lock`, `core/stall`, `core/gitstatus` |
| Features | `internal/features/` | Product capabilities, one package each: `conductor` (+ `batchexec`, `planrun`, `planmerge`, `planreview` subpackages) orchestrate batches; plus `batch`, `prd`, `planner`, `execution`, `cost`, `statusview`, `doctor`, `notify`, `autobranch`, `verify`, `worktreesetup`, `portblock`, `wakelock`, `skills`, `playbooks`, `workflow` |
| State | `internal/storage/` | The `.springfield/` control-plane layout, atomic writes, resolve-upward |

The dominant dependency direction flows downward: commands depend on features and core; features depend on core. Narrow exceptions run the other way, where core consumes leaf feature packages: the claude/codex/opencode adapters use `features/cost` for token-cost extraction, and `core/config` validates against the `features/portblock` and `features/prd` types. New core→features edges deserve scrutiny — prefer pushing the dependency up into the feature layer.

## Execution path of `springfield start`

1. `main.go` → `cmd.Execute()` → the `start` command's `RunE`
2. Acquires the single-writer flock (`internal/core/lock`)
3. Loads project state via `conductor.LoadProjectRaw` (`internal/features/conductor`)
4. Resolves branch mode / auto-branching (`internal/features/autobranch`)
5. Phase-ordered execution by `batchexec` — serial phases one at a time, parallel phases up to `[project] max_parallel`
6. Per-plan execution by `planrun`: isolated worktree, optional setup hook (`worktreesetup`), prompt build with envelope `context_md`
7. Agent dispatch loop in `core/runtime` builds commands via the adapter's `Commander`, runs them through `core/exec`, classifies results via optional capabilities (`ResultValidator`, `ErrorClassifier`, `Cooldowner`, `TranscriptDecoder`)
8. Evidence capture (`features/execution`), cost rollups (`features/cost`), terminal notification (`features/notify`)
9. Merge integration back to base via `planmerge`; verify gate via `features/verify`

## Adapter boundary

All agent interaction crosses `internal/core/agents`: the `Adapter`/`Commander` interfaces plus deliberately optional capability interfaces consumed by type assertion at dispatch time. Concrete adapters live in `claude/`, `codex/`, `gemini/`, `opencode/` and are assembled **only** in `core/agents/catalog`. Every production capability an adapter must provide is pinned with a compile-time assertion beside its constructor (AGENTS.md Principle 5).

## Package entry conventions

Packages expose their public surface through an entry file: `index.go` holding the primary exported API (the dominant convention — agents, config, exec, conductor, doctor, storage), or a `doc.go` package comment where prose helps. A tight set of top-level exported types/functions around it; internals stay unexported.

## Recipes

### Adding a command

1. New file in `cmd/` defining the `*cobra.Command`.
2. Register it in the `AddCommand` list in `cmd/root.go`.
3. Black-box e2e coverage under `tests/cmd/` using the binary-building helpers there.

### Adding a feature package

1. Directory under `internal/features/<name>` with an `index.go` (or `doc.go`) exposing the public API.
2. Unit tests co-located; integration/e2e coverage mirrored under `tests/<area>/`.
3. Config keys belong in `internal/core/config` (types + validation) — unknown keys are rejected at load, so document every key you add.
4. If the feature renders user-facing skill/command surfaces, define them in `internal/features/skills/types.go` and run `go run ./cmd/regen`.

### Changing transcript parsing

Regenerate the real-capture corpus with `go run ./cmd/capture-fixture` — see [testing.md](testing.md).
