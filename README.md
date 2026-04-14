# Springfield

Plugin-first, agent-native Springfield with a thin local CLI.

Springfield keeps project-local state in the repo, installs Springfield-branded host artifacts for Claude Code and Codex, and uses a small public CLI for bootstrap and runtime status.

## Public CLI

```bash
springfield            # Show help and next-step guidance
springfield init       # Scaffold springfield.toml and .springfield/
springfield install    # Install Springfield into Claude Code and Codex
springfield doctor     # Check local agent CLI availability
springfield status     # Inspect approved Springfield work
springfield resume     # Run or resume approved Springfield work
springfield version    # Print build version
```

V1 host targets:

- Claude Code
- Codex

## Quick Start

Build from source:

```bash
go install .
springfield version
```

Inside a project:

```bash
springfield init
springfield install
springfield doctor
```

By default `springfield install` writes:

- `~/.claude/commands/springfield.md`
- `~/.codex/skills/springfield/SKILL.md`

These installed Springfield artifacts carry the shared Springfield playbook plus project context from `AGENTS.md`, `CLAUDE.md`, or `GEMINI.md` when present.

## Configuration

Springfield resolves the project root from `springfield.toml` at the repo root.
See [`springfield.toml.example`](springfield.toml.example) for the current file shape.

Project-level agent execution settings live in `springfield.toml`. Example:

```toml
[project]
default_agent = "claude"

[agents.claude]
permission_mode = "bypassPermissions"

[agents.codex]
sandbox_mode = "danger-full-access"
approval_policy = "never"
```

Notes:

- `springfield init` scaffolds `springfield.toml` and `.springfield/`.
- `springfield install` is the primary bootstrap step after `init`.
- Recommended execution defaults target Claude Code and Codex together.
- Runtime state under `.springfield/` is local project state and should not be committed.

Internal execution config at `.springfield/execution/config.json` is Springfield-managed state. The default local shape is:

```json
{
  "plans_dir": ".springfield/execution/plans",
  "worktree_base": ".worktrees",
  "max_retries": 2,
  "single_workstream_iterations": 50,
  "single_workstream_timeout": 3600,
  "tool": "claude",
  "sequential": [],
  "batches": []
}
```

## Runtime Flow

Once Springfield work has been planned and approved, use:

```bash
springfield status
springfield resume
```

Use `springfield doctor` whenever local agent tooling looks unhealthy or a host CLI is missing.

## Install Methods

Tagged releases publish:

- `springfield_<version>_darwin_amd64.tar.gz`
- `springfield_<version>_darwin_arm64.tar.gz`
- `springfield_<version>_linux_amd64.tar.gz`
- `springfield_<version>_linux_arm64.tar.gz`
- `checksums.txt`
- `springfield.rb`

Install a downloaded archive with:

```bash
tar -xzf springfield_<version>_<os>_<arch>.tar.gz
install -m 0755 springfield /usr/local/bin/springfield
```

Homebrew release asset:

```bash
brew install --formula https://github.com/<owner>/<repo>/releases/download/vX.Y.Z/springfield.rb
```

## Development

```bash
go test ./...
go run . --help
go run . install --help
```

## Release Workflow

Maintainer release steps live in [docs/release.md](docs/release.md).

## License

Private. All rights reserved.
