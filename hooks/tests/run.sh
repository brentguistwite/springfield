#!/usr/bin/env bash
set -euo pipefail
fail=0

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
hook="$script_dir/../session-start.sh"

install_plugin_checksums() {
  local plugin_root="$1"
  local src="$2"
  mkdir -p "$plugin_root/hooks"
  cp "$src" "$plugin_root/hooks/checksums.txt"
}

sha256_file() {
  local path="$1"
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$path" | awk '{print $1}'
  else
    shasum -a 256 "$path" | awk '{print $1}'
  fi
}

write_checksum_entry() {
  local manifest_path="$1"
  local version="$2"
  local os="$3"
  local arch="$4"
  local file_path="$5"
  printf '%s  ./springfield_%s_%s_%s.tar.gz\n' "$(sha256_file "$file_path")" "$version" "$os" "$arch" \
    > "$manifest_path"
}

write_plugin_checksum_entry() {
  local plugin_root="$1"
  local version="$2"
  local os="$3"
  local arch="$4"
  local sum="$5"
  mkdir -p "$plugin_root/hooks"
  printf '%s  ./springfield_%s_%s_%s.tar.gz\n' "$sum" "$version" "$os" "$arch" \
    > "$plugin_root/hooks/checksums.txt"
}

# ---------- Test 1: cache hit + correct symlink → exit 0, no network ----------
tmp="$(mktemp -d)"
export HOME="$tmp"
export CLAUDE_PLUGIN_ROOT="$tmp/plugin"
mkdir -p "$tmp/plugin/hooks" "$tmp/plugin/.claude-plugin" "$tmp/.local/bin" "$tmp/.cache/springfield/1.2.3"
printf '{"version":"1.2.3"}\n' > "$tmp/plugin/.claude-plugin/plugin.json"
printf '#!/bin/sh\necho fake\n' > "$tmp/.cache/springfield/1.2.3/springfield"
chmod +x "$tmp/.cache/springfield/1.2.3/springfield"
ln -sfn "$tmp/.cache/springfield/1.2.3/springfield" "$tmp/.local/bin/springfield"

# Stub curl to guarantee no network
mkdir -p "$tmp/bin"
cat > "$tmp/bin/curl" <<'STUB'
#!/bin/sh
echo "curl called, fast path broken" >&2
exit 99
STUB
chmod +x "$tmp/bin/curl"

if ! PATH="$tmp/bin:$PATH" bash "$hook"; then
  echo "FAIL test1: fast path exited non-zero" >&2
  fail=1
fi

got="$(readlink "$tmp/.local/bin/springfield")"
want="$tmp/.cache/springfield/1.2.3/springfield"
if [[ "$got" != "$want" ]]; then
  echo "FAIL test1: symlink drift: $got != $want" >&2
  fail=1
fi
rm -rf "$tmp"

# ---------- Test 2: cache miss → fetch, verify, extract, symlink ----------
tmp2="$(mktemp -d)"
export HOME="$tmp2"
export CLAUDE_PLUGIN_ROOT="$tmp2/plugin"
mkdir -p "$tmp2/plugin/.claude-plugin" "$tmp2/bin" "$tmp2/fake-release"
printf '{"version":"9.9.9"}\n' > "$tmp2/plugin/.claude-plugin/plugin.json"

os="$(uname -s | tr '[:upper:]' '[:lower:]')"
arch="$(uname -m)"
case "$arch" in x86_64) arch=amd64;; aarch64|arm64) arch=arm64;; esac

# Build fake release tarball
stage="$(mktemp -d)"
printf '#!/bin/sh\necho v9.9.9\n' > "$stage/springfield"
chmod +x "$stage/springfield"
tarball="$tmp2/fake-release/springfield_9.9.9_${os}_${arch}.tar.gz"
tar -C "$stage" -czf "$tarball" springfield

write_checksum_entry "$tmp2/fake-release/checksums.txt" "9.9.9" "$os" "$arch" "$stage/springfield"
install_plugin_checksums "$tmp2/plugin" "$tmp2/fake-release/checksums.txt"

# Stub curl to serve from local filesystem by URL suffix
cat > "$tmp2/bin/curl" <<STUB
#!/usr/bin/env bash
set -euo pipefail
url=""
out="-"
while [ \$# -gt 0 ]; do
  case "\$1" in
    -o) out="\$2"; shift 2 ;;
    -fsSL|-fL|-sL|-s|-L|-f) shift ;;
    http*) url="\$1"; shift ;;
    *) shift ;;
  esac
done
case "\$url" in
  *checksums.txt) src="$tmp2/fake-release/checksums.txt" ;;
  *springfield_9.9.9_${os}_${arch}.tar.gz) src="$tarball" ;;
  *) echo "unexpected url \$url" >&2; exit 22 ;;
esac
if [ "\$out" = "-" ]; then cat "\$src"; else cp "\$src" "\$out"; fi
STUB
chmod +x "$tmp2/bin/curl"
PATH="$tmp2/bin:$PATH" bash "$hook"

got="$(readlink "$tmp2/.local/bin/springfield" 2>/dev/null || true)"
want="$tmp2/.cache/springfield/9.9.9/springfield"
if [[ "$got" != "$want" ]]; then
  echo "FAIL test2: cache-miss did not produce symlink: got=$got want=$want" >&2
  fail=1
fi
if [[ ! -x "$want" ]]; then
  echo "FAIL test2: fetched binary not executable" >&2
  fail=1
fi
rm -rf "$tmp2"

# ---------- Test 3: fetch failure must NOT switch to a different cached version ----------
# Plugin pinned to v8.8.8, cache has v7.7.7 from prior install. Fetch 404s.
# Expected: fallback does NOT repoint to v7.7.7; symlink either stays unset or
# points at nothing (because the pinned version has no cached binary). Version
# drift is a release-safety violation — the plugin declares v8.8.8, the CLI
# must not silently become v7.7.7.
tmp3="$(mktemp -d)"
export HOME="$tmp3"
export CLAUDE_PLUGIN_ROOT="$tmp3/plugin"
mkdir -p "$tmp3/plugin/.claude-plugin" "$tmp3/bin" \
  "$tmp3/.cache/springfield/7.7.7" "$tmp3/.local/bin"
printf '{"version":"8.8.8"}\n' > "$tmp3/plugin/.claude-plugin/plugin.json"
printf '#!/bin/sh\necho v7.7.7\n' > "$tmp3/.cache/springfield/7.7.7/springfield"
chmod +x "$tmp3/.cache/springfield/7.7.7/springfield"
write_plugin_checksum_entry "$tmp3/plugin" "8.8.8" "$os" "$arch" \
  "0000000000000000000000000000000000000000000000000000000000000000"

# curl stub that 404s everything
cat > "$tmp3/bin/curl" <<'STUB'
#!/bin/sh
exit 22
STUB
chmod +x "$tmp3/bin/curl"

set +e
PATH="$tmp3/bin:$PATH" bash "$hook" >/dev/null 2>"$tmp3/err3.log"
rc3=$?
set -e
if (( rc3 != 0 )); then
  echo "FAIL test3: fetch failure crashed hook with rc=$rc3" >&2
  fail=1
fi
got="$(readlink "$tmp3/.local/bin/springfield" 2>/dev/null || true)"
if [[ "$got" == *"7.7.7"* ]]; then
  echo "FAIL test3: version drift — symlink repointed to stale v7.7.7 on fetch failure: $got" >&2
  fail=1
fi
rm -rf "$tmp3"

# ---------- Test 4: fetch failure with exact-version cache hit IS allowed ----------
# If plugin is pinned to v6.6.6 and cache already has v6.6.6, fetch can fail
# (offline, transient 5xx) and we still want the symlink to resolve to the
# correct pinned version's cached binary.
tmp4="$(mktemp -d)"
export HOME="$tmp4"
export CLAUDE_PLUGIN_ROOT="$tmp4/plugin"
mkdir -p "$tmp4/plugin/.claude-plugin" "$tmp4/bin" \
  "$tmp4/.cache/springfield/6.6.6" "$tmp4/.local/bin"
printf '{"version":"6.6.6"}\n' > "$tmp4/plugin/.claude-plugin/plugin.json"
printf '#!/bin/sh\necho v6.6.6\n' > "$tmp4/.cache/springfield/6.6.6/springfield"
chmod +x "$tmp4/.cache/springfield/6.6.6/springfield"

