#!/usr/bin/env bash
# render-config.sh — read daemon's config input JSON, write hermes .env file.
# Invoked by `agentpkg config generate hermes-agent`.
#
# Inputs (env vars set by agentpkg):
#   $AGENT_MARKETPLACE_CONFIG_INPUT — path to daemon's JSON file
#   $AGENT_MARKETPLACE_AGENT        — "hermes-agent"
#   $AGENT_MARKETPLACE_VERSION      — e.g. "0.18.2"
#   $AGENT_MARKETPLACE_DEPLOY_ROOT  — e.g. "$HOME/.local/hermes-agent/0.18.2"
#
# Output:
#   $HOME/.hermes/.env (mode 0600)
#
# Exit codes:
#   0 — success
#   1 — configuration error
set -euo pipefail

: "${AGENT_MARKETPLACE_CONFIG_INPUT:?AGENT_MARKETPLACE_CONFIG_INPUT is required}"

if [[ ! -f "$AGENT_MARKETPLACE_CONFIG_INPUT" ]]; then
  echo "render-config.sh [hermes-agent]: config input not found: $AGENT_MARKETPLACE_CONFIG_INPUT" >&2
  exit 1
fi

PROVIDER=$(jq -r '.HERMES_PROVIDER // empty'   "$AGENT_MARKETPLACE_CONFIG_INPUT")
API_KEY=$(jq -r '.OPENAI_API_KEY // empty'      "$AGENT_MARKETPLACE_CONFIG_INPUT")

if [[ -z "$PROVIDER" || -z "$API_KEY" ]]; then
  echo "render-config.sh [hermes-agent]: missing HERMES_PROVIDER or OPENAI_API_KEY in input" >&2
  exit 1
fi

mkdir -p "$HOME/.hermes"
cat > "$HOME/.hermes/.env" <<EOF
HERMES_PROVIDER=${PROVIDER}
OPENAI_API_KEY=${API_KEY}
EOF
chmod 0600 "$HOME/.hermes/.env"