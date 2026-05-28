# Springfield Release Workflow

Springfield ships one version across every surface — CLI binary, Claude plugin manifest, Claude marketplace entry, Codex plugin manifest, Codex marketplace entry, release tag, and the rendered Homebrew formula. The single source of truth is `version.txt`, owned by [release-please](https://github.com/googleapis/release-please). All derived files are generated, not hand-edited.

The CLI (delivered via Homebrew / release tarballs) and the plugin (skills, delivered via the marketplace) are two artifacts. The CLI is the one users upgrade (`brew upgrade springfield`); the plugin is a thin, stable shim and updates rarely. See the [version model](../README.md#install) for the operator-facing framing.

## How a release happens now

Day-to-day work merges to `main` with [Conventional Commits](https://www.conventionalcommits.org/). `release-please` watches `main` and maintains a single open release PR that updates `version.txt` and `CHANGELOG.md` from those commits.

1. Push a feature branch using a conventional commit (`feat:`, `fix:`, `deps:`, etc.). Open and merge the PR as usual.
2. The [`Release Please`](../.github/workflows/release-please.yml) workflow opens or updates the release PR named `release-please--branches--main`. It bumps `version.txt` based on the commits since the last release.
3. The [`Release PR Hydrate`](../.github/workflows/release-hydrate.yml) workflow runs on that PR, executes [`go run ./cmd/release-sync`](../cmd/release-sync/main.go), and commits any drift back to the PR branch with the `[release-sync]` sentinel. The hydration step:
   - propagates `version.txt` into all four plugin/marketplace manifests
   - runs `go run ./cmd/release-sync -check` to assert idempotency
   - runs `SPRINGFIELD_RELEASE_TAG=v<version> go test ./tests/plugin/...` to assert version parity
4. A maintainer waits for the hydrate workflow to finish on the release PR (manifests must be at the new version), reviews the green release PR, and merges it. **No manual tag push.**
5. `release-please` runs again on `main`, sees the merged PR with no tag, and creates both the tag `vX.Y.Z` and the GitHub release object. release-please uses `RELEASE_PLEASE_TOKEN` (a fine-grained PAT) so the release-published event dispatches correctly.
6. The [`Release`](../.github/workflows/release.yml) workflow is `release: published` triggered. It runs preflight (semver + tag-vs-version.txt + manifest parity), rebuilds artifacts with `-trimpath -buildvcs=false`, runs a pre-publish `springfield version` smoke against the linux binary, renders `Formula/springfield.rb`, uploads all assets to the existing release object, runs a post-publish smoke that re-downloads each tarball and confirms its SHA256 matches the rendered formula, and publishes the formula to the Homebrew tap (see [Homebrew](#homebrew)).

> Earlier iterations used `skip-github-release: true` plus a `push: tags` trigger. release-please-action v4 refuses to tag with that flag set when an untagged merged release PR is outstanding, which deadlocks the chain. The current configuration lets release-please own both tag and release object, and `release.yml` uploads assets to the existing release.

## Versioning contract

- Operator-visible version: `version.txt`. Edit through `release-please`, never by hand.
- Tool state: `.release-please-manifest.json`. Initialized once during bootstrap; thereafter `release-please` owns it.
- Tag format: `vX.Y.Z`. Plugin/CLI versions are always `X.Y.Z` (no `v` prefix in manifests).
- Bump rules:
  - `fix:` / `deps:` → patch
  - `feat:` → minor
  - `!` / `BREAKING CHANGE:` → major
  - Plugin-only changes still get a normal release. We always ship a matching CLI artifact even if Go code did not change, so plugin/CLI versions stay locked.
- **Backward-compat gate**: within a major version the stable CLI verbs (`plan`, `start`, `status`, `recover`) and their flags/output must keep working for older plugin skills. Plugin updates lag the CLI (manual on both platforms), so "older plugin + newer CLI" is the normal steady state. The skill-side floor check (`skills.MinCLIVersion`) is the safety valve for the rarer "plugin needs a newer CLI" direction — bump it manually and rarely, only when a skill starts depending on a new CLI capability.

## Plugin metadata is release-critical

`release-sync` keeps these in lock-step. Do not edit by hand:

- [`version.txt`](../version.txt)
- [`.claude-plugin/plugin.json`](../.claude-plugin/plugin.json)
- [`.claude-plugin/marketplace.json`](../.claude-plugin/marketplace.json)
- [`.codex-plugin/plugin.json`](../.codex-plugin/plugin.json)
- [`.agents/plugins/marketplace.json`](../.agents/plugins/marketplace.json)

## Manual sync (rare)

Outside the release-PR flow, propagate the version locally with:

```bash
go run ./cmd/release-sync           # propagate version.txt into all manifests
go run ./cmd/release-sync -check    # idempotency guard
```

## Rollout window

Between "release PR merges into `main`" and "publish workflow uploads assets," `main` advertises `vX.Y.Z` slightly before `vX.Y.Z` assets exist on GitHub. Because the CLI installs via Homebrew/tarball rather than a session hook, this window is benign:

- **Existing installs**: keep running their current `springfield` binary. `brew upgrade springfield` only resolves the new version once the tap formula is published (the final publish step).
- **Fresh installs**: `brew install` resolves whatever the tap currently points at. During the window that is the previous version; retrying after the publish workflow finishes picks up the new one.

## Branch protection (operator setup)

The hydration workflow runs `go run ./cmd/release-sync` from PR-branch source under `contents: write`. release-please always branches from `main`, so any commit landing on `main` is executed by the next hydration run with write access to release artifacts. To preserve the supply chain, `main` must require at least one PR review and forbid direct pushes. Configure this in repository **Settings → Branches → Branch protection rules** for `main`:

- Require pull requests with at least 1 approving review
- Require status checks (`Release PR Hydrate`, plugin tests) before merge
- Disallow direct pushes to `main`

Without these protections, anyone with `contents: write` on the repo can stage code that the next release cycle will execute.

## Rollback

- If publish fails **before** assets upload (preflight, build, render formula): fix the workflow and rerun for the same tag. The release object exists (release-please created it) but is empty, so users see no usable artifacts and `brew install` from the tap still serves the previous version.
- If publish fails **after** assets upload but before tap publish (e.g. post-publish smoke fails, or HOMEBREW_TAP_TOKEN is missing): the release object is visible with tarballs attached, but the tap is stale. Re-running the workflow is safe — the assets-upload step is effectively idempotent (softprops/action-gh-release replaces files of the same name), and the tap-publish step skips when the formula content is unchanged.
- If publish is merely slow: existing installs keep their previous CLI; `brew upgrade` resolves the new version automatically once the tap formula lands.
- If a bad release ships: do not retag or mutate the tag. Merge a revert/fix to `main` and let `release-please` cut the next patch.

## Published Assets

The workflow publishes:

- `springfield_<version>_darwin_amd64.tar.gz`
- `springfield_<version>_darwin_arm64.tar.gz`
- `springfield_<version>_linux_amd64.tar.gz`
- `springfield_<version>_linux_arm64.tar.gz`
- `springfield.rb`

Each archive contains a single `springfield` binary built with `cmd.Version` set from the Git tag. The post-publish smoke re-downloads each tarball and confirms its SHA256 appears in the rendered `Formula/springfield.rb` — that tarball↔formula pairing is what `brew install` trusts.

## Homebrew

`springfield.rb` is rendered during the release from the computed archive URLs and SHA256 values. Keep the generated copy aligned with the marketplace-plugin install path: if the release formula wording drifts back to stale TUI-era text, treat that as a release blocker.

The release publishes the formula two ways:

1. **As a release asset** — installable directly without a tap:

   ```bash
   brew install --formula https://github.com/<owner>/<repo>/releases/download/v0.1.0/springfield.rb
   ```

2. **To the shared tap** `brentguistwite/homebrew-tap` (the supported path), giving operators:

   ```bash
   brew install brentguistwite/tap/springfield
   brew upgrade springfield
   ```

   The tap repo hosts other formulas (e.g. `blackbox`); the publish step rewrites only `Formula/springfield.rb` there, leaving siblings untouched. It requires a `HOMEBREW_TAP_TOKEN` repository secret with push (Contents:Read+Write) access to the tap repo. **The step hard-fails the release if the secret is missing** — a green release with a stale tap would silently leave `brew install brentguistwite/tap/springfield` on the previous version, so the workflow turns the release red instead and asks the operator to set the secret and re-run.

The checked-in [`Formula/springfield.rb`](../Formula/springfield.rb) file is only a template/reference copy. The release asset and the tap copy are the installable ones with real URLs and checksums.

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

### 2026-05 — Manual CLI install, plugin no longer ships a SessionStart binary fetch

The plugin previously shipped a `SessionStart` hook that downloaded and checksum-verified the matching CLI binary on every Claude Code session. That path was Claude-only (Codex plugins cannot ship a `bin/` or PATH-injecting hook), so it was removed in favor of a symmetric manual install: `brew install brentguistwite/tap/springfield` (macOS) or a release tarball on `PATH` (Linux/Windows). `hooks/checksums.txt` and `hooks/session-start.sh` are gone; `hooks/hooks.json` now carries only a `PreToolUse` guard that blocks interactive writes to `.springfield/`. The skills run a `springfield version` floor check (`skills.MinCLIVersion`) and surface the exact `brew install/upgrade` command when the CLI is missing or too old.

**Existing-user cleanup (optional).** Earlier versions of `springfield init` appended a control-plane guardrail block bracketed by a `<!-- springfield:guardrail -->` marker to your project's `AGENTS.md` / `CLAUDE.md` / `GEMINI.md`. That block is now dead text — the guards have moved to the plugin's `PreToolUse` hook and the batch prompt header. The CLI does **not** rewrite your files (Issue 3's whole point is zero mutation of your agent-instruction files), so the stale block stays put until you remove it. Search your project for `<!-- springfield:guardrail -->` and delete from that marker through the end of the `## Springfield control plane` section if you want a clean file.

**Plugin PreToolUse hook PATH caveat.** The plugin's `hooks/hooks.json` invokes `springfield hook-guard` by bare command name, so resolution depends on the PATH the Claude Code process sees. From a terminal-launched `claude`, the brew install path (`/opt/homebrew/bin` or `/usr/local/bin`) is on PATH and the guard fires. macOS GUI Claude Code launches do **not** always inherit a login shell's PATH; if `springfield` isn't resolvable there, the hook exits 127 and Claude treats the action as allowed (soft-fail). The interactive-Codex case is already accepted as a soft guard by design, and batch runs are unaffected (the runtime adapter resolves the binary via `os.Executable()`). If you rely on the interactive Claude guard, make sure `springfield` is on the PATH visible to your launch context (e.g. add a `launchctl setenv PATH` entry, or symlink `/usr/local/bin/springfield -> $(brew --prefix)/bin/springfield`).
