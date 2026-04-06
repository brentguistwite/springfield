# Springfield Release Workflow

Springfield release polish lives around one invariant: tagged releases publish installable artifacts without any hand-edited version files.

## Preflight

Run these before cutting a tag:

```bash
go test ./...
go build .
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
- `checksums.txt`
- `springfield.rb`

Each archive contains a single `springfield` binary built with `cmd.Version` set from the Git tag.

## Homebrew

`springfield.rb` is rendered during the release from the computed archive URLs and SHA256 values. Install it straight from the release assets:

```bash
brew install --formula https://github.com/<owner>/<repo>/releases/download/v0.1.0/springfield.rb
```

The checked-in [`Formula/springfield.rb`](../Formula/springfield.rb) file is only a template/reference copy. The release asset is the installable one with real URLs and checksums.
