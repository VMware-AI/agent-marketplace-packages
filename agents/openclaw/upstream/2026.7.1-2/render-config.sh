#!/usr/bin/env bash
# render-config.sh — manifest-driven renderer for openclaw.
# Invoked by `agentpkg config generate openclaw`.
#
# This script is entirely driven by $AGENT_MARKETPLACE_MANIFEST (the
# tarball's embedded manifest.json). Adding/changing a config field is a
# matter of editing manifest.json — no shell changes required.
#
# Inputs (env vars set by agentpkg):
#   $AGENT_MARKETPLACE_CONFIG_INPUT — path to daemon's config-input JSON
#   $AGENT_MARKETPLACE_MANIFEST     — path to manifest.json (extracted by CLI)
#   $AGENT_MARKETPLACE_AGENT        — "openclaw"
#   $AGENT_MARKETPLACE_VERSION      — e.g. "2026.7.1-2"
#   $AGENT_MARKETPLACE_DEPLOY_ROOT  — e.g. "$HOME/.local/openclaw/2026.7.1-2"
#
# Output:
#   ~/.openclaw/openclaw.json (mode 0600, JSON5-compatible pure JSON)
#
# Two-layer model:
#   - ADMIN-layer fields (provider/apiKey/gateway/diagnostics/...) are declared
#     in manifest.configs[0].required_inputs / optional_inputs.
#   - USER-layer (agents.list, plugins.entries, channels.*, ...) is OUT OF
#     SCOPE; users edit those directly after install.
#
# Exit codes:
#   0  — success
#   1  — internal error (env / input / manifest missing)
#   70 — required-input missing or invalid
#   71 — script error (bad type / enum / parse failure)
set -euo pipefail

: "${AGENT_MARKETPLACE_CONFIG_INPUT:?AGENT_MARKETPLACE_CONFIG_INPUT is required}"
: "${AGENT_MARKETPLACE_MANIFEST:?AGENT_MARKETPLACE_MANIFEST is required}"

if [[ ! -f "$AGENT_MARKETPLACE_CONFIG_INPUT" ]]; then
  echo "render-config.sh [openclaw]: config input not found: $AGENT_MARKETPLACE_CONFIG_INPUT" >&2
  exit 1
fi
if [[ ! -f "$AGENT_MARKETPLACE_MANIFEST" ]]; then
  echo "render-config.sh [openclaw]: manifest not found: $AGENT_MARKETPLACE_MANIFEST" >&2
  exit 1
fi

INPUT="$AGENT_MARKETPLACE_CONFIG_INPUT"
MF="$AGENT_MARKETPLACE_MANIFEST"
CFGI=0  # single configs[] entry

# Verify the configs entry is openclaw.json.
if ! jq -e ".configs[$CFGI].file == \"openclaw.json\"" "$MF" >/dev/null; then
  echo "render-config.sh [openclaw]: manifest.configs[0] is not openclaw.json" >&2
  exit 1
fi

# --- 1. required: OPENCLAW_PROVIDER --------------------------------------------
PROVIDER=$(jq -r '.OPENCLAW_PROVIDER // empty' "$INPUT")
if [[ -z "$PROVIDER" ]]; then
  echo "render-config.sh [openclaw]: missing OPENCLAW_PROVIDER in input" >&2
  exit 70
fi

BUILTIN_IDS=$(jq -r ".configs[$CFGI].supported_providers.builtin[].id" "$MF")
IS_BUILTIN=false
while IFS= read -r id; do
  [[ -z "$id" ]] && continue
  if [[ "$id" == "$PROVIDER" ]]; then IS_BUILTIN=true; break; fi
done <<< "$BUILTIN_IDS"

if $IS_BUILTIN; then
  PROVIDER_NPM=""
  PROVIDER_BASE_URL=""
else
  PROVIDER_NPM=$(jq -r '.OPENCLAW_PROVIDER_NPM // empty' "$INPUT")
  PROVIDER_BASE_URL=$(jq -r '.OPENCLAW_PROVIDER_BASE_URL // empty' "$INPUT")
  if [[ -z "$PROVIDER_BASE_URL" ]]; then
    echo "render-config.sh [openclaw]: custom provider '$PROVIDER' requires OPENCLAW_PROVIDER_BASE_URL" >&2
    exit 70
  fi
fi

# --- 2. required: <PROVIDER>_API_KEY -----------------------------------------
if $IS_BUILTIN; then
  KEY_ENV=$(jq -r --arg id "$PROVIDER" \
    ".configs[$CFGI].supported_providers.builtin[] | select(.id == \$id) | .api_key_env // empty" \
    "$MF")
  if [[ -n "$KEY_ENV" ]]; then
    API_KEY=$(jq -r --arg k "$KEY_ENV" '.[$k] // empty' "$INPUT")
  fi
else
  KEY_ENV="$(printf '%s' "$PROVIDER" | tr '[:lower:]' '[:upper:]')_API_KEY"
  API_KEY=$(jq -r --arg k "$KEY_ENV" '.[$k] // empty' "$INPUT")
fi

# OAuth providers (api_key_env == null) don't require a static API key.
IS_OAUTH=$(jq -r --arg id "$PROVIDER" \
  ".configs[$CFGI].supported_providers.builtin[] | select(.id == \$id) | .auth // \"api_key\"" \
  "$MF")

