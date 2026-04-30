# Springfield

Plugin-first, agent-native Springfield with a thin local CLI.

Springfield keeps project-local state in the repo, distributes end-user setup through host plugins/catalogs first, and keeps a thin local CLI for project bootstrap, local host sync, and runtime execution.

## Public CLI

```bash
springfield            # Show help and next-step guidance
springfield init       # Scaffold springfield.toml and .springfield/
springfield install    # Sync local Claude Code/Codex host artifacts
springfield doctor     # Check local agent CLI availability
springfield plan       # Compile a work request into a runnable batch
springfield start      # Execute the active batch from its saved cursor
springfield status     # Inspect the active batch or work
springfield version    # Print build version
```

Supported agents (all fully executable):

- Claude Code
- Codex CLI
- Gemini CLI (opt-in via `--agents ...,gemini` or by adding `"gemini"` to `agent_priority`; set `GEMINI_API_KEY` or sign in to the Gemini CLI before running headless)

## Install

Primary path (one step for both plugin skills and CLI binary):

```
/plugin marketplace add brentguistwite/springfield
/plugin install springfield@brentguistwite
```

The plugin ships a `SessionStart` hook that downloads the matching `springfield` CLI binary from the GitHub release pinned by the installed plugin version, verifies it against the plugin-shipped `hooks/checksums.txt` manifest, caches it under `~/.cache/springfield/<version>/`, and symlinks it to `~/.local/bin/springfield`. Add `~/.local/bin` to your `PATH` once; afterwards every `/plugin update springfield@brentguistwite` refreshes the plugin skills **and** the CLI binary in a single step — no `go install` or `brew upgrade` needed.

Slash commands available after install: `/springfield:plan`, `/springfield:start`, `/springfield:status`, `/springfield:recover`.

Manage the install:

```
/plugin list                               # verify install
/plugin update springfield@brentguistwite  # pull latest plugin + CLI
/plugin uninstall springfield@brentguistwite
```

`/plugin marketplace add` accepts the `owner/repo` GitHub shorthand; use the full `https://github.com/brentguistwite/springfield.git` URL if your environment needs it.

### Alternate Install Paths

Use these only if you need the CLI outside the plugin flow:

- Tarball: download from [Releases](https://github.com/brentguistwite/springfield/releases), then `tar -xzf springfield_<version>_<os>_<arch>.tar.gz && install -m 0755 springfield /usr/local/bin/springfield`.
- Homebrew formula from release asset: `brew install --formula https://github.com/brentguistwite/springfield/releases/download/vX.Y.Z/springfield.rb`.
- From source: `go install .` inside this repo.

The SessionStart hook currently supports macOS and Linux (amd64/arm64). Windows CLI users must install via the alternate paths above.

### Codex Plugin Directory

Inside Codex, add the GitHub-backed marketplace, then install Springfield from the plugin directory:

```bash
codex plugin marketplace add brentguistwite/springfield --sparse .agents/plugins
codex
```

Then inside Codex:

```text
/plugins
```

Choose the `Brent Guistwite` marketplace, then install `springfield`.

Manage the marketplace with:

```bash
codex plugin marketplace upgrade
codex plugin marketplace upgrade brentguistwite
codex plugin marketplace remove brentguistwite
```

Notes:

- `codex plugin marketplace add` accepts GitHub shorthand (`owner/repo`), Git URLs, SSH Git URLs, and local marketplace roots.
- For Codex GitHub installs, this repo exposes the marketplace from `.agents/plugins/marketplace.json` and resolves the actual plugin from the repo root as a Git-backed plugin source.
- This repo also ships `.claude-plugin/marketplace.json` for Claude-style marketplace discovery.
- If the marketplace or plugin does not appear immediately, restart Codex once and reopen `/plugins`.

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
Run `springfield init` and follow the prompt to scaffold one.

Project-level agent execution settings live in `springfield.toml`. Example:

```toml
[project]
agent_priority = ["claude", "codex"]

[agents.claude]
permission_mode = "bypassPermissions"

[agents.codex]
sandbox_mode = "danger-full-access"
approval_policy = "never"

# Opt in to Gemini by adding "gemini" to agent_priority; scaffold below.
# [agents.gemini]
# approval_mode = "yolo"
# sandbox_mode = "sandbox-exec"
```

Notes:

- `springfield init` asks for the agent priority order (default `claude,codex`) and scaffolds `springfield.toml` + `.springfield/` with recommended execution settings for Claude and Codex. Use `--agents codex,claude` to skip the prompt, or pipe input to run non-interactively.
- Gemini is execution-supported but opt-in. Pass `--agents claude,codex,gemini` (or edit `agent_priority`) to include it. See [`docs/release.md`](docs/release.md#2026-04-gemini-cli-execution-support) for the migration note.
- Primary end-user install is the Claude marketplace or Codex plugin/catalog flow.
- `springfield install` is the local sync/bootstrap/fallback path after `init`.
- Re-running `init` preserves existing config, only filling in missing recommended defaults and agent priority. Use `springfield init --reset` to back up the current config and rewrite it from scratch.
- Runtime state under `.springfield/` is local project state and should not be committed.

## Runtime Flow

Use `plan` to compile a work request into a runnable batch, then `start` to execute it.

The `springfield:plan` skill reads your plan (file or prompt), decides slice boundaries, and emits a JSON payload to `springfield plan --slices -`:

```bash
# Skill pipes the payload for you; direct usage looks like:
springfield plan --slices path/to/payload.json

# Or via stdin:
springfield plan --slices - <<'JSON'
{
  "title": "Implement OAuth 2.0 login",
  "source": "<your original plan text>",
  "slices": [
    {"id": "01", "title": "scaffold auth package", "summary": "..."},
    {"id": "02", "title": "wire login endpoint",    "summary": "..."}
  ]
}
JSON

# Execute the compiled batch
springfield start

# Check progress
springfield status
```

Execution is serial by default. Parallel execution only happens when the batch explicitly marks independent phases — this is rare and must be intentional.

If a batch already exists, use `--replace` to archive it and start fresh, or `--append` to add new slices:

```bash
springfield plan --replace --slices new-payload.json
springfield plan --append  --slices extra-slices.json
```

Use `springfield doctor` whenever local agent tooling looks unhealthy or a host CLI is missing.

## Release Assets

Tagged releases publish:

- `springfield_<version>_darwin_amd64.tar.gz`
- `springfield_<version>_darwin_arm64.tar.gz`
- `springfield_<version>_linux_amd64.tar.gz`
- `springfield_<version>_linux_arm64.tar.gz`
- `springfield.rb`

The SessionStart hook downloads the matching tarball automatically on `/plugin install` / `/plugin update`, then verifies the extracted binary against plugin-shipped `hooks/checksums.txt`. Manual install instructions live under [Alternate Install Paths](#alternate-install-paths).

## Development

```bash
go test ./...
go run . --help
go run . install --help
```

## Release Workflow

Springfield uses [release-please](https://github.com/googleapis/release-please) on `main` to maintain a single open release PR driven by [Conventional Commits](https://www.conventionalcommits.org/). A hydration workflow on that PR runs `go run ./cmd/release-sync` to keep `version.txt`, every plugin/marketplace manifest, and `hooks/checksums.txt` in lock-step. Merging the release PR creates the tag, which triggers the publish workflow. Maintainer details live in [docs/release.md](docs/release.md).

## License

Private. All rights reserved.