cat > "$tmp4/bin/curl" <<'STUB'
#!/bin/sh
exit 22
STUB
chmod +x "$tmp4/bin/curl"

set +e
PATH="$tmp4/bin:$PATH" bash "$hook" >/dev/null 2>"$tmp4/err4.log"
rc4=$?
set -e
if (( rc4 != 0 )); then
  echo "FAIL test4: exact-version fallback crashed hook with rc=$rc4" >&2
  fail=1
fi
got="$(readlink "$tmp4/.local/bin/springfield" 2>/dev/null || true)"
want="$tmp4/.cache/springfield/6.6.6/springfield"
if [[ "$got" != "$want" ]]; then
  echo "FAIL test4: exact-version cache should be used on fetch failure: got=$got want=$want" >&2
  fail=1
fi
rm -rf "$tmp4"

# ---------- Test 5: concurrent invocations do not corrupt the cache ----------
# Two hook processes race on the same version with an empty cache. Both fetch,
# both extract. Without locking + atomic rename they can trample each other's
# writes, leaving a partial or torn binary. With the fix, both converge on the
# same final binary and the symlink resolves to an intact executable.
tmp5="$(mktemp -d)"
export HOME="$tmp5"
export CLAUDE_PLUGIN_ROOT="$tmp5/plugin"
mkdir -p "$tmp5/plugin/.claude-plugin" "$tmp5/bin" "$tmp5/fake-release" \
  "$tmp5/.local/bin"
printf '{"version":"5.5.5"}\n' > "$tmp5/plugin/.claude-plugin/plugin.json"

os="$(uname -s | tr '[:upper:]' '[:lower:]')"
arch="$(uname -m)"
case "$arch" in x86_64) arch=amd64;; aarch64|arm64) arch=arm64;; esac

# Build a fake tarball with a large-ish payload so extraction is not instant.
stage5="$(mktemp -d)"
{
  printf '#!/bin/sh\n'
  # ~64KB of body so extraction actually spans a measurable window
  head -c 65536 /dev/urandom | base64
  printf 'echo v5.5.5\n'
} > "$stage5/springfield"
chmod +x "$stage5/springfield"
tarball5="$tmp5/fake-release/springfield_5.5.5_${os}_${arch}.tar.gz"
tar -C "$stage5" -czf "$tarball5" springfield

expected_bin_sum="$(sha256_file "$stage5/springfield")"
printf '%s  ./springfield_5.5.5_%s_%s.tar.gz\n' "$expected_bin_sum" "$os" "$arch" \
  > "$tmp5/fake-release/checksums.txt"
install_plugin_checksums "$tmp5/plugin" "$tmp5/fake-release/checksums.txt"

# curl stub with a small artificial delay so the race window is real
cat > "$tmp5/bin/curl" <<STUB
#!/usr/bin/env bash
set -euo pipefail
url=""
out="-"
while [ \$# -gt 0 ]; do
  case "\$1" in
    -o) out="\$2"; shift 2 ;;
    -fsSL|-fL|-sL|-s|-L|-f) shift ;;
    http*) url="\$1"; shift ;;
    *) shift ;;
  esac
done
case "\$url" in
  *checksums.txt) src="$tmp5/fake-release/checksums.txt" ;;
  *springfield_5.5.5_${os}_${arch}.tar.gz) src="$tarball5" ;;
  *) echo "unexpected url \$url" >&2; exit 22 ;;
esac
sleep 0.05
if [ "\$out" = "-" ]; then cat "\$src"; else cp "\$src" "\$out"; fi
STUB
chmod +x "$tmp5/bin/curl"

# Fire two concurrent invocations, wait for both
PATH="$tmp5/bin:$PATH" bash "$hook" > "$tmp5/out1.log" 2> "$tmp5/err1.log" &
pid1=$!
PATH="$tmp5/bin:$PATH" bash "$hook" > "$tmp5/out2.log" 2> "$tmp5/err2.log" &
pid2=$!
wait "$pid1" || { echo "FAIL test5: hook pid1 exited non-zero" >&2; fail=1; }
wait "$pid2" || { echo "FAIL test5: hook pid2 exited non-zero" >&2; fail=1; }

# Final binary must exist, be executable, and match the expected payload hash
final="$tmp5/.cache/springfield/5.5.5/springfield"
if [[ ! -x "$final" ]]; then
  echo "FAIL test5: final binary missing or not executable" >&2
  fail=1
else
  if command -v sha256sum >/dev/null 2>&1; then
    got_bin_sum="$(sha256sum "$final" | awk '{print $1}')"
  else
    got_bin_sum="$(shasum -a 256 "$final" | awk '{print $1}')"
  fi
  if [[ "$got_bin_sum" != "$expected_bin_sum" ]]; then
    echo "FAIL test5: concurrent extract corrupted binary: got=$got_bin_sum want=$expected_bin_sum" >&2
    fail=1
  fi
fi

# Symlink must resolve to the expected pinned version
got="$(readlink "$tmp5/.local/bin/springfield" 2>/dev/null || true)"
if [[ "$got" != "$final" ]]; then
  echo "FAIL test5: symlink not pointing at pinned version: got=$got want=$final" >&2
  fail=1
fi
rm -rf "$tmp5"

# ---------- Test 6: failed upgrade keeps existing symlink + warns loudly ----------
# User was on v4.4.4 (cached + symlinked). Plugin upgrades to v5.5.5. Fetch
# fails and v5.5.5 isn't cached (e.g. release assets haven't propagated yet
# after a plugin.json bump). The hook MUST keep v4.4.4 on PATH so commands
# still work during the rollout window, and MUST emit a VERSION MISMATCH
# warning so the user knows about the skew. Deleting the symlink would brick
# every teammate who updates before the release lands — worse than skew.
tmp6="$(mktemp -d)"
export HOME="$tmp6"
export CLAUDE_PLUGIN_ROOT="$tmp6/plugin"
mkdir -p "$tmp6/plugin/.claude-plugin" "$tmp6/bin" \
  "$tmp6/.cache/springfield/4.4.4" "$tmp6/.local/bin"
printf '{"version":"5.5.5"}\n' > "$tmp6/plugin/.claude-plugin/plugin.json"
printf '#!/bin/sh\necho v4.4.4\n' > "$tmp6/.cache/springfield/4.4.4/springfield"
chmod +x "$tmp6/.cache/springfield/4.4.4/springfield"
old_target="$tmp6/.cache/springfield/4.4.4/springfield"
ln -sfn "$old_target" "$tmp6/.local/bin/springfield"
write_plugin_checksum_entry "$tmp6/plugin" "5.5.5" "$os" "$arch" \
  "0000000000000000000000000000000000000000000000000000000000000000"

cat > "$tmp6/bin/curl" <<'STUB'
#!/bin/sh
exit 22
STUB
chmod +x "$tmp6/bin/curl"

set +e
PATH="$tmp6/bin:$PATH" bash "$hook" >/dev/null 2>"$tmp6/err6.log"
rc6=$?
set -e
err6="$(cat "$tmp6/err6.log" 2>/dev/null || true)"
if (( rc6 != 0 )); then
  echo "FAIL test6: failed-upgrade fallback crashed hook with rc=$rc6" >&2
  fail=1
fi
got="$(readlink "$tmp6/.local/bin/springfield" 2>/dev/null || true)"
if [[ "$got" != "$old_target" ]]; then
  echo "FAIL test6: existing symlink was removed or altered (got=$got want=$old_target)" >&2
  fail=1
fi
if [[ "$err6" != *"VERSION MISMATCH"* ]]; then
  echo "FAIL test6: missing VERSION MISMATCH warning in stderr: $err6" >&2
  fail=1
fi
rm -rf "$tmp6"

# ---------- Test 7: stale install lock from dead pid must be reapable ----------
# A prior run was SIGKILLed mid-install. The lock directory and its pid file
# remain; the pid points at a long-gone process. The hook must detect this,
# reap the lock, and install successfully — not spin for 30s and bail.
tmp7="$(mktemp -d)"
export HOME="$tmp7"
export CLAUDE_PLUGIN_ROOT="$tmp7/plugin"
mkdir -p "$tmp7/plugin/.claude-plugin" "$tmp7/bin" "$tmp7/fake-release" \
  "$tmp7/.local/bin" "$tmp7/.cache/springfield/.locks/3.3.3"
