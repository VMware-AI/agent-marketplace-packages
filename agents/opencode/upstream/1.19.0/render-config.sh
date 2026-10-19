#!/usr/bin/env bash
# render-config.sh — schema-driven renderer for opencode.json.
# Invoked by `agentpkg config generate opencode`.
#
# Two paths:
#
#   1. NEW (schema-driven; agent-marketplace-packages PR Phase 3):
#      reads $MARKETPLACE_CONFIG_OPENCODE (base64-encoded JSON, set
#      by the OVA firstboot from the platform's
#      guestinfo.marketplace_config.opencode guestinfo key), decodes,
#      validates, and writes verbatim to $RENDER_TO. This is the
#      contract for any package version that ships a config/ directory
#      in its tarball.
#
#   2. LEGACY (manifest-driven; pre-Phase-3 tarballs):
#      reads $AGENT_MARKETPLACE_CONFIG_INPUT (a JSON file the daemon
#      writes containing the marketplace_inputs.<key>=<value> pairs)
#      and walks the manifest's required_inputs / optional_inputs
#      jsonPaths to assemble opencode.json. Kept as a fallback so a
#      Phase-2-and-earlier opencode tarball continues to work until
#      every operator has upgraded to a Phase-3-aware version.
#      Removed in Phase 5 of the agent-platform-console refactor.
#
# Inputs (env vars):
#   $AGENT_MARKETPLACE_CONFIG_INPUT    — path to daemon's config-input JSON
#                                        (LEGACY path only)
#   $AGENT_MARKETPLACE_MANIFEST        — path to manifest.json (LEGACY path only)
#   $AGENT_MARKETPLACE_AGENT           — "opencode"
#   $AGENT_MARKETPLACE_VERSION         — e.g. "1.19.0"
#   $AGENT_MARKETPLACE_DEPLOY_ROOT     — e.g. "$HOME/.local/opencode/1.19.0"
#   $MARKETPLACE_CONFIG_OPENCODE       — base64(JSON), set by OVA firstboot
#                                        from guestinfo.marketplace_config.opencode
#                                        (NEW path)
#
# Output:
#   Path declared by manifest.configs[0].render_to, default
#   ~/.config/opencode/opencode.json (mode 0600).
#
# Exit codes:
#   0  — success
#   1  — internal error (env / manifest missing)
#   70 — required-input missing or invalid
#   71 — script error (bad type / enum value / bad JSON)

set -euo pipefail

: "${AGENT_MARKETPLACE_AGENT:?AGENT_MARKETPLACE_AGENT is required}"
: "${AGENT_MARKETPLACE_VERSION:?AGENT_MARKETPLACE_VERSION is required}"

SLOT_NAME="opencode"
RENDER_TO="$HOME/.config/opencode/opencode.json"
MODE="0600"

# =============================================================================
# NEW path: schema-driven raw config blob.
# =============================================================================
if [[ -n "${MARKETPLACE_CONFIG_OPENCODE:-}" ]]; then
  # The base64 round-trip is a cheap sanity check; the in-VM `jq empty`
  # is the source of truth on whether the decoded bytes are valid JSON.
  decoded="$(printf '%s' "$MARKETPLACE_CONFIG_OPENCODE" | base64 -d)"
  if ! printf '%s' "$decoded" | jq empty 2>/dev/null; then
    echo "render-config.sh [opencode]: MARKETPLACE_CONFIG_OPENCODE is not valid JSON" >&2
    exit 71
  fi
  mkdir -p "$(dirname "$RENDER_TO")"
  printf '%s' "$decoded" | jq . > "$RENDER_TO"
  chmod "$MODE" "$RENDER_TO"
  echo "render-config.sh [opencode]: wrote $RENDER_TO from MARKETPLACE_CONFIG_OPENCODE (new schema-driven path)"
  exit 0
fi

# =============================================================================
# LEGACY path: manifest-driven (kept for backward compat with pre-Phase-3
# tarballs; removed in Phase 5).
# =============================================================================
: "${AGENT_MARKETPLACE_CONFIG_INPUT:?AGENT_MARKETPLACE_CONFIG_INPUT is required (legacy path)}"
: "${AGENT_MARKETPLACE_MANIFEST:?AGENT_MARKETPLACE_MANIFEST is required (legacy path)}"

INPUT="$AGENT_MARKETPLACE_CONFIG_INPUT"
MF="$AGENT_MARKETPLACE_MANIFEST"

if [[ ! -f "$INPUT" ]]; then
  echo "render-config.sh [opencode]: config input not found: $INPUT" >&2
  exit 1
fi
if [[ ! -f "$MF" ]]; then
  echo "render-config.sh [opencode]: manifest not found: $MF" >&2
  exit 1
fi

# Locate the configs[0] block for this agent. There is currently only one
# configs[] entry per agent, so pick the first.
jq -e '.configs[0]' "$MF" >/dev/null || {
  echo "render-config.sh [opencode]: manifest.configs[0] missing" >&2; exit 1; }

# --- 1. required: OPENCODE_PROVIDER ------------------------------------------
PROVIDER=$(jq -r '.OPENCODE_PROVIDER // empty' "$INPUT")
if [[ -z "$PROVIDER" ]]; then
  echo "render-config.sh [opencode]: missing OPENCODE_PROVIDER in input" >&2
  exit 70
fi

# Lookup in supported_providers.builtin[].id
BUILTIN_IDS=$(jq -r '.configs[0].supported_providers.builtin[].id' "$MF")
IS_BUILTIN=false
while IFS= read -r id; do
  [[ -z "$id" ]] && continue
  if [[ "$id" == "$PROVIDER" ]]; then IS_BUILTIN=true; break; fi
