#!/usr/bin/env bash
# from-0.18.x.sh — migration shim from any hermes-agent 0.18.x prior version
#
# Patch-level upgrades within 0.18.x don't need data migration:
# hermes-agent stores user data under $HOME/.local/share/hermes-agent/ and
# $HOME/.config/hermes-agent/ which carry over unchanged between patches.
# Config schema has been stable in the 0.18.x series.

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

log "no data migration needed between 0.18.x patch releases"
log "  install.sh will deploy $VERSION; user data under ~/.local/share/hermes-agent is preserved"
exit 0