printf '{"version":"3.3.3"}\n' > "$tmp7/plugin/.claude-plugin/plugin.json"
# Use pid 999999 — virtually guaranteed to be dead. Portable `kill -0` will
# fail on a non-existent pid, triggering stale-lock reap.
printf '999999\n' > "$tmp7/.cache/springfield/.locks/3.3.3/pid"

os="$(uname -s | tr '[:upper:]' '[:lower:]')"
arch="$(uname -m)"
case "$arch" in x86_64) arch=amd64;; aarch64|arm64) arch=arm64;; esac

stage7="$(mktemp -d)"
printf '#!/bin/sh\necho v3.3.3\n' > "$stage7/springfield"
chmod +x "$stage7/springfield"
tarball7="$tmp7/fake-release/springfield_3.3.3_${os}_${arch}.tar.gz"
tar -C "$stage7" -czf "$tarball7" springfield

write_checksum_entry "$tmp7/fake-release/checksums.txt" "3.3.3" "$os" "$arch" "$stage7/springfield"
install_plugin_checksums "$tmp7/plugin" "$tmp7/fake-release/checksums.txt"

cat > "$tmp7/bin/curl" <<STUB
#!/usr/bin/env bash
set -euo pipefail
url=""
out="-"
while [ \$# -gt 0 ]; do
  case "\$1" in
    -o) out="\$2"; shift 2 ;;
    -fsSL|-fL|-sL|-s|-L|-f) shift ;;
    http*) url="\$1"; shift ;;
    *) shift ;;
  esac
done
case "\$url" in
  *checksums.txt) src="$tmp7/fake-release/checksums.txt" ;;
  *springfield_3.3.3_${os}_${arch}.tar.gz) src="$tarball7" ;;
  *) echo "unexpected url \$url" >&2; exit 22 ;;
esac
if [ "\$out" = "-" ]; then cat "\$src"; else cp "\$src" "\$out"; fi
STUB
chmod +x "$tmp7/bin/curl"

# Enforce a tight wall-clock budget so spinning on stale lock would fail this test.
t_start=$(date +%s)
PATH="$tmp7/bin:$PATH" bash "$hook" || { echo "FAIL test7: hook errored on stale-lock recovery" >&2; fail=1; }
t_end=$(date +%s)
if (( t_end - t_start > 5 )); then
  echo "FAIL test7: hook did not reap stale lock quickly (took $((t_end - t_start))s; expected <5s)" >&2
  fail=1
fi

got="$(readlink "$tmp7/.local/bin/springfield" 2>/dev/null || true)"
want="$tmp7/.cache/springfield/3.3.3/springfield"
if [[ "$got" != "$want" ]]; then
  echo "FAIL test7: install did not complete after stale-lock reap: got=$got want=$want" >&2
  fail=1
fi
rm -rf "$tmp7"

# ---------- Test 8: stalled connect must honor connect-timeout and fall back ----------
# DNS / proxy / TCP connect hangs. SessionStart runs synchronously before any
# interactive command, so an unbounded connect blocks the user out of Claude
# for minutes. The hook MUST pass --connect-timeout and treat a timeout as a
# fallback condition — not hang.
tmp8="$(mktemp -d)"
export HOME="$tmp8"
export CLAUDE_PLUGIN_ROOT="$tmp8/plugin"
mkdir -p "$tmp8/plugin/.claude-plugin" "$tmp8/bin" "$tmp8/.local/bin"
printf '{"version":"2.2.2"}\n' > "$tmp8/plugin/.claude-plugin/plugin.json"
write_plugin_checksum_entry "$tmp8/plugin" "2.2.2" "$os" "$arch" \
  "0000000000000000000000000000000000000000000000000000000000000000"

# Curl stub that honors connect-timeout first. If the hook stops passing
# --connect-timeout, this test stalls on the larger max-time budget instead.
cat > "$tmp8/bin/curl" <<'STUB'
#!/usr/bin/env bash
CONNECT_TIME=""
MAX_TIME=""
while [ $# -gt 0 ]; do
  case "$1" in
    --connect-timeout) CONNECT_TIME="$2"; shift 2 ;;
    --max-time) MAX_TIME="$2"; shift 2 ;;
    *) shift ;;
  esac
done
if [ -n "$CONNECT_TIME" ]; then
  sleep "$CONNECT_TIME"
  exit 28
fi
if [ -n "$MAX_TIME" ]; then
  sleep "$MAX_TIME"
  exit 28
fi
sleep 10
exit 28
STUB
chmod +x "$tmp8/bin/curl"

t_start=$(date +%s)
set +e
PATH="$tmp8/bin:$PATH" SPRINGFIELD_CURL_CONNECT_TIMEOUT=1 \
  SPRINGFIELD_CURL_MAX_TIME=9 bash "$hook" >/dev/null 2>"$tmp8/err8.log"
rc8=$?
set -e
err8="$(cat "$tmp8/err8.log" 2>/dev/null || true)"
t_end=$(date +%s)
elapsed=$((t_end - t_start))
if (( rc8 != 0 )); then
  echo "FAIL test8: connect-timeout fallback crashed hook with rc=$rc8" >&2
  fail=1
fi
if (( elapsed > 5 )); then
  echo "FAIL test8: hook did not bound connect stall: elapsed=${elapsed}s" >&2
  fail=1
fi
if [[ "$err8" != *"timeout or error"* ]]; then
  echo "FAIL test8: connect stall did not route through fallback path: $err8" >&2
  fail=1
fi
rm -rf "$tmp8"

# ---------- Test 8b: stalled transfer must honor max-time and fall back ----------
tmp8b="$(mktemp -d)"
export HOME="$tmp8b"
export CLAUDE_PLUGIN_ROOT="$tmp8b/plugin"
mkdir -p "$tmp8b/plugin/.claude-plugin" "$tmp8b/bin" "$tmp8b/.local/bin"
printf '{"version":"2.2.3"}\n' > "$tmp8b/plugin/.claude-plugin/plugin.json"
write_plugin_checksum_entry "$tmp8b/plugin" "2.2.3" "$os" "$arch" \
  "0000000000000000000000000000000000000000000000000000000000000000"

cat > "$tmp8b/bin/curl" <<'STUB'
#!/usr/bin/env bash
CONNECT_TIME=""
MAX_TIME=""
while [ $# -gt 0 ]; do
  case "$1" in
    --connect-timeout) CONNECT_TIME="$2"; shift 2 ;;
    --max-time) MAX_TIME="$2"; shift 2 ;;
    *) shift ;;
  esac
done
if [ -n "$MAX_TIME" ]; then
  sleep "$MAX_TIME"
  exit 28
fi
if [ -n "$CONNECT_TIME" ]; then
  sleep "$CONNECT_TIME"
  exit 28
fi
sleep 10
exit 28
STUB
chmod +x "$tmp8b/bin/curl"

t_start=$(date +%s)
set +e
PATH="$tmp8b/bin:$PATH" SPRINGFIELD_CURL_CONNECT_TIMEOUT=9 \
  SPRINGFIELD_CURL_MAX_TIME=2 bash "$hook" >/dev/null 2>"$tmp8b/err8b.log"
rc8b=$?
set -e
err8b="$(cat "$tmp8b/err8b.log" 2>/dev/null || true)"
t_end=$(date +%s)
elapsed=$((t_end - t_start))
if (( rc8b != 0 )); then
  echo "FAIL test8b: max-time fallback crashed hook with rc=$rc8b" >&2
  fail=1
fi
if (( elapsed > 8 )); then
  echo "FAIL test8b: hook did not bound transfer stall: elapsed=${elapsed}s" >&2
  fail=1
fi
if [[ "$err8b" != *"timeout or error"* ]]; then
  echo "FAIL test8b: transfer stall did not route through fallback path: $err8b" >&2
  fail=1
fi
rm -rf "$tmp8b"

# ---------- Test 9: checksum mismatch must fall back, not install ----------
# Release was tampered with / retransmission corrupted. The script MUST NOT
# install a binary whose extracted payload hash doesn't match the plugin-shipped
# manifest.
tmp9="$(mktemp -d)"
export HOME="$tmp9"
export CLAUDE_PLUGIN_ROOT="$tmp9/plugin"
mkdir -p "$tmp9/plugin/.claude-plugin" "$tmp9/bin" "$tmp9/fake-release" \
  "$tmp9/.local/bin"
printf '{"version":"2.9.9"}\n' > "$tmp9/plugin/.claude-plugin/plugin.json"