done <<< "$BUILTIN_IDS"

CUSTOM_NPM_DEFAULT=$(jq -r '.configs[0].supported_providers.custom.npm_default // empty' "$MF")

if $IS_BUILTIN; then
  PROVIDER_NPM=""
  PROVIDER_BASE_URL=""
else
  # Custom provider path
  PROVIDER_NPM=$(jq -r '.OPENCODE_PROVIDER_NPM // empty' "$INPUT")
  PROVIDER_BASE_URL=$(jq -r '.OPENCODE_PROVIDER_BASE_URL // empty' "$INPUT")
  if [[ -z "$PROVIDER_NPM" || -z "$PROVIDER_BASE_URL" ]]; then
    echo "render-config.sh [opencode]: custom provider '$PROVIDER' requires OPENCODE_PROVIDER_NPM and OPENCODE_PROVIDER_BASE_URL" >&2
    exit 70
  fi
  if [[ -z "$CUSTOM_NPM_DEFAULT" ]]; then
    :  # no default declared; require explicit
  fi
fi

# --- 2. required: <PROVIDER>_API_KEY -----------------------------------------
if $IS_BUILTIN; then
  KEY_ENV=$(jq -r --arg id "$PROVIDER" \
    '.configs[0].supported_providers.builtin[] | select(.id == $id) | .api_key_env // empty' \
    "$MF")
  if [[ -n "$KEY_ENV" ]]; then
    API_KEY=$(jq -r --arg k "$KEY_ENV" '.[$k] // empty' "$INPUT")
  fi
else
  KEY_ENV="$(printf '%s' "$PROVIDER" | tr '[:lower:]' '[:upper:]')_API_KEY"
  API_KEY=$(jq -r --arg k "$KEY_ENV" '.[$k] // empty' "$INPUT")
fi

if $IS_BUILTIN && [[ -z "$KEY_ENV" ]]; then
  :  # provider has no key (e.g. ollama); skip key check
elif [[ -z "$API_KEY" ]]; then
  echo "render-config.sh [opencode]: missing API key — looked for ${KEY_ENV} in input" >&2
  exit 70
fi

# --- 3. validate optional_inputs (enum + required_when + types) -------------
set -e
EXTRA_REQUIRED=$(jq -r --arg provider "$PROVIDER" --arg is_builtin "$IS_BUILTIN" '
  [.configs[0].optional_inputs
   | to_entries[]
   | select(.value.required_when != null)
   | select(
       (.value.required_when == "custom_provider" and $is_builtin == "false") or
       (.value.required_when == "builtin_provider"  and $is_builtin == "true")
     )
   | .key]
  | .[]
' "$MF")

for key in $EXTRA_REQUIRED; do
  val=$(jq -r --arg k "$key" '.[$k] // empty' "$INPUT")
  if [[ -z "$val" ]]; then
    echo "render-config.sh [opencode]: required_when '$key' is missing" >&2
    exit 70
  fi
done

ENUM_ERRORS=$(jq -r --argjson input "$(cat "$INPUT")" '
  [.configs[0].optional_inputs | to_entries[] | select(.value.type == "enum") | {key: .key, allowed: .value.enum_values}]
  | .[] as $f
  | (try $input[$f.key] catch null) as $val
  | if $val != null and ($val | tostring | (. as $v | ($f.allowed | index($v)) == null)) then
      "\($f.key)=\($val) is not a valid value; allowed: \($f.allowed | join(","))"
    else empty
  end
' "$MF")
if [[ -n "$ENUM_ERRORS" ]]; then
  echo "render-config.sh [opencode]: $ENUM_ERRORS" >&2
  exit 71
fi

# --- 4. build opencode.json --------------------------------------------------
mkdir -p "$HOME/.config/opencode"
OUT="$HOME/.config/opencode/opencode.json"

jq -n \
  --arg provider "$PROVIDER" \
  --arg is_builtin "$IS_BUILTIN" \
  --arg provider_npm "$PROVIDER_NPM" \
  --arg provider_base_url "$PROVIDER_BASE_URL" \
  --arg provider_api_key  "${API_KEY:-}" \
  --argjson input "$(cat "$INPUT")" \
  --argjson config "$(jq -c '.configs[0]' "$MF")" \
  '
  def sp(provider; p):
    p | split(".") | map(if . == "<name>" then provider else . end);

  def coerce(type; raw):
    if raw == null or raw == "" then null
    elif type == "integer"            then (try (raw | tonumber) catch null)
    elif type == "boolean"            then (raw == "true")
    elif type == "json_array" or type == "json_object" then
      (try (raw | fromjson) catch raw)
    else
      raw
    end;

  reduce ($config.optional_inputs | to_entries[]) as $f ({}; . as $acc |
    ($f.key) as $k | ($f.value) as $schema | ($input[$k] // null) as $raw |
    ($schema.type) as $t | ($schema.json_path) as $jp |
    ($jp | sp($provider; .)) as $p |
    ($raw | coerce($t; .)) as $val |
    if $val == null then $acc else $acc | setpath($p; $val) end
  )
  | . as $base
  | (if $is_builtin == "true" then
       { ($provider): { options: { apiKey: $provider_api_key } } }
     else
       { ($provider): {
           npm:     $provider_npm,
           options: { baseURL: $provider_base_url, apiKey: $provider_api_key }
         } }
     end) as $prov
  | $base | .provider = $prov | ."$schema" = "https://opencode.ai/config.json"
  ' > "$OUT"

chmod 0600 "$OUT"
echo "render-config.sh [opencode]: wrote $OUT (legacy manifest-driven path)"
