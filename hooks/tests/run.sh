#!/usr/bin/env bash
set -euo pipefail
fail=0

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
hook="$script_dir/../session-start.sh"

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

if command -v sha256sum >/dev/null 2>&1; then
  sum="$(sha256sum "$tarball" | awk '{print $1}')"
else
  sum="$(shasum -a 256 "$tarball" | awk '{print $1}')"
fi
# Mirror real release workflow: `sha256sum ./*.tar.gz > checksums.txt` writes ./-prefixed paths
printf '%s  ./springfield_9.9.9_%s_%s.tar.gz\n' "$sum" "$os" "$arch" \
  > "$tmp2/fake-release/checksums.txt"

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

# curl stub that 404s everything
cat > "$tmp3/bin/curl" <<'STUB'
#!/bin/sh
exit 22
STUB
chmod +x "$tmp3/bin/curl"

PATH="$tmp3/bin:$PATH" bash "$hook" || true
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

PATH="$tmp4/bin:$PATH" bash "$hook" || true
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

if command -v sha256sum >/dev/null 2>&1; then
  sum5="$(sha256sum "$tarball5" | awk '{print $1}')"
  expected_bin_sum="$(sha256sum "$stage5/springfield" | awk '{print $1}')"
else
  sum5="$(shasum -a 256 "$tarball5" | awk '{print $1}')"
  expected_bin_sum="$(shasum -a 256 "$stage5/springfield" | awk '{print $1}')"
fi
printf '%s  ./springfield_5.5.5_%s_%s.tar.gz\n' "$sum5" "$os" "$arch" \
  > "$tmp5/fake-release/checksums.txt"

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

cat > "$tmp6/bin/curl" <<'STUB'
#!/bin/sh
exit 22
STUB
chmod +x "$tmp6/bin/curl"

err6="$(PATH="$tmp6/bin:$PATH" bash "$hook" 2>&1 >/dev/null || true)"
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

if command -v sha256sum >/dev/null 2>&1; then
  sum7="$(sha256sum "$tarball7" | awk '{print $1}')"
else
  sum7="$(shasum -a 256 "$tarball7" | awk '{print $1}')"
fi
printf '%s  ./springfield_3.3.3_%s_%s.tar.gz\n' "$sum7" "$os" "$arch" \
  > "$tmp7/fake-release/checksums.txt"

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

# ---------- Test 8: stalled fetch must bound wall-clock and fall back ----------
# GitHub / DNS / proxy hangs. SessionStart runs synchronously before any
# interactive command, so an unbounded curl blocks the user out of Claude
# for minutes. The hook MUST bound the download via --max-time and treat a
# timeout identically to a network error — fall through to fallback_symlink,
# do not hang.
tmp8="$(mktemp -d)"
export HOME="$tmp8"
export CLAUDE_PLUGIN_ROOT="$tmp8/plugin"
mkdir -p "$tmp8/plugin/.claude-plugin" "$tmp8/bin" "$tmp8/.local/bin"
printf '{"version":"2.2.2"}\n' > "$tmp8/plugin/.claude-plugin/plugin.json"

# Curl stub that sleeps 10s — longer than the hook's max-time budget for this
# test (set via SPRINGFIELD_CURL_MAX_TIME=2 below).
cat > "$tmp8/bin/curl" <<'STUB'
#!/usr/bin/env bash
# Emulate --max-time by honoring it ourselves: if passed, sleep min(N, stall).
STALL=10
MAX_TIME=""
while [ $# -gt 0 ]; do
  case "$1" in
    --max-time) MAX_TIME="$2"; shift 2 ;;
    *) shift ;;
  esac
done
if [ -n "$MAX_TIME" ]; then
  sleep_for="$MAX_TIME"
  # Sleep the max-time, then exit with curl's timeout code (28)
  sleep "$sleep_for"
  exit 28
fi
sleep "$STALL"
exit 28
STUB
chmod +x "$tmp8/bin/curl"

t_start=$(date +%s)
err8="$(PATH="$tmp8/bin:$PATH" SPRINGFIELD_CURL_CONNECT_TIMEOUT=1 \
  SPRINGFIELD_CURL_MAX_TIME=2 bash "$hook" 2>&1 >/dev/null || true)"
t_end=$(date +%s)
elapsed=$((t_end - t_start))
# Budget: max-time 2s + fallback overhead. Generous cap 8s — well under the
# minutes-long hang that would happen with no --max-time.
if (( elapsed > 8 )); then
  echo "FAIL test8: hook did not bound stalled fetch: elapsed=${elapsed}s" >&2
  fail=1
fi
if [[ "$err8" != *"timeout or error"* ]]; then
  echo "FAIL test8: stalled fetch did not route through fallback path: $err8" >&2
  fail=1
fi
rm -rf "$tmp8"

# ---------- Test 9: checksum mismatch must fall back, not install ----------
# Release was tampered with / retransmission corrupted. The script MUST NOT
# install a binary whose sha256 doesn't match the published checksums.txt.
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