os="$(uname -s | tr '[:upper:]' '[:lower:]')"
arch="$(uname -m)"
case "$arch" in x86_64) arch=amd64;; aarch64|arm64) arch=arm64;; esac

stage9="$(mktemp -d)"
printf '#!/bin/sh\necho actual\n' > "$stage9/springfield"
chmod +x "$stage9/springfield"
tarball9="$tmp9/fake-release/springfield_2.9.9_${os}_${arch}.tar.gz"
tar -C "$stage9" -czf "$tarball9" springfield

# Deliberate wrong checksum
printf '%s  ./springfield_2.9.9_%s_%s.tar.gz\n' \
  '0000000000000000000000000000000000000000000000000000000000000000' "$os" "$arch" \
  > "$tmp9/fake-release/checksums.txt"
install_plugin_checksums "$tmp9/plugin" "$tmp9/fake-release/checksums.txt"

cat > "$tmp9/bin/curl" <<STUB
#!/usr/bin/env bash
url=""; out="-"
while [ \$# -gt 0 ]; do
  case "\$1" in
    -o) out="\$2"; shift 2 ;;
    --fail|--silent|--show-error|--location|-fsSL|-fL|-sL|-s|-L|-f) shift ;;
    --connect-timeout|--max-time) shift 2 ;;
    http*) url="\$1"; shift ;;
    *) shift ;;
  esac
done
case "\$url" in
  *checksums.txt) src="$tmp9/fake-release/checksums.txt" ;;
  *springfield_2.9.9_${os}_${arch}.tar.gz) src="$tarball9" ;;
  *) exit 22 ;;
esac
if [ "\$out" = "-" ]; then cat "\$src"; else cp "\$src" "\$out"; fi
STUB
chmod +x "$tmp9/bin/curl"

set +e
PATH="$tmp9/bin:$PATH" bash "$hook" >/dev/null 2>"$tmp9/err9.log"
rc9=$?
set -e
err9="$(cat "$tmp9/err9.log" 2>/dev/null || true)"
if (( rc9 != 0 )); then
  echo "FAIL test9: checksum-mismatch fallback crashed hook with rc=$rc9" >&2
  fail=1
fi
if [[ "$err9" != *"checksum mismatch"* ]]; then
  echo "FAIL test9: checksum mismatch not detected: $err9" >&2
  fail=1
fi
if [[ -e "$tmp9/.cache/springfield/2.9.9/springfield" ]]; then
  echo "FAIL test9: tampered binary was installed despite checksum failure" >&2
  fail=1
fi
rm -rf "$tmp9"

# ---------- Test 10: corrupt tarball fails install cleanly (fallback, no crash) ----------
# Download completes, checksum matches a corrupt file (we lie about the sum so
# the tar extract fails instead of the checksum check). The script MUST route
# through fallback_symlink, not crash the session with a non-zero exit from
# `set -e` on the `tar` command.
tmp10="$(mktemp -d)"
export HOME="$tmp10"
export CLAUDE_PLUGIN_ROOT="$tmp10/plugin"
mkdir -p "$tmp10/plugin/.claude-plugin" "$tmp10/bin" "$tmp10/fake-release" \
  "$tmp10/.local/bin"
printf '{"version":"1.1.1"}\n' > "$tmp10/plugin/.claude-plugin/plugin.json"

# "Tarball" is random garbage — tar -xzf will fail before checksum validation.
bad_tarball="$tmp10/fake-release/springfield_1.1.1_${os}_${arch}.tar.gz"
head -c 4096 /dev/urandom > "$bad_tarball"
bad_sum='0000000000000000000000000000000000000000000000000000000000000000'
printf '%s  ./springfield_1.1.1_%s_%s.tar.gz\n' "$bad_sum" "$os" "$arch" \
  > "$tmp10/fake-release/checksums.txt"
install_plugin_checksums "$tmp10/plugin" "$tmp10/fake-release/checksums.txt"

cat > "$tmp10/bin/curl" <<STUB
#!/usr/bin/env bash
url=""; out="-"
while [ \$# -gt 0 ]; do
  case "\$1" in
    -o) out="\$2"; shift 2 ;;
    --fail|--silent|--show-error|--location|-fsSL|-fL|-sL|-s|-L|-f) shift ;;
    --connect-timeout|--max-time) shift 2 ;;
    http*) url="\$1"; shift ;;
    *) shift ;;
  esac
done
case "\$url" in
  *checksums.txt) src="$tmp10/fake-release/checksums.txt" ;;
  *springfield_1.1.1_${os}_${arch}.tar.gz) src="$bad_tarball" ;;
  *) exit 22 ;;
esac
if [ "\$out" = "-" ]; then cat "\$src"; else cp "\$src" "\$out"; fi
STUB
chmod +x "$tmp10/bin/curl"

# Must exit 0 (non-crash) and must NOT have installed a binary
set +e
PATH="$tmp10/bin:$PATH" bash "$hook" >/dev/null 2>"$tmp10/err10.log"
rc10=$?
set -e
if (( rc10 != 0 )); then
  echo "FAIL test10: corrupt tarball crashed hook with rc=$rc10" >&2
  fail=1
fi
if [[ -e "$tmp10/.cache/springfield/1.1.1/springfield" ]]; then
  echo "FAIL test10: partial/bad binary ended up in cache" >&2
  fail=1
fi
if ! grep -q "install failed" "$tmp10/err10.log" 2>/dev/null; then
  echo "FAIL test10: corrupt tarball did not route through fallback: $(cat "$tmp10/err10.log")" >&2
  fail=1
fi
rm -rf "$tmp10"

# ---------- Test 11: concurrent installs of DIFFERENT versions don't block ----------
# Two sessions, one targeting v1 and one targeting v2, share the same HOME so
# they contend on the same .cache tree. They must still run independently
# because locks are version-scoped, not user-scoped.
tmp11="$(mktemp -d)"
shared_home11="$tmp11/home"
export CLAUDE_PLUGIN_ROOT_A="$tmp11/pluginA"
export CLAUDE_PLUGIN_ROOT_B="$tmp11/pluginB"
mkdir -p "$CLAUDE_PLUGIN_ROOT_A/.claude-plugin" "$CLAUDE_PLUGIN_ROOT_B/.claude-plugin" \
  "$tmp11/bin" "$tmp11/fake-release" "$shared_home11/.local/bin"
printf '{"version":"1.0.0"}\n' > "$CLAUDE_PLUGIN_ROOT_A/.claude-plugin/plugin.json"
printf '{"version":"2.0.0"}\n' > "$CLAUDE_PLUGIN_ROOT_B/.claude-plugin/plugin.json"

build_tarball() {
  local ver="$1" stage tarball
  stage="$(mktemp -d)"
  printf '#!/bin/sh\necho v%s\n' "$ver" > "$stage/springfield"
  chmod +x "$stage/springfield"
  tarball="$tmp11/fake-release/springfield_${ver}_${os}_${arch}.tar.gz"
  tar -C "$stage" -czf "$tarball" springfield
  printf '%s  ./springfield_%s_%s_%s.tar.gz\n' "$(sha256_file "$stage/springfield")" "$ver" "$os" "$arch"
}
{
  build_tarball 1.0.0
  build_tarball 2.0.0
} > "$tmp11/fake-release/checksums.txt"
install_plugin_checksums "$tmp11/pluginA" "$tmp11/fake-release/checksums.txt"
install_plugin_checksums "$tmp11/pluginB" "$tmp11/fake-release/checksums.txt"

cat > "$tmp11/bin/curl" <<STUB
#!/usr/bin/env bash
url=""; out="-"
while [ \$# -gt 0 ]; do
  case "\$1" in
    -o) out="\$2"; shift 2 ;;
    --fail|--silent|--show-error|--location|-fsSL|-fL|-sL|-s|-L|-f) shift ;;
    --connect-timeout|--max-time) shift 2 ;;
    http*) url="\$1"; shift ;;
    *) shift ;;
  esac
done
case "\$url" in
  *checksums.txt) src="$tmp11/fake-release/checksums.txt" ;;
  *springfield_1.0.0_${os}_${arch}.tar.gz) src="$tmp11/fake-release/springfield_1.0.0_${os}_${arch}.tar.gz" ;;
  *springfield_2.0.0_${os}_${arch}.tar.gz) src="$tmp11/fake-release/springfield_2.0.0_${os}_${arch}.tar.gz" ;;
  *) exit 22 ;;
