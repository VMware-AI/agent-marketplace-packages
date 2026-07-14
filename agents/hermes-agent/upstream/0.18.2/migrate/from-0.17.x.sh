#!/usr/bin/env bash
# from-0.17.x.sh — migration shim from hermes-agent 0.17.x → 0.18.x
#
# Placeholder — no breaking changes identified yet between 0.17.x and 0.18.x.
# If a future 0.18.x release breaks user config / data, add the conversion
# logic here.

set -euo pipefail

AGENT="hermes-agent"
VERSION="0.18.2"
TARGET_ROOT="${AGENT_MARKETPLACE_TARGET_ROOT:-$HOME/.local}"
STATE_FILE="$TARGET_ROOT/state/hermes-agent.state.json"

PREV=$(jq -r '.version // empty' "$STATE_FILE")
log() { echo "[$AGENT/$VERSION migrate from $PREV] $*" >&2; }

if [[ -z "$PREV" ]]; then
  log "no prior state — skipping"
  exit 0
fi

log "no data migration needed between 0.17.x and 0.18.x (compatible drop-in)"
exit 0