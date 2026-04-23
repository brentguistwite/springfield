#!/usr/bin/env bash
set -euo pipefail

PLUGIN_ROOT="${CLAUDE_PLUGIN_ROOT:-}"
if [[ -z "$PLUGIN_ROOT" ]]; then
  echo "springfield: CLAUDE_PLUGIN_ROOT not set" >&2
  exit 0
fi

# Bound network calls so a stalled GitHub/DNS/proxy cannot lock the user out
# at SessionStart. SESSION_START fires synchronously before any interactive
# command; blocking for minutes on curl is worse than failing fast and
# falling back. Env-overridable for tests.
SPRINGFIELD_CURL_CONNECT_TIMEOUT="${SPRINGFIELD_CURL_CONNECT_TIMEOUT:-5}"
SPRINGFIELD_CURL_MAX_TIME="${SPRINGFIELD_CURL_MAX_TIME:-30}"
_SPRINGFIELD_LOCK=""
_SPRINGFIELD_TMP=""

curl_bounded() {
  curl --fail --silent --show-error --location \
    --connect-timeout "$SPRINGFIELD_CURL_CONNECT_TIMEOUT" \
    --max-time "$SPRINGFIELD_CURL_MAX_TIME" \
    "$@"
}

sha256() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print $1}'
  elif command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$1" | awk '{print $1}'
  else
    echo "springfield: no sha256 tool available" >&2
    return 1
  fi
}

cached_binary_ready() {
  local bin="$1"
  [[ -f "$bin" && -x "$bin" ]]
}

make_temp_dir() {
  local tmp
  if ! tmp="$(mktemp -d 2>/dev/null)"; then
    return 1
  fi
  printf '%s\n' "$tmp"
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
  local manifest="$PLUGIN_ROOT/.claude-plugin/plugin.json"
  if [[ ! -f "$manifest" ]]; then
    return 0
  fi
  awk -F'"' '/"version"[[:space:]]*:/ { print $4; exit }' "$manifest" 2>/dev/null
}

# fallback_symlink resolves the "fetch failed or binary missing" path with a
# tradeoff between two bad outcomes:
#   A) silently keep running an older CLI under a newer plugin (version skew)
#   B) remove the last-known-good CLI and break all commands (command-not-found)
# Plugin version bumps can be visible before the matching release assets land
# (pushing plugin without tagging, GitHub outage mid-release, replica lag, CI
# still running). In those windows option B would brick every teammate who
# updates, so we choose option A — keep the existing symlink — but emit a
# loud, repeatable VERSION MISMATCH warning on every session until it
# resolves. Visible over silent; keep-working over brick.
fallback_symlink() {
  local dest="$1"
  local version="$2"
  local exact="$HOME/.cache/springfield/$version/springfield"
  if cached_binary_ready "$exact"; then
    safe_symlink "$exact" "$dest" || true
    echo "springfield: using cached v$version (fetch failed)" >&2
    return 0
  fi
  if [[ -L "$dest" || -e "$dest" ]]; then
    local current
    current="$(readlink "$dest" 2>/dev/null || echo "$dest")"
    echo "springfield: VERSION MISMATCH — plugin pinned v$version but no matching binary is cached and fetch failed; keeping existing $current on PATH. Run \`springfield version\` to inspect; install v$version manually from https://github.com/brentguistwite/springfield/releases if skew persists." >&2
    return 0
  fi
  echo "springfield: no cached binary for v$version and fetch failed; install manually from https://github.com/brentguistwite/springfield/releases" >&2
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
  local bin="$HOME/.cache/springfield/$version/springfield"
  if ! mkdir -p "$locks_dir" 2>/dev/null; then
    return 1
  fi

  local waited=0
  local max_wait_tenths=300  # 30 seconds
  local stale_age_seconds=60
  while ! mkdir "$lock" 2>/dev/null; do
    # Fast path re-check: maybe another process just finished.
    if cached_binary_ready "$bin"; then
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
  _SPRINGFIELD_LOCK="$lock"
  trap 'release_install_lock' EXIT INT TERM
  if ! printf '%d\n' "$$" > "$lock/pid"; then
    echo "springfield: failed to write install lock for v$version" >&2
    release_install_lock
    return 1
  fi
  return 0
}

install_binary() {
  local cache_dir="$1"
  local bin="$2"
  local archive="$3"

  if ! mkdir -p "$cache_dir" 2>/dev/null; then
    return 1
  fi
  local staging="$cache_dir/.staging.$$"
  rm -rf "$staging" 2>/dev/null || true
  if ! mkdir -p "$staging" 2>/dev/null; then
    return 1
  fi
  if ! tar -C "$staging" -xzf "$archive" springfield 2>/dev/null; then
    rm -rf "$staging" 2>/dev/null || true
    return 1
  fi
  if [[ ! -f "$staging/springfield" ]]; then
    rm -rf "$staging" 2>/dev/null || true
    return 1
  fi
  if ! chmod +x "$staging/springfield" 2>/dev/null; then
    rm -rf "$staging" 2>/dev/null || true
    return 1
  fi
  if [[ -e "$bin" && ! -f "$bin" ]]; then
    rm -rf "$staging" 2>/dev/null || true
    return 1
  fi
  # Atomic rename within same filesystem. On failure (ENOSPC mid-extract,
  # cross-device rename, etc), bail without leaving a half-written $bin.
  if ! mv "$staging/springfield" "$bin" 2>/dev/null; then
    rm -rf "$staging" 2>/dev/null || true
    return 1
  fi
  rmdir "$staging" 2>/dev/null || true
  return 0
}

