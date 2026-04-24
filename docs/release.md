# Springfield Release Workflow

Springfield release polish lives around one invariant: tagged releases publish installable artifacts for the thin Springfield CLI without any hand-edited version files.

## Preflight

Run these before cutting a tag:

```bash
go test ./...
go build .
```

Plugin metadata is release-critical. Do not cut a tag with pending changes in:

- [`.claude-plugin/plugin.json`](../.claude-plugin/plugin.json)
- [`.claude-plugin/marketplace.json`](../.claude-plugin/marketplace.json)
- [`.codex-plugin/plugin.json`](../.codex-plugin/plugin.json)
- [`hooks/checksums.txt`](../hooks/checksums.txt)

Those manifests and marketplace records must describe Springfield, stay version-aligned, and keep the checked-in `skills/plan`, `skills/start`, `skills/status`, and `skills/recover` inventory intact.

**Bump `.claude-plugin/plugin.json`, `.claude-plugin/marketplace.json` (springfield plugin entry), and `.codex-plugin/plugin.json` `version` to match the upcoming tag before pushing.** The release workflow sets `SPRINGFIELD_RELEASE_TAG=${{ github.ref_name }}` on the `Validate plugin metadata` step, which runs `TestPluginJSONVersionMatchesTagEnv` and fails the release if the manifest version disagrees with the tag. `go test ./...` also enforces Codex/Claude plugin version parity. Version bump is what triggers the SessionStart hook to fetch a new CLI binary after teammates run `/plugin update`.

`hooks/checksums.txt` is now release-critical too. The SessionStart hook trusts the plugin-shipped checksum manifest, not a runtime-fetched proof file from the release page. Each line stays keyed by the archive asset name (`./springfield_<version>_<os>_<arch>.tar.gz`), but the hash value is the SHA256 of the extracted `springfield` binary inside that archive. Before tagging, rebuild the four release archives for the target version and refresh the committed manifest:

```bash
version="$(awk -F'\"' '/\"version\"[[:space:]]*:/ { print $4; exit }' .claude-plugin/plugin.json)"
rm -rf dist
mkdir -p dist
sha_file() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print $1}'
  else
    shasum -a 256 "$1" | awk '{print $1}'
  fi
}
: > hooks/checksums.txt

for target in darwin/amd64 darwin/arm64 linux/amd64 linux/arm64; do
  GOOS="${target%/*}"
  GOARCH="${target#*/}"
  stage="$(mktemp -d)"
  CGO_ENABLED=0 GOOS="${GOOS}" GOARCH="${GOARCH}" \
    go build -trimpath -ldflags="-s -w -X springfield/cmd.Version=v${version}" -o "${stage}/springfield" .
  tar -C "${stage}" -czf "dist/springfield_${version}_${GOOS}_${GOARCH}.tar.gz" springfield
  printf "%s  ./springfield_%s_%s_%s.tar.gz\n" "$(sha_file "${stage}/springfield")" "${version}" "${GOOS}" "${GOARCH}" >> hooks/checksums.txt
  rm -rf "${stage}"
done

rm -rf dist
```

## Cut A Release

Push a semantic tag:

```bash
git tag v0.1.0
git push origin v0.1.0
```

That tag triggers [`.github/workflows/release.yml`](../.github/workflows/release.yml).

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