err9="$(PATH="$tmp9/bin:$PATH" bash "$hook" 2>&1 >/dev/null || true)"
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

# "Tarball" is random garbage — tar -xzf will fail.
bad_tarball="$tmp10/fake-release/springfield_1.1.1_${os}_${arch}.tar.gz"
head -c 4096 /dev/urandom > "$bad_tarball"
if command -v sha256sum >/dev/null 2>&1; then
  bad_sum="$(sha256sum "$bad_tarball" | awk '{print $1}')"
else
  bad_sum="$(shasum -a 256 "$bad_tarball" | awk '{print $1}')"
fi
printf '%s  ./springfield_1.1.1_%s_%s.tar.gz\n' "$bad_sum" "$os" "$arch" \
  > "$tmp10/fake-release/checksums.txt"

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
# Two sessions, one targeting v1 and one targeting v2. They use separate lock
# dirs and separate cache paths. They must both succeed without one starving
# the other. Wall-clock budget is tight; if they serialize on a shared lock
# the second finishes only after the first's curl delay completes.
tmp11="$(mktemp -d)"
export CLAUDE_PLUGIN_ROOT_A="$tmp11/pluginA"
export CLAUDE_PLUGIN_ROOT_B="$tmp11/pluginB"
mkdir -p "$CLAUDE_PLUGIN_ROOT_A/.claude-plugin" "$CLAUDE_PLUGIN_ROOT_B/.claude-plugin" \
  "$tmp11/bin" "$tmp11/fake-release" "$tmp11/homeA/.local/bin" "$tmp11/homeB/.local/bin"
printf '{"version":"1.0.0"}\n' > "$CLAUDE_PLUGIN_ROOT_A/.claude-plugin/plugin.json"
printf '{"version":"2.0.0"}\n' > "$CLAUDE_PLUGIN_ROOT_B/.claude-plugin/plugin.json"

build_tarball() {
  local ver="$1" stage tarball
  stage="$(mktemp -d)"
  printf '#!/bin/sh\necho v%s\n' "$ver" > "$stage/springfield"
  chmod +x "$stage/springfield"
  tarball="$tmp11/fake-release/springfield_${ver}_${os}_${arch}.tar.gz"
  tar -C "$stage" -czf "$tarball" springfield
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$tarball" | awk -v a="./springfield_${ver}_${os}_${arch}.tar.gz" '{print $1"  "a}'
  else
    shasum -a 256 "$tarball" | awk -v a="./springfield_${ver}_${os}_${arch}.tar.gz" '{print $1"  "a}'
  fi
}
{
  build_tarball 1.0.0
  build_tarball 2.0.0
} > "$tmp11/fake-release/checksums.txt"

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
sleep 0.2
if [ "\$out" = "-" ]; then cat "\$src"; else cp "\$src" "\$out"; fi
STUB
chmod +x "$tmp11/bin/curl"

t11_start=$(date +%s)
HOME="$tmp11/homeA" CLAUDE_PLUGIN_ROOT="$CLAUDE_PLUGIN_ROOT_A" PATH="$tmp11/bin:$PATH" \
  bash "$hook" >/dev/null 2>/dev/null &
pA=$!
HOME="$tmp11/homeB" CLAUDE_PLUGIN_ROOT="$CLAUDE_PLUGIN_ROOT_B" PATH="$tmp11/bin:$PATH" \
  bash "$hook" >/dev/null 2>/dev/null &
pB=$!
wait "$pA" || { echo "FAIL test11: session A exited non-zero" >&2; fail=1; }
wait "$pB" || { echo "FAIL test11: session B exited non-zero" >&2; fail=1; }
t11_end=$(date +%s)
# Each session has 2 curl calls × ~0.2s sleep = ~0.4s. Run in parallel → ~0.4s
# total. If serialized (bug), would be ~0.8s+. Loose bound at 3s for CI noise.
if (( t11_end - t11_start > 3 )); then
  echo "FAIL test11: concurrent different-version installs did not run in parallel: ${$((t11_end - t11_start))}s" >&2
  fail=1
fi
[[ -x "$tmp11/homeA/.cache/springfield/1.0.0/springfield" ]] || { echo "FAIL test11: v1 not installed" >&2; fail=1; }
[[ -x "$tmp11/homeB/.cache/springfield/2.0.0/springfield" ]] || { echo "FAIL test11: v2 not installed" >&2; fail=1; }
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
  echo "FAIL test12: $dest is not a symlink after hook" >&2
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
if command -v sha256sum >/dev/null 2>&1; then
  sum14="$(sha256sum "$tarball14" | awk '{print $1}')"
else
  sum14="$(shasum -a 256 "$tarball14" | awk '{print $1}')"
fi
printf '%s  ./springfield_0.7.7_%s_%s.tar.gz\n' "$sum14" "$os" "$arch" \
  > "$tmp14/fake-release/checksums.txt"

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

exit $fail
