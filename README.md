# Springfield

Local-first CLI and TUI for the unified Ralph product surface.

## Install

### Homebrew (macOS / Linux)

```bash
brew tap brentguistwite/springfield https://github.com/brentguistwite/springfield
brew install springfield
```

### Direct binary

Download the binary for your platform from the
[latest release](https://github.com/brentguistwite/springfield/releases/latest),
then move it onto your `PATH`:

```bash
chmod +x springfield-*
sudo mv springfield-* /usr/local/bin/springfield
```

### Build from source

```bash
go install springfield@latest
```

## Upgrade

```bash
# Homebrew
brew upgrade springfield

# Direct binary — download the new release and replace the old binary.
```

## First run

Running bare `springfield` opens the TUI shell with guided setup flows:

```
springfield          # opens the interactive TUI
springfield init     # (coming soon) scaffold a new workspace
springfield doctor   # check environment health
springfield ralph    # Ralph workflow commands
springfield conductor # Conductor orchestration commands
springfield version  # print version
```

## Platforms

macOS (arm64, amd64) is the primary target.
Linux (arm64, amd64) binaries are built in CI and functional but less tested.

## License

MIT
