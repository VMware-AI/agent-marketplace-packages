#!/usr/bin/env bash
# from-2026.7.x.sh — migration shim from any openclaw 2026.7.x prior version
#
# At time of writing, 2026.7.2 is the first 2026.7.x release we package.
# No state migration needed: the deploy_root uses the version segment so
# 2026.7.1 → 2026.7.2 just installs the new deploy_root alongside (if any)
# and removes the old one in uninstall.

set -euo pipefail

AGENT="openclaw"
VERSION="2026.7.2"
TARGET_ROOT="${AGENT_MARKETPLACE_TARGET_ROOT:-$HOME/.local}"
STATE_FILE="$TARGET_ROOT/state/openclaw.state.json"

PREV=$(jq -r '.version // empty' "$STATE_FILE")
log() { echo "[$AGENT/$VERSION migrate from $PREV] $*" >&2; }
fail() { echo "[$AGENT/$VERSION migrate] FAIL: $*" >&2; exit 1; }

if [[ -z "$PREV" ]]; then
  log "no prior state — skipping"
  exit 0
fi

log "no data migration needed between 2026.7.x patch releases"
log "  -> install.sh will deploy $VERSION; uninstall.sh removes the old deploy_root if empty"

# Future migrations slot in here. For example, if openclaw ever changes its
# config schema between patch versions, this is where we transform $HOME/.openclaw/.
exit 0