esac
case "\$url" in
  *1.0.0*) sleep 1 ;;
esac
printf '%s\n' "\$url" >> "$tmp11/curl.log"
if [ "\$out" = "-" ]; then cat "\$src"; else cp "\$src" "\$out"; fi
STUB
chmod +x "$tmp11/bin/curl"

HOME="$shared_home11" CLAUDE_PLUGIN_ROOT="$CLAUDE_PLUGIN_ROOT_A" PATH="$tmp11/bin:$PATH" \
  bash "$hook" >/dev/null 2>/dev/null &
pA=$!
HOME="$shared_home11" CLAUDE_PLUGIN_ROOT="$CLAUDE_PLUGIN_ROOT_B" PATH="$tmp11/bin:$PATH" \
  bash "$hook" >/dev/null 2>/dev/null &
pB=$!

v2_ready_while_v1_running=0
for _ in $(seq 1 100); do
  if [[ -x "$shared_home11/.cache/springfield/2.0.0/springfield" ]] && kill -0 "$pA" 2>/dev/null; then
    v2_ready_while_v1_running=1
    break
  fi
  sleep 0.05
done

wait "$pA" || { echo "FAIL test11: session A exited non-zero" >&2; fail=1; }
wait "$pB" || { echo "FAIL test11: session B exited non-zero" >&2; fail=1; }
if (( v2_ready_while_v1_running == 0 )); then
  echo "FAIL test11: v2 install did not finish while slow v1 install still held same HOME cache: $(cat "$tmp11/curl.log" 2>/dev/null)" >&2
  fail=1
fi
[[ -x "$shared_home11/.cache/springfield/1.0.0/springfield" ]] || { echo "FAIL test11: v1 not installed" >&2; fail=1; }
[[ -x "$shared_home11/.cache/springfield/2.0.0/springfield" ]] || { echo "FAIL test11: v2 not installed" >&2; fail=1; }
rm -rf "$tmp11"

# ---------- Test 12: $dest exists as regular file (not symlink) ----------
# User manually ran `cp springfield ~/.local/bin/`. Hook must replace it with
# a proper symlink to the cached binary, not error out or leave both states.
tmp12="$(mktemp -d)"
export HOME="$tmp12"
export CLAUDE_PLUGIN_ROOT="$tmp12/plugin"
mkdir -p "$tmp12/plugin/.claude-plugin" "$tmp12/.local/bin" \
  "$tmp12/.cache/springfield/0.4.2"
printf '{"version":"0.4.2"}\n' > "$tmp12/plugin/.claude-plugin/plugin.json"
printf '#!/bin/sh\necho v0.4.2\n' > "$tmp12/.cache/springfield/0.4.2/springfield"
chmod +x "$tmp12/.cache/springfield/0.4.2/springfield"
# Regular file, not a symlink
printf '#!/bin/sh\necho stale regular file\n' > "$tmp12/.local/bin/springfield"
chmod +x "$tmp12/.local/bin/springfield"

PATH="/usr/bin:/bin" bash "$hook" >/dev/null 2>/dev/null || { echo "FAIL test12: hook errored on regular-file dest" >&2; fail=1; }
if [[ ! -L "$tmp12/.local/bin/springfield" ]]; then
  echo "FAIL test12: ~/.local/bin/springfield is not a symlink after hook" >&2
  fail=1
fi
got12="$(readlink "$tmp12/.local/bin/springfield" 2>/dev/null || true)"
want12="$tmp12/.cache/springfield/0.4.2/springfield"
if [[ "$got12" != "$want12" ]]; then
  echo "FAIL test12: symlink target wrong: got=$got12 want=$want12" >&2
  fail=1
fi
rm -rf "$tmp12"

# ---------- Test 13: plugin.json file missing entirely ----------
# CLAUDE_PLUGIN_ROOT set but the manifest doesn't exist (e.g. broken install,
# wrong path). Hook must not error the session; just print a clear message
# and exit 0.
tmp13="$(mktemp -d)"
export HOME="$tmp13"
export CLAUDE_PLUGIN_ROOT="$tmp13/plugin"
mkdir -p "$tmp13/plugin" "$tmp13/.local/bin"
# No .claude-plugin/plugin.json created.

set +e
err13="$(bash "$hook" 2>&1 >/dev/null)"
rc13=$?
set -e
if (( rc13 != 0 )); then
  echo "FAIL test13: missing plugin.json crashed hook with rc=$rc13" >&2
  fail=1
fi
if [[ "$err13" != *"could not read plugin version"* ]]; then
  echo "FAIL test13: missing plugin.json did not produce expected message: $err13" >&2
  fail=1
fi
rm -rf "$tmp13"

# ---------- Test 14: cached binary deleted externally (dangling refs) ----------
# Something (antivirus? user?) deleted the cached binary after a prior install.
# Cache dir still exists but is empty. Fast path must miss, fetch path must
# re-install without error.
tmp14="$(mktemp -d)"
export HOME="$tmp14"
export CLAUDE_PLUGIN_ROOT="$tmp14/plugin"
mkdir -p "$tmp14/plugin/.claude-plugin" "$tmp14/bin" "$tmp14/fake-release" \
  "$tmp14/.local/bin" "$tmp14/.cache/springfield/0.7.7"
printf '{"version":"0.7.7"}\n' > "$tmp14/plugin/.claude-plugin/plugin.json"
# Empty cache dir — binary was deleted externally
# Dangling symlink pointing at deleted file
ln -sfn "$tmp14/.cache/springfield/0.7.7/springfield" "$tmp14/.local/bin/springfield"

stage14="$(mktemp -d)"
printf '#!/bin/sh\necho v0.7.7\n' > "$stage14/springfield"
chmod +x "$stage14/springfield"
tarball14="$tmp14/fake-release/springfield_0.7.7_${os}_${arch}.tar.gz"
tar -C "$stage14" -czf "$tarball14" springfield
write_checksum_entry "$tmp14/fake-release/checksums.txt" "0.7.7" "$os" "$arch" "$stage14/springfield"
install_plugin_checksums "$tmp14/plugin" "$tmp14/fake-release/checksums.txt"

cat > "$tmp14/bin/curl" <<STUB
#!/usr/bin/env bash
url=""; out="-"
while [ \$# -gt 0 ]; do
  case "\$1" in
    -o) out="\$2"; shift 2 ;;
    --fail|--silent|--show-error|--location|-fsSL|-fL|-sL|-s|-L|-f) shift ;;
    --connect-timeout|--max-time) shift 2 ;;
    http*) url="\$1"; shift ;;
    *) shift ;;
  esac
done
case "\$url" in
  *checksums.txt) src="$tmp14/fake-release/checksums.txt" ;;
  *springfield_0.7.7_${os}_${arch}.tar.gz) src="$tarball14" ;;
  *) exit 22 ;;
esac
if [ "\$out" = "-" ]; then cat "\$src"; else cp "\$src" "\$out"; fi
STUB
chmod +x "$tmp14/bin/curl"

PATH="$tmp14/bin:$PATH" bash "$hook" >/dev/null 2>/dev/null || { echo "FAIL test14: hook errored on dangling state" >&2; fail=1; }
[[ -x "$tmp14/.cache/springfield/0.7.7/springfield" ]] || { echo "FAIL test14: binary not re-installed" >&2; fail=1; }
got14="$(readlink "$tmp14/.local/bin/springfield" 2>/dev/null || true)"
want14="$tmp14/.cache/springfield/0.7.7/springfield"
if [[ "$got14" != "$want14" ]]; then
  echo "FAIL test14: symlink not refreshed: got=$got14 want=$want14" >&2
  fail=1
fi
rm -rf "$tmp14"

# ---------- Test 15: successful install leaves NO stale lock behind ----------
# release_install_lock used to call rmdir, which fails silently on non-empty
# dirs. With a pid sentinel inside, every successful install leaked the lock
# forever, forcing later sessions through stale-reap heuristics.
tmp15="$(mktemp -d)"
export HOME="$tmp15"
export CLAUDE_PLUGIN_ROOT="$tmp15/plugin"
mkdir -p "$tmp15/plugin/.claude-plugin" "$tmp15/bin" "$tmp15/fake-release" \
  "$tmp15/.local/bin"
printf '{"version":"1.5.0"}\n' > "$tmp15/plugin/.claude-plugin/plugin.json"

