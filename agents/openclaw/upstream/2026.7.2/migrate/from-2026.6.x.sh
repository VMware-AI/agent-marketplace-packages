#!/usr/bin/env bash
# from-2026.6.x.sh — migration shim from any openclaw 2026.6.x prior version
#
# This is a placeholder for a future breaking-change migration.
# At present, upgrading across minor versions (2026.6.x → 2026.7.2) is
# treated as compatible — openclaw's data directory ($HOME/.openclaw or
# XDG-equivalent) carries over unchanged. If a future release breaks that,
# add the conversion logic here.

set -euo pipefail

AGENT="openclaw"
VERSION="2026.7.2"
TARGET_ROOT="${AGENT_MARKETPLACE_TARGET_ROOT:-$HOME/.local}"
STATE_FILE="$TARGET_ROOT/state/openclaw.state.json"

PREV=$(jq -r '.version // empty' "$STATE_FILE")
log() { echo "[$AGENT/$VERSION migrate from $PREV] $*" >&2; }

if [[ -z "$PREV" ]]; then
  log "no prior state — skipping"
  exit 0
fi

log "no data migration needed between 2026.6.x and 2026.7.x (compatible drop-in)"
log "  install.sh will deploy $VERSION; any user-config under ~/.config/openclaw is preserved"
exit 0
