#!/usr/bin/env bash
# fetch.sh — pull an upstream artifact into agents/<agent>/<source>/<version>/payload/
#
# Usage:
#   tools/fetch.sh <agent> <source> <version>
#
# Examples:
#   tools/fetch.sh opencode upstream 0.0.55
#   tools/fetch.sh openclaw upstream 2026.7.2
#   tools/fetch.sh hermes-agent upstream 0.18.2
#
# Per-agent behavior:
#   opencode      → downloads the linux-x86_64 tar.gz from GitHub Releases,
#                   extracts the inner `opencode` binary into payload/bin/
#   openclaw      → downloads the .tgz from the npm registry, places it as
#                   payload/openclaw-<version>.tgz (size + SHA256 logged)
#   hermes-agent  → downloads the wheel from a PyPI mirror, places it as
#                   payload/hermes_agent-<version>-py3-none-any.whl
#
# After fetching, the script updates manifest.json's `upstream.sha256` field
# to record the artifact's SHA256 for audit.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
cd "$REPO_ROOT"

log()  { echo "[fetch] $*" >&2; }
ok()   { echo "[fetch] OK: $*" >&2; }
err()  { echo "[fetch] ERR: $*" >&2; exit 1; }

[[ $# -eq 3 ]] || { echo "usage: $0 <agent> <source> <version>" >&2; exit 1; }
AGENT="$1"; SOURCE="$2"; VERSION="$3"
BUNDLE_DIR="agents/$AGENT/$SOURCE/$VERSION"

[[ -d "$BUNDLE_DIR" ]] || { err "$BUNDLE_DIR does not exist; create it first (or run tools/pack.sh's CONTRIBUTING.md SOP)" ; exit 1; }

# Pick the right fetcher based on agent
case "$AGENT" in
  opencode)      fetch_opencode      "$VERSION" "$BUNDLE_DIR" ;;
  openclaw)      fetch_openclaw      "$VERSION" "$BUNDLE_DIR" ;;
  hermes-agent)  fetch_hermes_agent  "$VERSION" "$BUNDLE_DIR" ;;
  *) err "no fetcher defined for agent '$AGENT'; add a function to tools/fetch.sh" ;;
esac

ok "fetched $AGENT/$SOURCE/$VERSION → $BUNDLE_DIR/payload/"

# ---------------------------------------------------------------------------
# Per-agent implementations
# ---------------------------------------------------------------------------

fetch_opencode() {
  local version="$1" dir="$2"
  local tag="v$version"
  local url="https://github.com/opencode-ai/opencode/releases/download/$tag/opencode-linux-x86_64.tar.gz"

  mkdir -p "$dir/payload/bin"
  local tmp; tmp="$(mktemp -t opencode-fetch.XXXXXX.tar.gz)"

  log "downloading $url"
  if ! curl -sL --fail "$url" -o "$tmp"; then
    err "download failed: $url"
  fi

  local expected; expected="$(curl -sL --fail "https://github.com/opencode-ai/opencode/releases/download/$tag/checksums.txt" | awk '/opencode-linux-x86_64\.tar\.gz/ {print $1}')"
  if [[ -z "$expected" ]]; then
    warn "no upstream checksum found at checksums.txt — proceeding without cross-check"
  else
    local actual; actual="$(sha256sum "$tmp" | awk '{print $1}')"
    if [[ "$actual" != "$expected" ]]; then
      err "sha256 mismatch: expected=$expected actual=$actual"
    fi
    ok "upstream SHA256 verified: $actual"
  fi

  # Extract only the `opencode` binary into payload/bin/, discard the wrapper
  tar -xzf "$tmp" -C "$dir/payload/" opencode
  mv "$dir/payload/opencode" "$dir/payload/bin/opencode"
  chmod 0755 "$dir/payload/bin/opencode"
  rm -f "$tmp"

  local new_sha; new_sha="$(sha256sum "$dir/payload/bin/opencode" | awk '{print $1}')"
  log "binary SHA256: $new_sha"

  # Update manifest.json's upstream.sha256
  if [[ -f "$dir/manifest.json" ]]; then
    python3 -c "
import json, sys
with open('$dir/manifest.json') as f: d = json.load(f)
d.setdefault('upstream', {})['sha256'] = 'sha256:$expected'
with open('$dir/manifest.json', 'w') as f:
    json.dump(d, f, indent=2, sort_keys=True); f.write('\n')
"
    ok "updated manifest.json upstream.sha256"
  fi
}

