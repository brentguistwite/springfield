# Springfield

Local-first CLI and TUI entrypoint that unifies Ralph and Ralph Conductor behind one binary.

## Current Surface

Springfield already ships a TUI-first shell plus stable CLI entrypoints for setup, Ralph, Conductor, diagnostics, and version output. The local-first project state model and product surface are in place, while shared execution still uses a placeholder executor until the unified runtime lands.

Running bare `springfield` opens a TUI shell. CLI subcommands remain accessible underneath:

```bash
springfield            # TUI-first interactive shell
springfield init       # Scaffold springfield.toml and .springfield/
springfield ralph      # Ralph plan init/status/run workflows
springfield conductor  # Conductor workflow surface
springfield doctor     # Local setup diagnostics
springfield version    # Print build version
```

## Install

### Build From Source

Requires Go 1.26.1+.

```bash
go install .
springfield version
springfield
```

Or run directly from a checkout:

```bash
go run .
```

### Tagged Release Binary

Tagged releases publish `tar.gz` archives for:

- macOS `arm64`
- macOS `amd64`
- Linux `arm64`
- Linux `amd64`

Install a downloaded archive by extracting the binary and moving it onto your `PATH`:

```bash
tar -xzf springfield_<version>_<os>_<arch>.tar.gz
install -m 0755 springfield /usr/local/bin/springfield
```

### Homebrew

Each tagged release also publishes a rendered Homebrew formula asset:

```bash
brew install --formula https://github.com/<owner>/<repo>/releases/download/vX.Y.Z/springfield.rb
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

## Release Workflow

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

## Release Workflow

Tagging `vX.Y.Z` runs [`.github/workflows/release.yml`](.github/workflows/release.yml). The workflow builds archives, writes `checksums.txt`, renders a Homebrew formula, and attaches all of them to the GitHub release.

Maintainer release steps live in [`docs/release.md`](docs/release.md).

## Development

```bash
go test ./...               # Run all tests
go run .                    # Launch Springfield
go run . ralph --help       # Inspect Ralph subcommands
go run . conductor --help   # Inspect conductor subcommands
go run . version            # Print the current build version
```

## License

Private. All rights reserved.
