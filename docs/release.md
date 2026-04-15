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

Those manifests and marketplace records must describe Springfield, stay version-aligned, and keep the checked-in `skills/start`, `skills/status`, and `skills/recover` inventory intact.

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
- `checksums.txt`
- `springfield.rb`

Each archive contains a single `springfield` binary built with `cmd.Version` set from the Git tag. Before packaging, the workflow runs plugin metadata validation so manifest drift fails before release creation.

## Homebrew

`springfield.rb` is rendered during the release from the computed archive URLs and SHA256 values. Keep the generated copy plugin-first: if the release formula wording drifts back to stale TUI-era text, treat that as a release blocker. Install it straight from the release assets:

```bash
brew install --formula https://github.com/<owner>/<repo>/releases/download/v0.1.0/springfield.rb
```

The checked-in [`Formula/springfield.rb`](../Formula/springfield.rb) file is only a template/reference copy. The release asset is the installable one with real URLs and checksums.
