#!/usr/bin/env bash
# render-config.sh — schema-driven renderer for opencode.json.
# Invoked by `agentpkg config generate opencode`.
#
# Reads $MARKETPLACE_CONFIG_OPENCODE (base64-encoded JSON, set by
# the OVA firstboot from the platform's
# guestinfo.marketplace_config.opencode guestinfo key),
# decodes, validates, and writes verbatim to $RENDER_TO.
# This is the post-Phase-5 contract: the upstream
# agent-marketplace-packages no longer ships the legacy
# jsonPath-walking path. Every opencode tarball version
# 1.19.0+ ships a config/ directory and uses the new shape.
#
# Output:
#   ~/.config/opencode/opencode.json (mode 0600, pure JSON).
#
# Exit codes:
#   0  — success
#   1  — env var missing
#   71 — base64 decode or JSON validation failed
set -euo pipefail

: "${AGENT_MARKETPLACE_AGENT:?AGENT_MARKETPLACE_AGENT is required}"
: "${AGENT_MARKETPLACE_VERSION:?AGENT_MARKETPLACE_VERSION is required}"
: "${MARKET_MARKETPLACE_CONFIG_OPENCODE_VAR:-${MARKETPLACE_CONFIG_OPENCODE:-}}"
: "${MARKETPLACE_CONFIG_OPENCODE:?MARKETPLACE_CONFIG_OPENCODE is required (the OVA firstboot must export it from guestinfo.marketplace_config.opencode)}"

SLOT_NAME="opencode"
RENDER_TO="$HOME/.config/opencode/opencode.json"
MODE="0600"

decoded="$(printf '%s' "$MARKETPLACE_CONFIG_OPENCODE" | base64 -d)"
if ! printf '%s' "$decoded" | jq empty 2>/dev/null; then
  echo "render-config.sh [opencode]: MARKETPLACE_CONFIG_OPENCODE is not valid JSON" >&2
  exit 71
fi

mkdir -p "$(dirname "$RENDER_TO")"
printf '%s' "$decoded" | jq . > "$RENDER_TO"
chmod "$MODE" "$RENDER_TO"
echo "render-config.sh [opencode]: wrote $RENDER_TO (new schema-driven path)"
