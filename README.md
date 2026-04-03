# Springfield

Local-first CLI and TUI entrypoint that unifies Ralph and Ralph Conductor behind one binary.

## Status

**Bootstrap phase.** The command surface is stable but subcommands are placeholders.

Running bare `springfield` opens a TUI shell. CLI subcommands remain accessible underneath:

```
springfield              # TUI-first interactive shell
springfield ralph        # Ralph workflows (placeholder)
springfield conductor    # Conductor workflows (placeholder)
springfield doctor       # Local setup diagnostics (placeholder)
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

Springfield reads project config from `springfield.toml` at the repo root.
See [`springfield.toml.example`](springfield.toml.example) for the reference format.

Runtime state (generated files, caches) lives in `.springfield/` and should not be committed.

## Architecture

```
main.go                     # Entrypoint — delegates to cmd.Execute()
cmd/                        # Cobra command wiring
  root.go                   # Root command — bare invocation launches TUI
  tui.go                    # Explicit `springfield tui` entry
  ralph.go                  # Ralph subcommand placeholder
  conductor.go              # Conductor subcommand placeholder
  doctor.go                 # Doctor subcommand placeholder
internal/features/tui/      # Bubble Tea TUI shell
  app.go                    # Startup TUI placeholder
tests/cmd/                  # CLI smoke tests
```

## Development

```bash
go test ./...               # Run all tests
go run .                    # Launch Springfield
```

## License

Private. All rights reserved.