stage15="$(mktemp -d)"
printf '#!/bin/sh\necho v1.5.0\n' > "$stage15/springfield"
chmod +x "$stage15/springfield"
tarball15="$tmp15/fake-release/springfield_1.5.0_${os}_${arch}.tar.gz"
tar -C "$stage15" -czf "$tarball15" springfield
write_checksum_entry "$tmp15/fake-release/checksums.txt" "1.5.0" "$os" "$arch" "$stage15/springfield"
install_plugin_checksums "$tmp15/plugin" "$tmp15/fake-release/checksums.txt"

cat > "$tmp15/bin/curl" <<STUB
#!/usr/bin/env bash
url=""; out="-"
while [ \$# -gt 0 ]; do
  case "\$1" in
    -o) out="\$2"; shift 2 ;;
    --fail|--silent|--show-error|--location|-fsSL|-fL|-sL|-s|-L|-f) shift ;;
    --connect-timeout|--max-time) shift 2 ;;
    http*) url="\$1"; shift ;;
    *) shift ;;
  esac
done
case "\$url" in
  *checksums.txt) src="$tmp15/fake-release/checksums.txt" ;;
  *springfield_1.5.0_${os}_${arch}.tar.gz) src="$tarball15" ;;
  *) exit 22 ;;
esac
if [ "\$out" = "-" ]; then cat "\$src"; else cp "\$src" "\$out"; fi
STUB
chmod +x "$tmp15/bin/curl"

PATH="$tmp15/bin:$PATH" bash "$hook" >/dev/null 2>/dev/null || { echo "FAIL test15: hook errored" >&2; fail=1; }
# Lock dir must NOT exist after successful install.
if [[ -e "$tmp15/.cache/springfield/.locks/1.5.0" ]]; then
  echo "FAIL test15: stale lock leaked after successful install: $(ls -la "$tmp15/.cache/springfield/.locks/1.5.0")" >&2
  fail=1
fi
rm -rf "$tmp15"

# ---------- Test 16: symlink failure in cache-hit fast path does not crash ----------
# ~/.local/bin is read-only, so `ln -sfn` cannot create the symlink there.
# Under raw `set -e` this would exit non-zero and crash the synchronous
# SessionStart. safe_symlink must catch the failure, warn, and exit 0.
tmp16="$(mktemp -d)"
export HOME="$tmp16"
export CLAUDE_PLUGIN_ROOT="$tmp16/plugin"
mkdir -p "$tmp16/plugin/.claude-plugin" \
  "$tmp16/.cache/springfield/0.6.3" \
  "$tmp16/.local/bin"
printf '{"version":"0.6.3"}\n' > "$tmp16/plugin/.claude-plugin/plugin.json"
printf '#!/bin/sh\necho v0.6.3\n' > "$tmp16/.cache/springfield/0.6.3/springfield"
chmod +x "$tmp16/.cache/springfield/0.6.3/springfield"
# Deny write on the parent so ln fails.
chmod 555 "$tmp16/.local/bin"

set +e
PATH="/usr/bin:/bin" bash "$hook" >/dev/null 2>"$tmp16/err16.log"
rc16=$?
set -e
chmod 755 "$tmp16/.local/bin"  # so rm can clean up
if (( rc16 != 0 )); then
  echo "FAIL test16: symlink failure crashed hook with rc=$rc16" >&2
  fail=1
fi
if ! grep -q "failed to update" "$tmp16/err16.log" 2>/dev/null; then
  echo "FAIL test16: expected diagnostic not emitted: $(cat "$tmp16/err16.log")" >&2
  fail=1
fi
rm -rf "$tmp16"

# ---------- Test 17: disk-full mid-extract falls back and leaves no partial ----------
# Simulate tar failing after creating a partial staging file. The hook must
# clean staging, keep rc 0, and avoid publishing any partial cache binary.
tmp17="$(mktemp -d)"
export HOME="$tmp17"
export CLAUDE_PLUGIN_ROOT="$tmp17/plugin"
mkdir -p "$tmp17/plugin/.claude-plugin" "$tmp17/bin" "$tmp17/fake-release" \
  "$tmp17/.local/bin"
printf '{"version":"0.8.8"}\n' > "$tmp17/plugin/.claude-plugin/plugin.json"

stage17="$(mktemp -d)"
printf '#!/bin/sh\necho v0.8.8\n' > "$stage17/springfield"
chmod +x "$stage17/springfield"
tarball17="$tmp17/fake-release/springfield_0.8.8_${os}_${arch}.tar.gz"
tar -C "$stage17" -czf "$tarball17" springfield
write_checksum_entry "$tmp17/fake-release/checksums.txt" "0.8.8" "$os" "$arch" "$stage17/springfield"
install_plugin_checksums "$tmp17/plugin" "$tmp17/fake-release/checksums.txt"

real_tar="$(command -v tar)"
cat > "$tmp17/bin/curl" <<STUB
#!/usr/bin/env bash
url=""; out="-"
while [ \$# -gt 0 ]; do
  case "\$1" in
    -o) out="\$2"; shift 2 ;;
    --fail|--silent|--show-error|--location|-fsSL|-fL|-sL|-s|-L|-f) shift ;;
    --connect-timeout|--max-time) shift 2 ;;
    http*) url="\$1"; shift ;;
    *) shift ;;
  esac
done
case "\$url" in
  *checksums.txt) src="$tmp17/fake-release/checksums.txt" ;;
  *springfield_0.8.8_${os}_${arch}.tar.gz) src="$tarball17" ;;
  *) exit 22 ;;
esac
if [ "\$out" = "-" ]; then cat "\$src"; else cp "\$src" "\$out"; fi
STUB
cat > "$tmp17/bin/tar" <<STUB
#!/usr/bin/env bash
set -euo pipefail
args=("\$@")
dest=""
extract=0
while [ \$# -gt 0 ]; do
  case "\$1" in
    -C) dest="\$2"; shift 2 ;;
    -xzf) extract=1; shift 2 ;;
    *) shift ;;
  esac
done
if [ "\$extract" -eq 1 ]; then
  mkdir -p "\$dest"
  printf 'partial\n' > "\$dest/springfield"
  exit 1
fi
exec "$real_tar" "\${args[@]}"
STUB
chmod +x "$tmp17/bin/curl" "$tmp17/bin/tar"

set +e
PATH="$tmp17/bin:$PATH" bash "$hook" >/dev/null 2>"$tmp17/err17.log"
rc17=$?
set -e
if (( rc17 != 0 )); then
  echo "FAIL test17: disk-full simulation crashed hook with rc=$rc17" >&2
  fail=1
fi
if [[ -e "$tmp17/.cache/springfield/0.8.8/springfield" ]]; then
  echo "FAIL test17: partial binary survived disk-full simulation" >&2
  fail=1
fi
if compgen -G "$tmp17/.cache/springfield/0.8.8/.staging.*" >/dev/null; then
  echo "FAIL test17: staging dir leaked after disk-full simulation" >&2
  fail=1
fi
if ! grep -q "install failed" "$tmp17/err17.log" 2>/dev/null; then
  echo "FAIL test17: disk-full simulation did not route through fallback: $(cat "$tmp17/err17.log")" >&2
  fail=1
fi
rm -rf "$tmp17"

# ---------- Test 18: concurrent cache-hit relinks never leave link absent ----------
# Two same-version cache-hit sessions race to refresh the public symlink.
# safe_symlink must swap atomically; an unlink/create implementation leaves a
# command-not-found window. The ln stub below only widens that window when the
# final public path itself is the ln destination.
tmp18="$(mktemp -d)"
export HOME="$tmp18"
export CLAUDE_PLUGIN_ROOT="$tmp18/plugin"
mkdir -p "$tmp18/plugin/.claude-plugin" "$tmp18/bin" \
  "$tmp18/.cache/springfield/0.8.9" "$tmp18/.cache/springfield/old" "$tmp18/.local/bin"
printf '{"version":"0.8.9"}\n' > "$tmp18/plugin/.claude-plugin/plugin.json"
printf '#!/bin/sh\necho v0.8.9\n' > "$tmp18/.cache/springfield/0.8.9/springfield"
printf '#!/bin/sh\necho old\n' > "$tmp18/.cache/springfield/old/springfield"
chmod +x "$tmp18/.cache/springfield/0.8.9/springfield" "$tmp18/.cache/springfield/old/springfield"
ln -sfn "$tmp18/.cache/springfield/old/springfield" "$tmp18/.local/bin/springfield"

