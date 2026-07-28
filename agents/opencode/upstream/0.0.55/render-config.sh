#!/usr/bin/env bash
# render-config.sh — read daemon's config input JSON, write opencode.json.
# Invoked by `agentpkg config generate opencode`.
#
# Inputs (env vars set by agentpkg):
#   $AGENT_MARKETPLACE_CONFIG_INPUT — path to daemon's JSON file
#   $AGENT_MARKETPLACE_AGENT        — "opencode"
#   $AGENT_MARKETPLACE_VERSION      — e.g. "0.0.55"
#   $AGENT_MARKETPLACE_DEPLOY_ROOT  — e.g. "$HOME/.local/opencode/0.0.55"
#
# Output:
#   $HOME/.config/opencode/opencode.json (mode 0600)
#
# Exit codes:
#   0 — success
#   1 — configuration error
set -euo pipefail

: "${AGENT_MARKETPLACE_CONFIG_INPUT:?AGENT_MARKETPLACE_CONFIG_INPUT is required}"

if [[ ! -f "$AGENT_MARKETPLACE_CONFIG_INPUT" ]]; then
  echo "render-config.sh [opencode]: config input not found: $AGENT_MARKETPLACE_CONFIG_INPUT" >&2
  exit 1
fi

PROVIDER=$(jq -r '.OPENCODE_PROVIDER // empty'  "$AGENT_MARKETPLACE_CONFIG_INPUT")
API_KEY=$(jq -r '.ANTHROPIC_API_KEY // empty'    "$AGENT_MARKETPLACE_CONFIG_INPUT")

if [[ -z "$PROVIDER" || -z "$API_KEY" ]]; then
  echo "render-config.sh [opencode]: missing OPENCODE_PROVIDER or ANTHROPIC_API_KEY in input" >&2
  exit 1
fi

mkdir -p "$HOME/.config/opencode"
cat > "$HOME/.config/opencode/opencode.json" <<EOF
{
  "provider": "${PROVIDER}",
  "api_key": "${API_KEY}"
}
EOF
chmod 0600 "$HOME/.config/opencode/opencode.json"