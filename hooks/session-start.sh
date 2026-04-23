#!/usr/bin/env bash
set -euo pipefail

PLUGIN_ROOT="${CLAUDE_PLUGIN_ROOT:?CLAUDE_PLUGIN_ROOT not set}"

sha256() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print $1}'
  else
    shasum -a 256 "$1" | awk '{print $1}'
  fi
}

detect_platform() {
  local os arch
  os="$(uname -s | tr '[:upper:]' '[:lower:]')"
  arch="$(uname -m)"
  case "$arch" in
    x86_64)        arch=amd64 ;;
    aarch64|arm64) arch=arm64 ;;
    *) echo "springfield: unsupported arch $arch" >&2; return 1 ;;
  esac
  case "$os" in
    darwin|linux) ;;
    *) echo "springfield: unsupported os $os" >&2; return 1 ;;
  esac
  printf '%s %s\n' "$os" "$arch"
}

plugin_version() {
  awk -F'"' '/"version"[[:space:]]*:/ { print $4; exit }' \
    "$PLUGIN_ROOT/.claude-plugin/plugin.json"
}

fallback_symlink() {
  local dest="$1"
  local latest=""
  if [[ -d "$HOME/.cache/springfield" ]]; then
    latest="$(ls -1 "$HOME/.cache/springfield" 2>/dev/null | sort -V | tail -n 1 || true)"
  fi
  if [[ -n "$latest" && -x "$HOME/.cache/springfield/$latest/springfield" ]]; then
    ln -sfn "$HOME/.cache/springfield/$latest/springfield" "$dest"
    echo "springfield: using cached $latest (fetch failed)" >&2
  else
    echo "springfield: no cached binary available and fetch failed; install manually from https://github.com/brentguistwite/springfield/releases" >&2
  fi
}

main() {
  local os arch version cache_dir bin dest
  if ! read -r os arch < <(detect_platform); then
    exit 0
  fi
  version="$(plugin_version)"
  if [[ -z "$version" ]]; then
    echo "springfield: could not read plugin version" >&2
    exit 0
  fi

  cache_dir="$HOME/.cache/springfield/$version"
  bin="$cache_dir/springfield"
  dest="$HOME/.local/bin/springfield"
  mkdir -p "$HOME/.local/bin"

  if [[ -x "$bin" && -L "$dest" && "$(readlink "$dest")" == "$bin" ]]; then
    exit 0
  fi

  local repo="brentguistwite/springfield"
  local base="https://github.com/$repo/releases/download/v$version"
  local asset="springfield_${version}_${os}_${arch}.tar.gz"
  local asset_url="$base/$asset"
  local sums_url="$base/checksums.txt"

  mkdir -p "$cache_dir"
  local tmp
  tmp="$(mktemp -d)"
  # Export so the EXIT trap can see it after main()'s local scope unwinds.
  export _SPRINGFIELD_TMP="$tmp"
  trap 'rm -rf "${_SPRINGFIELD_TMP:-}"' EXIT

  if ! curl -fsSL -o "$tmp/$asset" "$asset_url"; then
    echo "springfield: failed to download $asset_url" >&2
    fallback_symlink "$dest"
    exit 0
  fi
  if ! curl -fsSL "$sums_url" > "$tmp/checksums.txt"; then
    echo "springfield: failed to download checksums.txt" >&2
    fallback_symlink "$dest"
    exit 0
  fi

  local expected
  expected="$(awk -v a="$asset" '$2 == a || $2 == "./"a { print $1; exit }' "$tmp/checksums.txt")"
  if [[ -z "$expected" ]]; then
    echo "springfield: checksum entry missing for $asset" >&2
    fallback_symlink "$dest"
    exit 0
  fi
  local got_sum
  got_sum="$(sha256 "$tmp/$asset")"
  if [[ "$got_sum" != "$expected" ]]; then
    echo "springfield: checksum mismatch for $asset ($got_sum != $expected)" >&2
    fallback_symlink "$dest"
    exit 0
  fi

  tar -C "$cache_dir" -xzf "$tmp/$asset" springfield
  chmod +x "$bin"
  ln -sfn "$bin" "$dest"

  case ":$PATH:" in
    *":$HOME/.local/bin:"*) ;;
    *) echo "springfield: add '$HOME/.local/bin' to PATH to use 'springfield' directly" >&2 ;;
  esac
}

main "$@"