real_ln="$(command -v ln)"
cat > "$tmp18/bin/ln" <<STUB
#!/usr/bin/env bash
set -euo pipefail
dest=""
for arg in "\$@"; do
  dest="\$arg"
done
if [ "\$dest" = "$tmp18/.local/bin/springfield" ]; then
  rm -f "\$dest"
  sleep 0.1
fi
exec "$real_ln" "\$@"
STUB
chmod +x "$tmp18/bin/ln"

PATH="$tmp18/bin:$PATH" bash "$hook" >/dev/null 2>"$tmp18/err18a.log" &
p18a=$!
PATH="$tmp18/bin:$PATH" bash "$hook" >/dev/null 2>"$tmp18/err18b.log" &
p18b=$!

missing18=0
while kill -0 "$p18a" 2>/dev/null || kill -0 "$p18b" 2>/dev/null; do
  if [[ ! -L "$tmp18/.local/bin/springfield" ]]; then
    missing18=1
    break
  fi
  sleep 0.01
done
wait "$p18a" || { echo "FAIL test18: hook A exited non-zero" >&2; fail=1; }
wait "$p18b" || { echo "FAIL test18: hook B exited non-zero" >&2; fail=1; }
if (( missing18 != 0 )); then
  echo "FAIL test18: public symlink disappeared during concurrent relink" >&2
  fail=1
fi
got18="$(readlink "$tmp18/.local/bin/springfield" 2>/dev/null || true)"
want18="$tmp18/.cache/springfield/0.8.9/springfield"
if [[ "$got18" != "$want18" ]]; then
  echo "FAIL test18: concurrent relink ended at wrong target: got=$got18 want=$want18" >&2
  fail=1
fi
rm -rf "$tmp18"

# ---------- Test 19: hostile checksums filename text does not match asset ----------
# A malicious checksums.txt line with shell-ish suffix text must not be treated
# as the asset entry. The exact asset line should still win and install cleanly.
tmp19="$(mktemp -d)"
export HOME="$tmp19"
export CLAUDE_PLUGIN_ROOT="$tmp19/plugin"
mkdir -p "$tmp19/plugin/.claude-plugin" "$tmp19/bin" "$tmp19/fake-release" \
  "$tmp19/.local/bin"
printf '{"version":"0.9.1"}\n' > "$tmp19/plugin/.claude-plugin/plugin.json"

stage19="$(mktemp -d)"
printf '#!/bin/sh\necho v0.9.1\n' > "$stage19/springfield"
chmod +x "$stage19/springfield"
tarball19="$tmp19/fake-release/springfield_0.9.1_${os}_${arch}.tar.gz"
tar -C "$stage19" -czf "$tarball19" springfield
sum19="$(sha256_file "$stage19/springfield")"
cat > "$tmp19/fake-release/checksums.txt" <<EOF
0000000000000000000000000000000000000000000000000000000000000000  ./springfield_0.9.1_${os}_${arch}.tar.gz; touch "$tmp19/pwned"
$sum19  ./springfield_0.9.1_${os}_${arch}.tar.gz
EOF
install_plugin_checksums "$tmp19/plugin" "$tmp19/fake-release/checksums.txt"

cat > "$tmp19/bin/curl" <<STUB
#!/usr/bin/env bash
url=""; out="-"
while [ \$# -gt 0 ]; do
  case "\$1" in
    -o) out="\$2"; shift 2 ;;
    --fail|--silent|--show-error|--location|-fsSL|-fL|-sL|-s|-L|-f) shift ;;
    --connect-timeout|--max-time) shift 2 ;;
    http*) url="\$1"; shift ;;
    *) shift ;;
  esac
done
case "\$url" in
  *checksums.txt) src="$tmp19/fake-release/checksums.txt" ;;
  *springfield_0.9.1_${os}_${arch}.tar.gz) src="$tarball19" ;;
  *) exit 22 ;;
esac
if [ "\$out" = "-" ]; then cat "\$src"; else cp "\$src" "\$out"; fi
STUB
chmod +x "$tmp19/bin/curl"

PATH="$tmp19/bin:$PATH" bash "$hook" >/dev/null 2>"$tmp19/err19.log" || { echo "FAIL test19: hook errored on hostile checksums.txt" >&2; fail=1; }
[[ -x "$tmp19/.cache/springfield/0.9.1/springfield" ]] || { echo "FAIL test19: hostile checksums.txt blocked valid install" >&2; fail=1; }
if [[ -e "$tmp19/pwned" ]]; then
  echo "FAIL test19: hostile checksums content executed" >&2
  fail=1
fi
rm -rf "$tmp19"

# ---------- Test 20: HOME with spaces still installs and relinks correctly ----------
tmp20_root="$(mktemp -d)"
tmp20="$tmp20_root/home with spaces"
export HOME="$tmp20"
export CLAUDE_PLUGIN_ROOT="$tmp20/plugin root"
mkdir -p "$tmp20/plugin root/.claude-plugin" "$tmp20/bin dir" "$tmp20/fake release" "$tmp20/.local/bin"
printf '{"version":"0.9.2"}\n' > "$tmp20/plugin root/.claude-plugin/plugin.json"

stage20="$(mktemp -d)"
printf '#!/bin/sh\necho v0.9.2\n' > "$stage20/springfield"
chmod +x "$stage20/springfield"
tarball20="$tmp20/fake release/springfield_0.9.2_${os}_${arch}.tar.gz"
tar -C "$stage20" -czf "$tarball20" springfield
write_checksum_entry "$tmp20/fake release/checksums.txt" "0.9.2" "$os" "$arch" "$stage20/springfield"
install_plugin_checksums "$tmp20/plugin root" "$tmp20/fake release/checksums.txt"

cat > "$tmp20/bin dir/curl" <<STUB
#!/usr/bin/env bash
url=""; out="-"
while [ \$# -gt 0 ]; do
  case "\$1" in
    -o) out="\$2"; shift 2 ;;
    --fail|--silent|--show-error|--location|-fsSL|-fL|-sL|-s|-L|-f) shift ;;
    --connect-timeout|--max-time) shift 2 ;;
    http*) url="\$1"; shift ;;
    *) shift ;;
  esac
done
case "\$url" in
  *checksums.txt) src="$tmp20/fake release/checksums.txt" ;;
  *springfield_0.9.2_${os}_${arch}.tar.gz) src="$tarball20" ;;
  *) exit 22 ;;
esac
if [ "\$out" = "-" ]; then cat "\$src"; else cp "\$src" "\$out"; fi
STUB
chmod +x "$tmp20/bin dir/curl"

PATH="$tmp20/bin dir:$PATH" bash "$hook" >/dev/null 2>"$tmp20/err20.log" || { echo "FAIL test20: hook errored with spaces in HOME: $(cat "$tmp20/err20.log")" >&2; fail=1; }
got20="$(readlink "$tmp20/.local/bin/springfield" 2>/dev/null || true)"
want20="$tmp20/.cache/springfield/0.9.2/springfield"
if [[ "$got20" != "$want20" ]]; then
  echo "FAIL test20: symlink wrong with spaces in HOME: got=$got20 want=$want20" >&2
  fail=1
fi
rm -rf "$tmp20_root"

# ---------- Test 21: real directory collision at dest warns instead of mutating ----------
tmp21="$(mktemp -d)"
export HOME="$tmp21"
export CLAUDE_PLUGIN_ROOT="$tmp21/plugin"
mkdir -p "$tmp21/plugin/.claude-plugin" "$tmp21/.cache/springfield/0.9.3" \
  "$tmp21/.local/bin/springfield"
printf '{"version":"0.9.3"}\n' > "$tmp21/plugin/.claude-plugin/plugin.json"
printf '#!/bin/sh\necho v0.9.3\n' > "$tmp21/.cache/springfield/0.9.3/springfield"
chmod +x "$tmp21/.cache/springfield/0.9.3/springfield"

set +e
PATH="/usr/bin:/bin" bash "$hook" >/dev/null 2>"$tmp21/err21.log"
rc21=$?
set -e
if (( rc21 != 0 )); then
  echo "FAIL test21: directory collision crashed hook with rc=$rc21" >&2
  fail=1
fi
if [[ ! -d "$tmp21/.local/bin/springfield" ]]; then
  echo "FAIL test21: directory collision path was unexpectedly replaced" >&2
  fail=1
