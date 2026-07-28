#!/usr/bin/env bash
# render-config.sh — read daemon's config input JSON, write openclaw.json.
# Invoked by `agentpkg config generate openclaw` (Stage 1 of the install
# flow). Daemon writes a JSON file with the user-supplied keys; this
# script reads them and writes openclaw's upstream-canonical config file.
#
# Inputs (env vars set by agentpkg):
#   $AGENT_MARKETPLACE_CONFIG_INPUT — absolute path to daemon's JSON file
#   $AGENT_MARKETPLACE_AGENT        — "openclaw"
#   $AGENT_MARKETPLACE_VERSION      — e.g. "2026.7.1-2"
#   $AGENT_MARKETPLACE_DEPLOY_ROOT  — e.g. "$HOME/.local/openclaw/2026.7.1-2"
#
# Output:
#   $HOME/.openclaw/openclaw.json (mode 0600)
#
# Exit codes:
#   0 — success
#   1 — configuration error (input file missing, missing required key)
set -euo pipefail

: "${AGENT_MARKETPLACE_CONFIG_INPUT:?AGENT_MARKETPLACE_CONFIG_INPUT is required}"

if [[ ! -f "$AGENT_MARKETPLACE_CONFIG_INPUT" ]]; then
  echo "render-config.sh [openclaw]: config input not found: $AGENT_MARKETPLACE_CONFIG_INPUT" >&2
  exit 1
fi

PROVIDER=$(jq -r '.OPENCLAW_PROVIDER // empty' "$AGENT_MARKETPLACE_CONFIG_INPUT")
API_KEY=$(jq -r '.ANTHROPIC_API_KEY // empty'   "$AGENT_MARKETPLACE_CONFIG_INPUT")

if [[ -z "$PROVIDER" || -z "$API_KEY" ]]; then
  echo "render-config.sh [openclaw]: missing OPENCLAW_PROVIDER or ANTHROPIC_API_KEY in input" >&2
  exit 1
fi

mkdir -p "$HOME/.openclaw"
cat > "$HOME/.openclaw/openclaw.json" <<EOF
{
  "agent": {
    "model": "${PROVIDER}/claude-sonnet"
  }
}
EOF
chmod 0600 "$HOME/.openclaw/openclaw.json"