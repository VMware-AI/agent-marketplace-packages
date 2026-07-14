#!/usr/bin/env bash
# sign.sh — produce a GPG detached signature for a tarball
#
# Usage:
#   tools/sign.sh <agent> <source> <version> [in-dir]
#
# Requires gpg to be on PATH. The signing key is whichever key is default for
# the invoking user (gpg --default-key). Use --key <fpr> via GPG_ARGS env var
# to override.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
cd "$REPO_ROOT"

log()  { echo "[sign] $*" >&2; }
err()  { echo "[sign] ERR: $*" >&2; exit 1; }

[[ $# -ge 3 ]] || { echo "usage: $0 <agent> <source> <version> [in-dir]" >&2; exit 1; }
AGENT="$1"; SOURCE="$2"; VERSION="$3"; IN_DIR="${4:-dist}"

command -v gpg >/dev/null 2>&1 || err "gpg not found on PATH — install with: apt-get install -y gnupg"

TARBALL_NAME="${AGENT}-${SOURCE}-${VERSION}.tar.gz"
TARBALL_PATH="$IN_DIR/$TARBALL_NAME"
SIG_PATH="$TARBALL_PATH.sig"

[[ -f "$TARBALL_PATH" ]] || err "no tarball at $TARBALL_PATH (run tools/pack.sh first)"

log "signing $TARBALL_PATH"
gpg --batch --yes --armor --detach-sign \
    --output "$SIG_PATH" \
    ${GPG_ARGS:-} \
    "$TARBALL_PATH" \
  || err "gpg signing failed"

log "wrote $SIG_PATH"
echo
echo "Consumers verify with:"
echo "  gpg --verify $SIG_PATH $TARBALL_NAME"