if [[ "$IS_OAUTH" != "oauth" && -n "$KEY_ENV" && -z "$API_KEY" ]]; then
  echo "render-config.sh [openclaw]: missing API key — looked for ${KEY_ENV} in input" >&2
  exit 70
fi

# --- 3. validate enum values upfront -----------------------------------------
ENUM_ERRORS=$(jq -c '
  .configs[0].optional_inputs // {} | to_entries[] | select(.value.type == "enum") |
  {key: .key, allowed: .value.enum_values}
' "$MF")
ENUM_ERR=""
while IFS= read -r json_row; do
  [[ -z "$json_row" ]] && continue
  k=$(echo "$json_row" | jq -r '.key')
  val=$(jq -r --arg k "$k" '.[$k] // null' "$INPUT")
  if [[ "$val" != "null" && "$val" != "" ]]; then
    if ! echo "$json_row" | jq -e --arg v "$val" '.allowed | index($v)' >/dev/null; then
      allowed_list=$(echo "$json_row" | jq -r '.allowed | join(",")')
      ENUM_ERR+="${ENUM_ERR:+; }$k=$val not allowed (allowed: $allowed_list)"
    fi
  fi
done <<< "$ENUM_ERRORS"
if [[ -n "$ENUM_ERR" ]]; then
  echo "render-config.sh [openclaw]: $ENUM_ERR" >&2
  exit 71
fi

# --- 4. validate required_when conditions -------------------------------------
REQ_WHEN_ERR=""
REQUIRED_WHEN_KEYS=$(jq -r ".configs[$CFGI].optional_inputs | to_entries[] | select(.value.required_when != null) | .key" "$MF")
for k in $REQUIRED_WHEN_KEYS; do
  val=$(jq -r --arg k "$k" '.[$k] // empty' "$INPUT")
  required_when=$(jq -r --arg k "$k" ".configs[$CFGI].optional_inputs[\$k].required_when" "$MF")
  needed=false
  case "$required_when" in
    custom_provider) $IS_BUILTIN || needed=true ;;
    builtin_provider) $IS_BUILTIN && needed=true ;;
  esac
  if $needed && [[ -z "$val" ]]; then
    REQ_WHEN_ERR+="${REQ_WHEN_ERR:+; }$k (required_when=$required_when) missing"
  fi
done
if [[ -n "$REQ_WHEN_ERR" ]]; then
  echo "render-config.sh [openclaw]: $REQ_WHEN_ERR" >&2
  exit 70
fi

# --- 5. build openclaw.json (pure JSON) ---------------------------------------
mkdir -p "$HOME/.openclaw"
OUT="$HOME/.openclaw/openclaw.json"
JSON_TMP="$(mktemp -t openclaw-json.XXXXXX)"
trap 'rm -f "$JSON_TMP"' EXIT

# Build the JSON tree from optional_inputs + required_inputs. Path syntax:
#   "a.b.c"            → setpath(["a","b","c"]; v)
#   "models.providers.<name>.x" → setpath(["models","providers", $provider, "x"]; v)
jq -n \
  --arg provider "$PROVIDER" \
  --arg is_builtin "$IS_BUILTIN" \
  --arg provider_npm "$PROVIDER_NPM" \
  --arg provider_base_url "$PROVIDER_BASE_URL" \
  --arg provider_api_key  "${API_KEY:-}" \
  --argjson input "$(cat "$INPUT")" \
  --argjson config "$(jq -c ".configs[$CFGI]" "$MF")" \
  '
  def sp(provider; p):
    p | split(".") | map(if . == "<name>" then provider else . end);

  def coerce(type; raw):
    if raw == null or raw == "" then null
    elif type == "integer"            then (try (raw | tonumber) catch null)
    elif type == "boolean"            then (raw == "true")
    elif type == "number"             then (try (raw | tonumber) catch null)
    elif type == "json_array" or type == "json_object" then
      (try (raw | fromjson) catch raw)
    else raw
    end;

  # Build the field list from both optional_inputs (object) and required_inputs
  # (array of objects with input_key). Each normalized to [k, schema].
  ($config.optional_inputs // {} | to_entries | map([.key, .value])) as $opt_pairs |
  ($config.required_inputs // [] | map([.input_key, .])) as $req_pairs |
  ($opt_pairs + $req_pairs) as $all_pairs |
  reduce $all_pairs[] as $pair ({}; . as $acc |
    ($pair[0]) as $k | ($pair[1]) as $schema | ($input[$k] // null) as $raw |
    ($schema.type) as $t | ($schema.json_path) as $jp |
    ($jp | sp($provider; .)) as $p |
    ($raw | coerce($t; .)) as $val |
    if $val == null or ($p // []) == [] then $acc else $acc | setpath($p; $val) end
  )
  | . as $base
  | (if $is_builtin == "true" then
       { ($provider): { apiKey: $provider_api_key } }
     else
       {
         ($provider): {
           baseUrl: $provider_base_url,
           apiKey: $provider_api_key,
           localService: { command: $provider_npm }
         }
       }
     end) as $prov
  | $base
  | .models = (if ($base.models // null) == null then {providers: $prov}
               else ($base.models + {providers: ($base.models.providers + $prov)})
               end)
  ' > "$JSON_TMP"

# Pretty-print with stable key order.
jq -S . "$JSON_TMP" > "$OUT"
chmod 0600 "$OUT"