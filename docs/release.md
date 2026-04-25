# Springfield Release Workflow

Springfield ships one version across every surface — CLI binary, Claude plugin manifest, Claude marketplace entry, Codex plugin manifest, Codex marketplace entry, release tag, and `hooks/checksums.txt`. The single source of truth is `version.txt`, owned by [release-please](https://github.com/googleapis/release-please). All derived files are generated, not hand-edited.

## How a release happens now

Day-to-day work merges to `main` with [Conventional Commits](https://www.conventionalcommits.org/). `release-please` watches `main` and maintains a single open release PR that updates `version.txt` and `CHANGELOG.md` from those commits.

1. Push a feature branch using a conventional commit (`feat:`, `fix:`, `deps:`, etc.). Open and merge the PR as usual.
2. The [`Release Please`](../.github/workflows/release-please.yml) workflow opens or updates the release PR named `release-please--branches--main`. It bumps `version.txt` based on the commits since the last release.
3. The [`Release PR Hydrate`](../.github/workflows/release-hydrate.yml) workflow runs on that PR, executes [`go run ./cmd/release-sync`](../cmd/release-sync/main.go), and commits any drift back to the PR branch with the `[release-sync]` sentinel. The hydration step:
   - propagates `version.txt` into all four plugin/marketplace manifests
   - rebuilds each platform archive with deterministic flags and refreshes `hooks/checksums.txt`
   - runs `go run ./cmd/release-sync -check` to assert idempotency
   - runs `SPRINGFIELD_RELEASE_TAG=v<version> go test ./tests/plugin/...` to assert version parity
4. A maintainer reviews the green release PR and merges it. **No manual tag push.**
5. `release-please` tags `vX.Y.Z` once `main` advances. It is configured with `skip-github-release: true`, so it does not publish an empty release page.
6. The existing [`Release`](../.github/workflows/release.yml) workflow is tag-triggered. It rebuilds artifacts with the same `-trimpath -buildvcs=false` flags, verifies the rebuilt binaries against the committed `hooks/checksums.txt`, renders `Formula/springfield.rb`, uploads release assets, creates the GitHub release as the final step, and runs a post-publish smoke that re-downloads each asset by its public URL and re-verifies the checksum.

## Versioning contract

- Operator-visible version: `version.txt`. Edit through `release-please`, never by hand.
- Tool state: `.release-please-manifest.json`. Initialized once during bootstrap; thereafter `release-please` owns it.
- Tag format: `vX.Y.Z`. Plugin/CLI versions are always `X.Y.Z` (no `v` prefix in manifests).
- Bump rules:
  - `fix:` / `deps:` → patch
  - `feat:` → minor
  - `!` / `BREAKING CHANGE:` → major
  - Plugin-only changes still get a normal release. We always ship a matching CLI artifact even if Go code did not change, so plugin/CLI versions stay locked.

## Plugin metadata is release-critical

`release-sync` keeps these in lock-step. Do not edit by hand:

- [`version.txt`](../version.txt)
- [`.claude-plugin/plugin.json`](../.claude-plugin/plugin.json)
- [`.claude-plugin/marketplace.json`](../.claude-plugin/marketplace.json)
- [`.codex-plugin/plugin.json`](../.codex-plugin/plugin.json)
- [`.agents/plugins/marketplace.json`](../.agents/plugins/marketplace.json)
- [`hooks/checksums.txt`](../hooks/checksums.txt)

`hooks/checksums.txt` is plugin-shipped, not a published release asset. Each line is keyed by archive asset name (`./springfield_<version>_<os>_<arch>.tar.gz`); the hash is the SHA256 of the extracted `springfield` binary inside that archive.

## Manual sync (rare)

Outside the release-PR flow, regenerate everything locally with:

```bash
go run ./cmd/release-sync           # propagate version.txt + rebuild checksums
go run ./cmd/release-sync -check    # idempotency guard
```

Use `-skip-build` to update only the manifest version fields.

## Rollout window

Between "release PR merges into `main`" and "publish workflow uploads assets," `main` advertises `vX.Y.Z` slightly before `vX.Y.Z` assets exist on GitHub. The contract:

- **Existing installs**: `SessionStart` keeps the previously cached CLI binary if the exact-version asset is still missing. The hook surfaces a visible `springfield: VERSION MISMATCH` warning so operators know an upgrade is in flight.
- **Fresh installs**: fail visibly during the rollout window. Retrying after the publish workflow finishes recovers cleanly.
- The `hooks/tests/run.sh` `Test 6` case exercises this rollout-window fallback.

## Rollback

- If publish fails after tag creation but before assets upload: fix the workflow and rerun publish for the same tag. Because the GitHub release object is the final step, the broken state is invisible to the release page.
- If publish is merely slow: existing installs keep their previous CLI; fresh installs recover automatically once assets land.
- If a bad release ships: do not retag or mutate the tag. Merge a revert/fix to `main` and let `release-please` cut the next patch.

## Published Assets

The workflow publishes:

- `springfield_<version>_darwin_amd64.tar.gz`
- `springfield_<version>_darwin_arm64.tar.gz`
- `springfield_<version>_linux_amd64.tar.gz`
- `springfield_<version>_linux_arm64.tar.gz`
- `springfield.rb`

Each archive contains a single `springfield` binary built with `cmd.Version` set from the Git tag. Before release creation, the workflow unpacks each downloaded `dist/*.tar.gz`, hashes the extracted `springfield` binary, and compares that hash to the committed `hooks/checksums.txt` entry for the matching asset name. `checksums.txt` is not published as a release asset anymore because its semantics are now plugin trust for extracted binaries, not direct verification of tarball bytes.

## Homebrew

`springfield.rb` is rendered during the release from the computed archive URLs and SHA256 values. Keep the generated copy plugin-first: if the release formula wording drifts back to stale TUI-era text, treat that as a release blocker. Install it straight from the release assets:

```bash
brew install --formula https://github.com/<owner>/<repo>/releases/download/v0.1.0/springfield.rb
```

The checked-in [`Formula/springfield.rb`](../Formula/springfield.rb) file is only a template/reference copy. The release asset is the installable one with real URLs and checksums.

## Migration notes

### 2026-04 — Gemini CLI execution support

Gemini CLI joins Claude Code and Codex CLI as a fully executable agent. Existing projects stay valid without changes — Gemini is opt-in.

To enable Gemini on an existing project:

```bash
springfield init --agents claude,codex,gemini
```

That backfills an `[agents.gemini]` block with the recommended defaults (`approval_mode = "yolo"`, `sandbox_mode = "sandbox-exec"`) and adds `"gemini"` to `agent_priority`. Alternatively, edit `springfield.toml` by hand:

```toml
[project]
agent_priority = ["claude", "codex", "gemini"]

[agents.gemini]
approval_mode = "yolo"
sandbox_mode = "sandbox-exec"
# model = "pro"   # optional; empty delegates to Gemini's default
```

Headless runs require either `GEMINI_API_KEY` in the environment or a cached OAuth token at `~/.gemini/oauth_token`. On Linux, `sandbox_mode = "sandbox-exec"` is macOS-only — set it to `"docker"`/`"podman"`/`"runsc"` or leave it empty on other platforms.

Springfield injects its control-plane hook via `GEMINI_CLI_SYSTEM_SETTINGS_PATH`, pointing Gemini at a per-invocation override at `.springfield/gemini-system-settings.json`. The installer never mutates your `~/.gemini/settings.json`.
