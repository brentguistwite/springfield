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

# fallback_symlink only reuses the EXACT pinned version from cache. If the
# pinned version isn't cached, REMOVE any pre-existing symlink so the user
# doesn't silently keep running a stale CLI version under a newer plugin.
# Visible breakage (`springfield: command not found`) beats invisible version
# skew (new slash commands driving an old CLI with different flags/schema).
fallback_symlink() {
  local dest="$1"
  local version="$2"
  local exact="$HOME/.cache/springfield/$version/springfield"
  if [[ -x "$exact" ]]; then
    ln -sfn "$exact" "$dest"
    echo "springfield: using cached v$version (fetch failed)" >&2
    return 0
  fi
  if [[ -e "$dest" || -L "$dest" ]]; then
    rm -f "$dest"
    echo "springfield: removed stale CLI symlink — plugin pinned v$version but no matching binary is cached and fetch failed" >&2
  fi
  echo "springfield: install manually from https://github.com/brentguistwite/springfield/releases" >&2
}

# stat_mtime portably prints the epoch mtime of $1 (BSD stat on macOS vs
# GNU stat on Linux). Echoes 0 if stat fails (treat as ancient → reapable).
stat_mtime() {
  stat -f %m "$1" 2>/dev/null || stat -c %Y "$1" 2>/dev/null || echo 0
}

# acquire_install_lock serializes concurrent SessionStart invocations for the
# same version. mkdir is atomic across POSIX filesystems — the first caller
# wins the directory, subsequent callers wait. Stale-lock recovery:
#   1. On acquire, write $$ into $lock/pid so others can detect the holder.
#   2. On contention, read that pid; if `kill -0 $pid` fails (process gone),
#      reap the lock and retry. Covers SIGKILL, crash, reboot.
#   3. Belt-and-braces: if the lock dir's mtime is older than 2× the wait
#      budget (60s) and pid check couldn't reap it (missing pid file, cross-
#      host cache, etc.), reap by age instead of returning failure.
acquire_install_lock() {
  local version="$1"
  local locks_dir="$HOME/.cache/springfield/.locks"
  local lock="$locks_dir/$version"
  mkdir -p "$locks_dir"

  local waited=0
  local max_wait_tenths=300  # 30 seconds
  local stale_age_seconds=60
  while ! mkdir "$lock" 2>/dev/null; do
    # Fast path re-check: maybe another process just finished.
    if [[ -x "$HOME/.cache/springfield/$version/springfield" ]]; then
      return 2
    fi
    # Pid-based stale reap: owning process gone?
    if [[ -f "$lock/pid" ]]; then
      local holder
      holder="$(cat "$lock/pid" 2>/dev/null || true)"
      if [[ -n "$holder" ]] && ! kill -0 "$holder" 2>/dev/null; then
        rm -rf "$lock"
        continue
      fi
    fi
    # Age-based stale reap for locks with no readable pid.
    local now age
    now=$(date +%s)
    age=$(( now - $(stat_mtime "$lock") ))
    if (( age > stale_age_seconds )); then
      rm -rf "$lock"
      continue
    fi
    sleep 0.1
    waited=$((waited + 1))
    if (( waited >= max_wait_tenths )); then
      # Last-chance reap before giving up.
      rm -rf "$lock" 2>/dev/null || true
      if mkdir "$lock" 2>/dev/null; then
        break
      fi
      echo "springfield: install lock for v$version held too long, skipping" >&2
      return 1
    fi
  done
  printf '%d\n' "$$" > "$lock/pid"
  export _SPRINGFIELD_LOCK="$lock"
  trap 'release_install_lock' EXIT INT TERM
  return 0
}

release_install_lock() {
  rm -rf "${_SPRINGFIELD_TMP:-}" 2>/dev/null || true
  unset _SPRINGFIELD_TMP
  if [[ -n "${_SPRINGFIELD_LOCK:-}" ]]; then
    rmdir "$_SPRINGFIELD_LOCK" 2>/dev/null || true
    unset _SPRINGFIELD_LOCK
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

  # Fast path: pinned-version binary already cached + symlink correct
  if [[ -x "$bin" && -L "$dest" && "$(readlink "$dest")" == "$bin" ]]; then
    exit 0
  fi

  # Acquire per-version install lock. If another session finishes first while
  # we wait, acquire returns 2 → binary is ready, skip straight to symlink.
  local lock_rc=0
  acquire_install_lock "$version" || lock_rc=$?
  if (( lock_rc == 2 )); then
    ln -sfn "$bin" "$dest"
    exit 0
  fi
  if (( lock_rc != 0 )); then
    # Lock timeout — best effort fallback
    fallback_symlink "$dest" "$version"
    exit 0
  fi

  # Re-check fast path inside the lock (another process may have published
  # between our initial check and lock acquisition)
  if [[ -x "$bin" ]]; then
    ln -sfn "$bin" "$dest"
    exit 0
  fi

  # Cache miss + lock held — fetch release asset
  local repo="brentguistwite/springfield"
  local base="https://github.com/$repo/releases/download/v$version"
  local asset="springfield_${version}_${os}_${arch}.tar.gz"
  local asset_url="$base/$asset"
  local sums_url="$base/checksums.txt"

  local tmp
  tmp="$(mktemp -d)"
  export _SPRINGFIELD_TMP="$tmp"

  if ! curl -fsSL -o "$tmp/$asset" "$asset_url"; then
    echo "springfield: failed to download $asset_url" >&2
    fallback_symlink "$dest" "$version"
    exit 0
  fi
  if ! curl -fsSL "$sums_url" > "$tmp/checksums.txt"; then
    echo "springfield: failed to download checksums.txt" >&2
    fallback_symlink "$dest" "$version"
    exit 0
  fi

  local expected
  expected="$(awk -v a="$asset" '$2 == a || $2 == "./"a { print $1; exit }' "$tmp/checksums.txt")"
  if [[ -z "$expected" ]]; then
    echo "springfield: checksum entry missing for $asset" >&2
    fallback_symlink "$dest" "$version"
    exit 0
  fi
  local got_sum
  got_sum="$(sha256 "$tmp/$asset")"
  if [[ "$got_sum" != "$expected" ]]; then
    echo "springfield: checksum mismatch for $asset ($got_sum != $expected)" >&2
    fallback_symlink "$dest" "$version"
    exit 0
  fi

  # Extract to staging inside cache_dir, then atomic-rename into place.
  # rename(2) within the same filesystem is atomic on POSIX, which prevents
  # a concurrent reader from observing a partially-written binary.
  mkdir -p "$cache_dir"
  local staging="$cache_dir/.staging.$$"
  rm -rf "$staging"
  mkdir -p "$staging"
  tar -C "$staging" -xzf "$tmp/$asset" springfield
  chmod +x "$staging/springfield"
  mv "$staging/springfield" "$bin"
  rmdir "$staging" 2>/dev/null || true

  ln -sfn "$bin" "$dest"

  case ":$PATH:" in
    *":$HOME/.local/bin:"*) ;;
    *) echo "springfield: add '$HOME/.local/bin' to PATH to use 'springfield' directly" >&2 ;;
  esac
}

main "$@"
