#!/usr/bin/env bash
# render-config.sh — schema-driven renderer for openclaw.json.
# Invoked by `agentpkg config generate openclaw`.
#
# Reads $MARKETPLACE_CONFIG_OPENCLAW (base64-encoded JSON, set by
# the OVA firstboot from the platform's
# guestinfo.marketplace_config.openclaw guestinfo key),
# decodes, validates, and writes verbatim to $RENDER_TO.
# Post-Phase-5 contract — see agents/opencode/.../render-config.sh
# for the same one-liner rationale.
#
# Output:
#   ~/.openclaw/openclaw.json (mode 0600, pure JSON).
#
# Exit codes:
#   0  — success
#   1  — env var missing
#   71 — base64 decode or JSON validation failed
set -euo pipefail

: "${AGENT_MARKETPLACE_AGENT:?AGENT_MARKETPLACE_AGENT is required}"
: "${AGENT_MARKETPLACE_VERSION:?AGENT_MARKETPLACE_VERSION is required}"
: "${MARKETPLACE_CONFIG_OPENCLAW:?MARKETPLACE_CONFIG_OPENCLAW is required (the OVA firstboot must export it from guestinfo.marketplace_config.openclaw)}"

RENDER_TO="$HOME/.openclaw/openclaw.json"
MODE="0600"

decoded="$(printf '%s' "$MARKETPLACE_CONFIG_OPENCLAW" | base64 -d)"
if ! printf '%s' "$decoded" | jq empty 2>/dev/null; then
  echo "render-config.sh [openclaw]: MARKETPLACE_CONFIG_OPENCLAW is not valid JSON" >&2
  exit 71
fi

mkdir -p "$(dirname "$RENDER_TO")"
printf '%s' "$decoded" | jq . > "$RENDER_TO"
chmod "$MODE" "$RENDER_TO"
echo "render-config.sh [openclaw]: wrote $RENDER_TO (new schema-driven path)"
