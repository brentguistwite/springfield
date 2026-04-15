# Springfield

Plugin-first, agent-native Springfield with a thin local CLI.

Springfield keeps project-local state in the repo, distributes end-user setup through host plugins/catalogs first, and keeps a thin local CLI for project bootstrap, local host sync, and runtime status.

## Public CLI

```bash
springfield            # Show help and next-step guidance
springfield init       # Scaffold springfield.toml and .springfield/
springfield install    # Sync local Claude Code/Codex host artifacts
springfield doctor     # Check local agent CLI availability
springfield status     # Inspect approved Springfield work
springfield resume     # Run or resume approved Springfield work
springfield version    # Print build version
```

V1 host targets:

- Claude Code
- Codex

## End-User Install

Primary path:

- Claude: install Springfield from the Claude marketplace.
- Codex: install Springfield from the Codex plugin/catalog flow.

Use the local CLI only when you need project bootstrap plus local host sync, development setup, or a fallback path outside plugin/catalog distribution.

## Quick Start

Build from source:

```bash
go install .
springfield version
```

Inside a project:

```bash
springfield init
springfield doctor
```

If you need local host integration instead of marketplace/catalog install:

```bash
springfield install
springfield doctor
```

By default `springfield install` writes deterministic local artifacts:

- `~/.claude/commands/springfield.md`
- `~/.agents/skills/springfield/SKILL.md`

These local artifacts carry the shared Springfield playbook plus project context from `AGENTS.md`, `CLAUDE.md`, or `GEMINI.md` when present.

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
- Primary end-user install is the Claude marketplace or Codex plugin/catalog flow.
- `springfield install` is the local sync/bootstrap/fallback path after `init`.
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