release_install_lock() {
  rm -rf "${_SPRINGFIELD_TMP:-}" 2>/dev/null || true
  _SPRINGFIELD_TMP=""
  if [[ -n "${_SPRINGFIELD_LOCK:-}" ]]; then
    # rm -rf (not rmdir) because the lock dir contains a `pid` sentinel.
    # rmdir fails on non-empty dirs → would leak the lock forever, forcing
    # later sessions through the stale-reap heuristics for no good reason.
    rm -rf "$_SPRINGFIELD_LOCK" 2>/dev/null || true
    _SPRINGFIELD_LOCK=""
  fi
}

# safe_symlink swaps the public link atomically via a temporary sibling symlink
# + rename. This avoids the `ln -sfn` unlink/recreate gap, and behaves
# consistently on macOS/Linux when $link already exists as a file or symlink.
# A real directory at $link is treated as a collision and left untouched.
safe_symlink() {
  local target="$1"
  local link="$2"
  local link_dir tmp_link
  link_dir="$(dirname "$link")"
  tmp_link="$link_dir/.springfield-link.$$"
  if [[ -d "$link" && ! -L "$link" ]]; then
    echo "springfield: failed to update $link -> $target (permission? directory collision?)" >&2
    return 1
  fi
  rm -f "$tmp_link" 2>/dev/null || true
  if ! ln -s "$target" "$tmp_link" 2>/dev/null; then
    echo "springfield: failed to update $link -> $target (permission? directory collision?)" >&2
    return 1
  fi
  if mv -f "$tmp_link" "$link" 2>/dev/null; then
    return 0
  fi
  rm -f "$tmp_link" 2>/dev/null || true
  echo "springfield: failed to update $link -> $target (permission? directory collision?)" >&2
  return 1
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
  if ! mkdir -p "$HOME/.local/bin" 2>/dev/null; then
    echo "springfield: cannot create $HOME/.local/bin (permission?)" >&2
    exit 0
  fi

  # Fast path: pinned-version binary already cached + symlink correct
  if cached_binary_ready "$bin" && [[ -L "$dest" && "$(readlink "$dest")" == "$bin" ]]; then
    exit 0
  fi

  # Acquire per-version install lock. If another session finishes first while
  # we wait, acquire returns 2 → binary is ready, skip straight to symlink.
  local lock_rc=0
  acquire_install_lock "$version" || lock_rc=$?
  if (( lock_rc == 2 )); then
    safe_symlink "$bin" "$dest" || true
    exit 0
  fi
  if (( lock_rc != 0 )); then
    # Lock timeout — best effort fallback
    fallback_symlink "$dest" "$version"
    exit 0
  fi

  # Re-check fast path inside the lock (another process may have published
  # between our initial check and lock acquisition)
  if cached_binary_ready "$bin"; then
    safe_symlink "$bin" "$dest" || true
    exit 0
  fi

  # Cache miss + lock held — fetch release asset
  local repo="brentguistwite/springfield"
  local base="https://github.com/$repo/releases/download/v$version"
  local asset="springfield_${version}_${os}_${arch}.tar.gz"
  local asset_url="$base/$asset"
  local sums_url="$base/checksums.txt"

  local tmp
  if ! tmp="$(make_temp_dir)"; then
    echo "springfield: failed to create temp dir; falling back" >&2
    fallback_symlink "$dest" "$version"
    exit 0
  fi
  _SPRINGFIELD_TMP="$tmp"

  if ! curl_bounded -o "$tmp/$asset" "$asset_url"; then
    echo "springfield: failed to download $asset_url (timeout or error)" >&2
    fallback_symlink "$dest" "$version"
    exit 0
  fi
  if ! curl_bounded "$sums_url" > "$tmp/checksums.txt"; then
    echo "springfield: failed to download checksums.txt (timeout or error)" >&2
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
  if ! got_sum="$(sha256 "$tmp/$asset")"; then
    fallback_symlink "$dest" "$version"
    exit 0
  fi
  if [[ "$got_sum" != "$expected" ]]; then
    echo "springfield: checksum mismatch for $asset ($got_sum != $expected)" >&2
    fallback_symlink "$dest" "$version"
    exit 0
  fi

  # Extract to staging inside cache_dir, then atomic-rename into place.
  # rename(2) within the same filesystem is atomic on POSIX, preventing a
  # concurrent reader from observing a partially-written binary. Every I/O
  # step returns an error code instead of crashing via `set -e` so we can
  # route install failures (disk full, permission denied, corrupt tarball)
  # through fallback_symlink rather than erroring the session.
  if ! install_binary "$cache_dir" "$bin" "$tmp/$asset"; then
    echo "springfield: install failed (disk, permissions, or corrupt archive); falling back" >&2
    fallback_symlink "$dest" "$version"
    exit 0
  fi

  safe_symlink "$bin" "$dest" || exit 0

  case ":$PATH:" in
    *":$HOME/.local/bin:"*) ;;
    *) echo "springfield: add '$HOME/.local/bin' to PATH to use 'springfield' directly" >&2 ;;
  esac
}

main "$@"