fetch_openclaw() {
  local version="$1" dir="$2"
  local url="https://registry.npmjs.org/openclaw/-/openclaw-${version}.tgz"

  mkdir -p "$dir/payload"
  local tmp; tmp="$(mktemp -t openclaw-fetch.XXXXXX.tgz)"

  log "downloading $url"
  if ! curl -sL --fail "$url" -o "$tmp"; then
    err "download failed: $url — is the version published on npm?"
  fi

  local actual; actual="$(sha256sum "$tmp" | awk '{print $1}')"
  ok "tarball SHA256: $actual"
  mv "$tmp" "$dir/payload/openclaw-${version}.tgz"

  # Update manifest.json's upstream.sha256
  if [[ -f "$dir/manifest.json" ]]; then
    python3 -c "
import json
with open('$dir/manifest.json') as f: d = json.load(f)
d.setdefault('upstream', {})['sha256'] = 'sha256:$actual'
with open('$dir/manifest.json', 'w') as f:
    json.dump(d, f, indent=2, sort_keys=True); f.write('\n')
"
    ok "updated manifest.json upstream.sha256"
  fi
}

fetch_hermes_agent() {
  local version="$1" dir="$2"
  # Use Aliyun mirror as the primary (more reliable from build hosts),
  # fall back to upstream if it's not reachable.
  local mirror="https://mirrors.aliyun.com/pypi/simple/"
  local page; page="$(curl -sL --fail "${mirror}hermes-agent/")" || err "cannot list hermes-agent on $mirror"

  # Find the wheel URL matching this version
  local url
  url="$(echo "$page" | python3 -c "
import sys, re
html = sys.stdin.read()
# Pick the first .whl that mentions the version (highest version first on aliyun)
for m in re.finditer(r'href=\"([^\"]+hermes_agent-${version}-[^\"]+\.whl)\"', html):
    print(m.group(1).split('#')[0]); break
")"
  if [[ -z "$url" ]]; then
    err "no wheel for hermes-agent ${version} on $mirror"
  fi
  # Aliyun relative URLs look like ../../packages/.../.../...
  url="${url#../../}"
  local full_url="https://mirrors.aliyun.com/pypi/${url}"

  mkdir -p "$dir/payload"
  local tmp; tmp="$(mktemp -t hermes-fetch.XXXXXX.whl)"

  log "downloading $full_url"
  if ! curl -sL --fail "$full_url" -o "$tmp"; then
    err "download failed: $full_url"
  fi

  local actual; actual="$(sha256sum "$tmp" | awk '{print $1}')"
  ok "wheel SHA256: $actual"

  local fname="hermes_agent-${version}-py3-none-any.whl"
  mv "$tmp" "$dir/payload/$fname"

  # Update manifest.json's upstream.sha256
  if [[ -f "$dir/manifest.json" ]]; then
    python3 -c "
import json
with open('$dir/manifest.json') as f: d = json.load(f)
d.setdefault('upstream', {})['sha256'] = 'sha256:$actual'
d.setdefault('upstream', {})['url'] = '$full_url'
with open('$dir/manifest.json', 'w') as f:
    json.dump(d, f, indent=2, sort_keys=True); f.write('\n')
"
    ok "updated manifest.json upstream.sha256"
  fi

  # Also: stage a wheels/ subdir for fully-offline installs
  mkdir -p "$dir/payload/wheels"
  cp "$dir/payload/$fname" "$dir/payload/wheels/"
  log "staged at payload/wheels/$fname for fully-offline install"
}