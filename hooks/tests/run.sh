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

exit $fail
