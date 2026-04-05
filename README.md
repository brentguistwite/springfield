# Springfield

Local-first CLI and TUI entrypoint that unifies Ralph and Ralph Conductor behind one binary.

## Status

**Bootstrap phase.** The CLI/TUI shell is in place. `springfield ralph` now persists plan state and run history locally, and conductor now has a real config/state model plus status/run/resume/diagnose CLI surface. Shared execution remains a placeholder executor while the unified runtime lands.

Running bare `springfield` opens a TUI shell. CLI subcommands remain accessible underneath:

```
springfield              # TUI-first interactive shell
springfield ralph        # Ralph plan init/status/run workflows
springfield conductor    # Conductor workflow surface
springfield doctor       # Local setup diagnostics
```

## Install

Requires Go 1.26+.

```bash
go install .
springfield
```

Or run directly:

```bash
go run .
```

## Configuration

Springfield resolves the project root from `springfield.toml` at the repo root.
See [`springfield.toml.example`](springfield.toml.example) for that file's current format. At minimum, set `[project].default_agent`.

For the current 05 conductor surface, `springfield.toml` alone is not enough. The `springfield conductor` commands also require runtime config at `.springfield/conductor/config.json`.

Current minimal conductor config shape:

```json
{
  "plans_dir": ".conductor/plans",
  "worktree_base": ".worktrees",
  "max_retries": 2,
  "ralph_iterations": 50,
  "ralph_timeout": 3600,
  "tool": "claude",
  "fallback_tool": "codex",
  "sequential": ["01-bootstrap", "02-config"],
  "batches": []
}
```

Runtime state (generated files, caches) lives in `.springfield/` and should not be committed.

## Architecture

```
main.go                     # Entrypoint — delegates to cmd.Execute()
cmd/                        # Cobra command wiring
  root.go                   # Root command — bare invocation launches TUI
  tui.go                    # Explicit `springfield tui` entry
  ralph.go                  # Ralph plan init/status/run commands
  conductor.go              # Conductor status/run/resume/diagnose CLI
  doctor.go                 # Doctor diagnostics command
internal/features/ralph/    # Ralph workspace/selection/run module
internal/features/conductor/ # Conductor config/state/scheduling/runner module
internal/features/tui/      # Bubble Tea TUI shell
  app.go                    # Startup TUI placeholder
tests/cmd/                  # CLI smoke tests
tests/ralph/                # Ralph behavior tests
tests/conductor/            # Conductor behavior tests
```

## Development

```bash
go test ./...               # Run all tests
go run .                    # Launch Springfield
go run . ralph --help       # Inspect Ralph subcommands
go run . conductor --help   # Inspect conductor subcommands
```

## License

Private. All rights reserved.
