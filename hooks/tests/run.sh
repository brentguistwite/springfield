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

# ---------- Test 6: failed upgrade must NOT leave stale symlink in place ----------
# User was on v4.4.4 (cached + symlinked). Plugin upgrades to v5.5.5. Fetch
# fails and v5.5.5 isn't cached. The hook must REMOVE the stale symlink rather
# than quietly keep routing to v4.4.4, because the plugin's skills/commands
# will now expect v5.5.5 CLI semantics.
tmp6="$(mktemp -d)"
export HOME="$tmp6"
export CLAUDE_PLUGIN_ROOT="$tmp6/plugin"
mkdir -p "$tmp6/plugin/.claude-plugin" "$tmp6/bin" \
  "$tmp6/.cache/springfield/4.4.4" "$tmp6/.local/bin"
printf '{"version":"5.5.5"}\n' > "$tmp6/plugin/.claude-plugin/plugin.json"
printf '#!/bin/sh\necho v4.4.4\n' > "$tmp6/.cache/springfield/4.4.4/springfield"
chmod +x "$tmp6/.cache/springfield/4.4.4/springfield"
ln -sfn "$tmp6/.cache/springfield/4.4.4/springfield" "$tmp6/.local/bin/springfield"

cat > "$tmp6/bin/curl" <<'STUB'
#!/bin/sh
exit 22
STUB
chmod +x "$tmp6/bin/curl"

PATH="$tmp6/bin:$PATH" bash "$hook" || true
if [[ -L "$tmp6/.local/bin/springfield" || -e "$tmp6/.local/bin/springfield" ]]; then
  target="$(readlink "$tmp6/.local/bin/springfield" 2>/dev/null || echo EXISTS)"
  echo "FAIL test6: stale symlink not removed after failed upgrade: $target" >&2
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

exit $fail