fi
if ! grep -q "failed to update" "$tmp21/err21.log" 2>/dev/null; then
  echo "FAIL test21: directory collision did not emit diagnostic: $(cat "$tmp21/err21.log")" >&2
  fail=1
fi
rm -rf "$tmp21"

# ---------- Test 22: timeout must not steal a live same-version lock ----------
# First install holds the version lock with slow-but-valid downloads. Second
# same-version session times out waiting and must fall back, not delete the
# live lock and start its own download.
tmp22="$(mktemp -d)"
export HOME="$tmp22"
export CLAUDE_PLUGIN_ROOT="$tmp22/plugin"
mkdir -p "$tmp22/plugin/.claude-plugin" "$tmp22/bin" "$tmp22/fake-release" \
  "$tmp22/.local/bin"
printf '{"version":"0.9.4"}\n' > "$tmp22/plugin/.claude-plugin/plugin.json"

stage22="$(mktemp -d)"
printf '#!/bin/sh\necho v0.9.4\n' > "$stage22/springfield"
chmod +x "$stage22/springfield"
tarball22="$tmp22/fake-release/springfield_0.9.4_${os}_${arch}.tar.gz"
tar -C "$stage22" -czf "$tarball22" springfield
write_checksum_entry "$tmp22/fake-release/checksums.txt" "0.9.4" "$os" "$arch" "$stage22/springfield"
install_plugin_checksums "$tmp22/plugin" "$tmp22/fake-release/checksums.txt"

cat > "$tmp22/bin/curl" <<STUB
#!/usr/bin/env bash
set -euo pipefail
url=""
out="-"
while [ \$# -gt 0 ]; do
  case "\$1" in
    -o) out="\$2"; shift 2 ;;
    --fail|--silent|--show-error|--location|-fsSL|-fL|-sL|-s|-L|-f) shift ;;
    --connect-timeout|--max-time) shift 2 ;;
    http*) url="\$1"; shift ;;
    *) shift ;;
  esac
done
printf '%s\n' "\$url" >> "$tmp22/curl.log"
case "\$url" in
  *checksums.txt) src="$tmp22/fake-release/checksums.txt" ;;
  *springfield_0.9.4_${os}_${arch}.tar.gz) src="$tarball22" ;;
  *) exit 22 ;;
esac
sleep 0.4
if [ "\$out" = "-" ]; then cat "\$src"; else cp "\$src" "\$out"; fi
STUB
chmod +x "$tmp22/bin/curl"

PATH="$tmp22/bin:$PATH" \
SPRINGFIELD_INSTALL_LOCK_MAX_WAIT_TENTHS=2 \
SPRINGFIELD_INSTALL_LOCK_STALE_AGE_SECONDS=60 \
  bash "$hook" >/dev/null 2>"$tmp22/err22a.log" &
p22a=$!

for _ in $(seq 1 100); do
  if [[ -f "$tmp22/.cache/springfield/.locks/0.9.4/pid" ]] && [[ "$(cat "$tmp22/.cache/springfield/.locks/0.9.4/pid" 2>/dev/null || true)" == "$p22a" ]]; then
    break
  fi
  sleep 0.05
done
if [[ ! -f "$tmp22/.cache/springfield/.locks/0.9.4/pid" ]] || [[ "$(cat "$tmp22/.cache/springfield/.locks/0.9.4/pid" 2>/dev/null || true)" != "$p22a" ]]; then
  echo "FAIL test22: primary install never became lock holder" >&2
  fail=1
fi

PATH="$tmp22/bin:$PATH" \
SPRINGFIELD_INSTALL_LOCK_MAX_WAIT_TENTHS=2 \
SPRINGFIELD_INSTALL_LOCK_STALE_AGE_SECONDS=60 \
  bash "$hook" >/dev/null 2>"$tmp22/err22b.log" &
p22b=$!

wait "$p22a" || { echo "FAIL test22: primary install exited non-zero" >&2; fail=1; }
wait "$p22b" || { echo "FAIL test22: waiting install exited non-zero" >&2; fail=1; }

asset_fetches22="$(grep -c 'springfield_0.9.4_' "$tmp22/curl.log" 2>/dev/null || true)"
sum_fetches22="$(grep -c 'checksums.txt' "$tmp22/curl.log" 2>/dev/null || true)"
if [[ "$asset_fetches22" != "1" || "$sum_fetches22" != "0" ]]; then
  echo "FAIL test22: waiting session stole live lock and started duplicate downloads: assets=$asset_fetches22 sums=$sum_fetches22" >&2
  fail=1
fi
[[ -x "$tmp22/.cache/springfield/0.9.4/springfield" ]] || { echo "FAIL test22: primary install did not finish" >&2; fail=1; }
if ! grep -q 'held too long, skipping' "$tmp22/err22b.log" 2>/dev/null; then
  echo "FAIL test22: waiting session did not time out cleanly: $(cat "$tmp22/err22b.log")" >&2
  fail=1
fi
rm -rf "$tmp22"

# ---------- Test 23: missing plugin-shipped checksum manifest blocks install before network ----------
tmp23="$(mktemp -d)"
export HOME="$tmp23"
export CLAUDE_PLUGIN_ROOT="$tmp23/plugin"
mkdir -p "$tmp23/plugin/.claude-plugin" "$tmp23/bin" "$tmp23/.local/bin"
printf '{"version":"0.9.5"}\n' > "$tmp23/plugin/.claude-plugin/plugin.json"

cat > "$tmp23/bin/curl" <<'STUB'
#!/usr/bin/env bash
echo "unexpected network fetch" >&2
exit 99
STUB
chmod +x "$tmp23/bin/curl"

set +e
PATH="$tmp23/bin:$PATH" bash "$hook" >/dev/null 2>"$tmp23/err23.log"
rc23=$?
set -e
err23="$(cat "$tmp23/err23.log" 2>/dev/null || true)"
if (( rc23 != 0 )); then
  echo "FAIL test23: missing manifest crashed hook with rc=$rc23" >&2
  fail=1
fi
if [[ "$err23" != *"checksum manifest missing"* ]]; then
  echo "FAIL test23: missing manifest not reported clearly: $err23" >&2
  fail=1
fi
if [[ "$err23" == *"checksum entry missing"* ]]; then
  echo "FAIL test23: missing manifest should not be reported as missing entry: $err23" >&2
  fail=1
fi
if [[ -e "$tmp23/.cache/springfield/0.9.5/springfield" ]]; then
  echo "FAIL test23: install proceeded despite missing manifest" >&2
  fail=1
fi
rm -rf "$tmp23"

# ---------- Test 24: manifest without current asset entry blocks install before network ----------
tmp24="$(mktemp -d)"
export HOME="$tmp24"
export CLAUDE_PLUGIN_ROOT="$tmp24/plugin"
mkdir -p "$tmp24/plugin/.claude-plugin" "$tmp24/.local/bin"
printf '{"version":"0.9.6"}\n' > "$tmp24/plugin/.claude-plugin/plugin.json"
other_arch="amd64"
if [[ "$arch" == "amd64" ]]; then
  other_arch="arm64"
fi
write_plugin_checksum_entry "$tmp24/plugin" "0.9.6" "$os" "$other_arch" \
  "0000000000000000000000000000000000000000000000000000000000000000"

mkdir -p "$tmp24/bin"
cat > "$tmp24/bin/curl" <<'STUB'
#!/usr/bin/env bash
echo "unexpected network fetch" >&2
exit 99
STUB
chmod +x "$tmp24/bin/curl"

set +e
PATH="$tmp24/bin:$PATH" bash "$hook" >/dev/null 2>"$tmp24/err24.log"
rc24=$?
set -e
err24="$(cat "$tmp24/err24.log" 2>/dev/null || true)"
if (( rc24 != 0 )); then
  echo "FAIL test24: missing manifest entry crashed hook with rc=$rc24" >&2
  fail=1
fi
if [[ "$err24" != *"checksum entry missing"* ]]; then
  echo "FAIL test24: missing manifest entry not reported clearly: $err24" >&2
  fail=1
fi
if [[ "$err24" == *"checksum manifest missing"* ]]; then
  echo "FAIL test24: missing entry should not be reported as missing manifest: $err24" >&2
  fail=1
fi
if [[ -e "$tmp24/.cache/springfield/0.9.6/springfield" ]]; then
  echo "FAIL test24: install proceeded despite missing manifest entry" >&2
  fail=1
fi
rm -rf "$tmp24"

exit $fail
