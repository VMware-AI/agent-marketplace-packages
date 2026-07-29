#!/usr/bin/env bash
# render-config.sh — read daemon's config input JSON, write opencode.jsonc.
# Invoked by `agentpkg config generate opencode`.
#
# Inputs (env vars set by agentpkg):
#   $AGENT_MARKETPLACE_CONFIG_INPUT — path to daemon's JSON file
#   $AGENT_MARKETPLACE_AGENT        — "opencode"
#   $AGENT_MARKETPLACE_VERSION      — e.g. "1.18.9"
#   $AGENT_MARKETPLACE_DEPLOY_ROOT  — e.g. "$HOME/.local/opencode/1.18.9"
#
# Output:
#   $HOME/.config/opencode/opencode.jsonc (mode 0600)
#
# opencode 1.18.9 (anomalyco/opencode) reads opencode.jsonc — JSON with
# optional $schema header. Schema:
#   {
#     "$schema": "https://opencode.ai/config.json",
#     "provider": {
#       "<provider_name>": {
#         "options": { "apiKey": "<key>" }
#       }
#     }
#   }
# Plus optional "model" key at the top level to pin the default model.
#
# Daemon supplies OPENCODE_PROVIDER (e.g. "anthropic") and the API key
# whose name matches that provider's convention (ANTHROPIC_API_KEY for
# anthropic, OPENAI_API_KEY for openai, GOOGLE_API_KEY for google, etc.).
# We map OPENCODE_PROVIDER -> expected env var via a small case table.
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

PROVIDER=$(jq -r '.OPENCODE_PROVIDER // empty' "$AGENT_MARKETPLACE_CONFIG_INPUT")
MODEL=$(jq -r '.OPENCODE_MODEL // empty' "$AGENT_MARKETPLACE_CONFIG_INPUT")

if [[ -z "$PROVIDER" ]]; then
  echo "render-config.sh [opencode]: missing OPENCODE_PROVIDER in input" >&2
  exit 1
fi

# Map provider name → expected API-key env var name (uppercase provider
# + _API_KEY is the convention for all four supported here). Use tr to
# uppercase for portability with bash 3.2 (macOS) — ${VAR^^} is bash 4+.
PROVIDER_KEY_VAR="$(printf '%s' "$PROVIDER" | tr '[:lower:]' '[:upper:]')_API_KEY"
API_KEY=$(jq -r --arg k "$PROVIDER_KEY_VAR" '.[$k] // empty' "$AGENT_MARKETPLACE_CONFIG_INPUT")

# Fallback: also accept ANTHROPIC_API_KEY for backward compatibility
# (the previous manifest expected ANTHROPIC_API_KEY regardless of provider).
if [[ -z "$API_KEY" ]]; then
  API_KEY=$(jq -r '.ANTHROPIC_API_KEY // empty' "$AGENT_MARKETPLACE_CONFIG_INPUT")
fi

if [[ -z "$API_KEY" ]]; then
  echo "render-config.sh [opencode]: missing API key — looked for $PROVIDER_KEY_VAR or ANTHROPIC_API_KEY in input" >&2
  exit 1
fi

mkdir -p "$HOME/.config/opencode"

# Build the JSONC. Use jq for safe escaping of values.
if [[ -n "$MODEL" ]]; then
  jq -n \
    --arg provider "$PROVIDER" \
    --arg model "$MODEL" \
    --arg key "$API_KEY" \
    '{ "$schema": "https://opencode.ai/config.json", model: $model, provider: { ($provider): { options: { apiKey: $key } } } }' \
    > "$HOME/.config/opencode/opencode.jsonc"
else
  jq -n \
    --arg provider "$PROVIDER" \
    --arg key "$API_KEY" \
    '{ "$schema": "https://opencode.ai/config.json", provider: { ($provider): { options: { apiKey: $key } } } }' \
    > "$HOME/.config/opencode/opencode.jsonc"
fi

chmod 0600 "$HOME/.config/opencode/opencode.jsonc"