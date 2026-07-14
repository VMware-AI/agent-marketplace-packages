#!/usr/bin/env bash
# pack.sh — produce dist/<agent>-<source>-<version>.tar.gz + .sha256
#
# Usage:
#   tools/pack.sh <agent> <source> <version> [out-dir]
#
# The tarball's root is the version directory itself — i.e. when extracted,
# you land in agents/<agent>/<source>/<version>/ and can immediately run
# ./install.sh.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
cd "$REPO_ROOT"

log()  { echo "[pack] $*" >&2; }
ok()   { echo "[pack] OK: $*" >&2; }
err()  { echo "[pack] ERR: $*" >&2; exit 1; }

[[ $# -ge 3 ]] || { echo "usage: $0 <agent> <source> <version> [out-dir]" >&2; exit 1; }
AGENT="$1"; SOURCE="$2"; VERSION="$3"; OUT_DIR="${4:-dist}"
BUNDLE_DIR="agents/$AGENT/$SOURCE/$VERSION"

[[ -d "$BUNDLE_DIR" ]] || err "$BUNDLE_DIR does not exist"
[[ -f "$BUNDLE_DIR/manifest.json" ]] || err "$BUNDLE_DIR/manifest.json missing"
[[ -x "$BUNDLE_DIR/install.sh" ]] || err "$BUNDLE_DIR/install.sh is missing or not executable"

mkdir -p "$OUT_DIR"

TARBALL_NAME="${AGENT}-${SOURCE}-${VERSION}.tar.gz"
TARBALL_PATH="$OUT_DIR/$TARBALL_NAME"
SHA_PATH="$TARBALL_PATH.sha256"

log "creating $TARBALL_PATH"
log "  root:  $BUNDLE_DIR/"
log "  files: $(find "$BUNDLE_DIR" -type f | wc -l | tr -d ' ')"

# Build the tarball. We cd into the parent so the tarball's root is the
# version directory name itself (not its absolute path).
BUNDLE_PARENT="$(dirname "$BUNDLE_DIR")"
BUNDLE_BASE="$(basename "$BUNDLE_DIR")"

tar -czf "$TARBALL_PATH" \
  --owner=0 --group=0 --numeric-owner \
  -C "$BUNDLE_PARENT" \
  "$BUNDLE_BASE" \
  || err "tar failed"

# Compute SHA256 in the standard `sha256sum` output format
TARBALL_ABS="$(cd "$(dirname "$TARBALL_PATH")" && pwd)/$(basename "$TARBALL_PATH")"
SHA_ABS="$(cd "$(dirname "$SHA_PATH")" && pwd)/$(basename "$SHA_PATH")"
sha256sum "$TARBALL_ABS" > "$SHA_ABS"
TAR_SHA="$(awk '{print $1}' "$SHA_ABS")"
ok "tarball SHA256: $TAR_SHA"

# Update manifest.json's tarball.sha256 if it's still TBD
if grep -q '"sha256": "sha256:TBD"' "$BUNDLE_DIR/manifest.json"; then
  python3 -c "
import json
with open('$BUNDLE_DIR/manifest.json') as f: d = json.load(f)
d.setdefault('tarball', {})
d['tarball']['sha256'] = 'sha256:$TAR_SHA'
d['tarball']['filename'] = '$TARBALL_NAME'
with open('$BUNDLE_DIR/manifest.json', 'w') as f:
    json.dump(d, f, indent=2, sort_keys=True); f.write('\n')
"
  log "  updated manifest.tarball.sha256 (was TBD)"
fi

ok "wrote $TARBALL_PATH ($(du -h "$TARBALL_PATH" | awk '{print $1}'))"
ok "wrote $SHA_PATH"
log ""
log "to ship:"
log "  rsync -avP $TARBALL_PATH $SHA_PATH your-cdn:/path/to/agents/"