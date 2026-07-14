#!/usr/bin/env bash
# runtime-fetch.sh — download a runtime tarball into runtime-pool/<name>-<version>-<arch>/
#
# Usage:
#   tools/runtime-fetch.sh node 22.22.3 linux-x64
#   tools/runtime-fetch.sh python 3.12.13 linux-x64
#   tools/runtime-fetch.sh uv 0.11.28 linux-x64
#
# What it does:
#   1. Resolves the canonical upstream URL for that runtime + version + arch
#   2. Verifies the SHA256 against the official SHASUMS / digest attribute
#   3. Extracts into runtime-pool/<name>-<version>-<arch>/ (re-uses if present)
#
# Then to use the runtime in a version directory:
#   cp -r runtime-pool/node-22.22.3-linux-x64/ agents/openclaw/upstream/<v>/runtime/node/

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
cd "$REPO_ROOT"

log()  { echo "[runtime-fetch] $*" >&2; }
ok()   { echo "[runtime-fetch] OK: $*" >&2; }
err()  { echo "[runtime-fetch] ERR: $*" >&2; exit 1; }

[[ $# -eq 3 ]] || { echo "usage: $0 <name> <version> <arch>" >&2; exit 1; }
NAME="$1"; VERSION="$2"; ARCH="$3"
POOL_DIR="runtime-pool/${NAME}-${VERSION}-${ARCH}"

if [[ -d "$POOL_DIR" ]] && [[ -f "$POOL_DIR/.fetched" ]]; then
  log "$POOL_DIR already cached (delete the directory to re-fetch)"
  exit 0
fi

case "$NAME" in
  node)   fetch_node   "$VERSION" "$ARCH" "$POOL_DIR" ;;
  python) fetch_python "$VERSION" "$ARCH" "$POOL_DIR" ;;
  uv)     fetch_uv     "$VERSION" "$ARCH" "$POOL_DIR" ;;
  *) err "no fetcher defined for runtime '$NAME' (add one in tools/runtime-fetch.sh)" ;;
esac

ok "runtime-pool/$NAME-$VERSION-$ARCH/ ready (copy it into agents/<agent>/.../runtime/<name>/)"

# ---------------------------------------------------------------------------

fetch_node() {
  local version="$1" arch="$2" dest="$3"
  local url="https://nodejs.org/dist/v${version}/node-v${version}-${arch}.tar.xz"
  local shasums_url="https://nodejs.org/dist/v${version}/SHASUMS256.txt"

  local tmp; tmp="$(mktemp -t node-fetch.XXXXXX.tar.xz)"
  log "downloading $url"
  curl -sL --fail "$url" -o "$tmp" || err "download failed: $url"

  # Verify against official SHASUMS256.txt
  local expected; expected="$(curl -sL --fail "$shasums_url" | awk -v f="node-v${version}-${arch}.tar.xz" '$2==f {print $1}')"
  [[ -n "$expected" ]] || err "could not find $arch tarball in $shasums_url"
  local actual; actual="$(sha256sum "$tmp" | awk '{print $1}')"
  [[ "$actual" == "$expected" ]] || err "SHA256 mismatch: expected=$expected actual=$actual"
  ok "SHA256 verified against official SHASUMS"

  mkdir -p "$dest"
  tar -xJf "$tmp" --strip-components=1 -C "$dest"
  rm -f "$tmp"
  touch "$dest/.fetched"
}

fetch_python() {
  local version="$1" arch="$2" dest="$3"

  # python-build-standalone uses tagged releases; we use the latest known tag and
  # pick the latest matching 3.12.x. Caller can override via PBS_TAG env var.
  local tag="${PBS_TAG:-20260623}"
  local filename="cpython-${version}+${tag}-${arch}-unknown-linux-gnu-install_only_stripped.tar.gz"
  local url="https://github.com/astral-sh/python-build-standalone/releases/download/${tag}/${filename}"

  local tmp; tmp="$(mktemp -t python-fetch.XXXXXX.tar.gz)"
  log "downloading $url"
  curl -sL --fail "$url" -o "$tmp" || err "download failed: $url — try setting PBS_TAG to a newer release date"

  # Verify against GitHub release asset digest
  local expected
  expected="$(curl -sL --fail "https://api.github.com/repos/astral-sh/python-build-standalone/releases/tags/${tag}" \
    | python3 -c "
import json, sys
d = json.load(sys.stdin)
for a in d.get('assets', []):
    if a['name'] == '$filename':
        print(a.get('digest','').removeprefix('sha256:'))
        break
")"
  [[ -n "$expected" ]] || err "could not find $filename in release $tag"
  local actual; actual="$(sha256sum "$tmp" | awk '{print $1}')"
  [[ "$actual" == "$expected" ]] || err "SHA256 mismatch: expected=$expected actual=$actual"
  ok "SHA256 verified against GitHub release digest"

  mkdir -p "$dest"
  tar -xzf "$tmp" --strip-components=1 -C "$dest"
  rm -f "$tmp"
  touch "$dest/.fetched"
}

fetch_uv() {
  local version="$1" arch="$2" dest="$3"
  local filename="uv-${arch}.tar.gz"
  local url="https://github.com/astral-sh/uv/releases/download/${version}/${filename}"

  local tmp; tmp="$(mktemp -t uv-fetch.XXXXXX.tar.gz)"
  log "downloading $url"
  curl -sL --fail "$url" -o "$tmp" || err "download failed: $url"

  local expected
  expected="$(curl -sL --fail "https://api.github.com/repos/astral-sh/uv/releases/tags/${version}" \
    | python3 -c "
import json, sys
d = json.load(sys.stdin)
for a in d.get('assets', []):
    if a['name'] == '$filename':
        print(a.get('digest','').removeprefix('sha256:'))
        break
")"
  [[ -n "$expected" ]] || err "could not find $filename in release $version"
  local actual; actual="$(sha256sum "$tmp" | awk '{print $1}')"
  [[ "$actual" == "$expected" ]] || err "SHA256 mismatch: expected=$expected actual=$actual"
  ok "SHA256 verified against GitHub release digest"

  mkdir -p "$dest"
  tar -xzf "$tmp" --strip-components=1 -C "$dest"
  rm -f "$tmp"
  touch "$dest/.fetched"
}