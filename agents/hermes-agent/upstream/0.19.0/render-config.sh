#!/usr/bin/env bash
# render-config.sh — schema-driven renderer for hermes-agent.
# Invoked by `agentpkg config generate hermes-agent`.
#
# Two paths:
#
#   1. NEW (schema-driven; agent-marketplace-packages PR Phase 3):
#      reads $MARKETPLACE_CONFIG_HERMES (YAML/JSON, slot 1) and
#      $MARKETPLACE_CONFIG_HERMES_ENV (KEY=VALUE, slot 2) env vars
#      (set by the OVA firstboot from the platform's
#      guestinfo.marketplace_config.<slot> guestinfo keys),
#      decodes, validates, and writes verbatim to the slot's
#      render_to path. This is the contract for any package
#      version that ships a config/ directory in its tarball.
#
#   2. LEGACY (manifest-driven; pre-Phase-3 tarballs):
#      reads $AGENT_MARKETPLACE_CONFIG_INPUT (a JSON file the daemon
#      writes containing the marketplace_inputs.<key>=<value> pairs)
#      and walks the manifest's required_inputs / optional_inputs
#      jsonPaths to assemble the per-slot files. Kept as a fallback
#      so a Phase-2-and-earlier hermes-agent tarball continues to
#      work until every operator has upgraded to a Phase-3-aware
#      version. Removed in Phase 5 of the agent-platform-console
#      refactor.
#
# Inputs (env vars):
#   $AGENT_MARKETPLACE_CONFIG_INPUT    — daemon's config-input JSON (LEGACY)
#   $AGENT_MARKETPLACE_MANIFEST        — path to manifest.json (LEGACY)
#   $AGENT_MARKETPLACE_AGENT           — "hermes-agent"
#   $AGENT_MARKETPLACE_VERSION         — e.g. "0.19.0"
#   $AGENT_MARKETPLACE_DEPLOY_ROOT     — e.g. "$HOME/.local/hermes-agent/0.19.0"
#   $MARKETPLACE_CONFIG_HERMES         — base64(JSON), hermes slot (NEW)
#   $MARKETPLACE_CONFIG_HERMES_ENV     — base64(JSON), hermes-env slot (NEW)
#
# Outputs:
#   - ~/.hermes/config.yaml  (YAML, slot 1)
#   - ~/.hermes/.env         (KEY=VALUE, slot 2)
#
# Two-layer model:
#   - ADMIN-layer fields (provider/apiKey/model/compression/...) are
#     declared in the schema/manifest.
#   - USER-layer (SOUL.md, skills/, memories/, cron/, sessions/) is OUT
#     OF SCOPE; users create those directly.
#
# Exit codes:
#   0  — success
#   1  — internal error (env / manifest missing)
#   70 — required-input missing or invalid
#   71 — script error (bad type / bad JSON / pyyaml unavailable)
set -euo pipefail

: "${AGENT_MARKETPLACE_AGENT:?AGENT_MARKETPLACE_AGENT is required}"
: "${AGENT_MARKETPLACE_VERSION:?AGENT_MARKETPLACE_VERSION is required}"

# =============================================================================
# NEW path: schema-driven raw config blobs.
# =============================================================================
# write_slot decodes the base64 JSON blob and writes it in the format
# implied by the render_to filename extension:
#   - *.yaml / *.yml  → use yq to convert JSON → YAML (falls back to
#                        a hand-rolled python one-liner if yq is not
#                        installed)
#   - .env            → flatten the JSON object to KEY=VALUE lines
#   - anything else   → assume JSON, write verbatim
write_slot() {
  local slot_name="$1"
  local env_var="$2"
  local render_to="$3"
  local mode="$4"
  if [[ -z "${!env_var:-}" ]]; then
    return 1
  fi
  local decoded
  decoded="$(printf '%s' "${!env_var}" | base64 -d)"
  if ! printf '%s' "$decoded" | jq empty 2>/dev/null; then
    echo "render-config.sh [hermes-agent]: $env_var is not valid JSON" >&2
    exit 71
  fi
  mkdir -p "$(dirname "$render_to")"
  case "$render_to" in
    *.yaml|*.yml)
      if command -v yq >/dev/null 2>&1; then
        printf '%s' "$decoded" | yq -P > "$render_to"
      elif command -v python3 >/dev/null 2>&1; then
        printf '%s' "$decoded" | python3 -c "import sys, json, yaml; sys.stdout.write(yaml.safe_dump(json.load(sys.stdin), default_flow_style=False, sort_keys=False))" > "$render_to"
      else
        echo "render-config.sh [hermes-agent]: no yq or python3+yaml for $render_to" >&2
        exit 71
      fi
      ;;
    .env|*.env)
      # Flatten the JSON object to KEY=VALUE lines. Strings keep their
      # value (single-quoted for safety); numbers/booleans go in raw
      # form. Nested objects are JSON-stringified (the .env format
      # doesn't support nesting; the hermes daemon's loader expects
      # only flat env vars in this file).
      printf '%s' "$decoded" | jq -r 'to_entries | .[] | (.key) + "=" + (if (.value | type) == "string" then (.value | tojson) else (.value | tostring) end)' > "$render_to"
      ;;
    *)
      printf '%s' "$decoded" | jq . > "$render_to"
      ;;
  esac
  chmod "$mode" "$render_to"
  echo "render-config.sh [hermes-agent]: wrote $render_to (new schema-driven path)"
  return 0
}

new_path_used=0
if write_slot "hermes"     "MARKETPLACE_CONFIG_HERMES"     "$HOME/.hermes/config.yaml" 0600; then new_path_used=1; fi
if write_slot "hermes-env" "MARKETPLACE_CONFIG_HERMES_ENV" "$HOME/.hermes/.env"        0600; then new_path_used=1; fi
if [[ "$new_path_used" == "1" ]]; then
  exit 0
fi

# =============================================================================
# LEGACY path: manifest-driven (kept for backward compat with pre-Phase-3
# tarballs; removed in Phase 5).
# =============================================================================
: "${AGENT_MARKETPLACE_CONFIG_INPUT:?AGENT_MARKETPLACE_CONFIG_INPUT is required (legacy path)}"
: "${AGENT_MARKETPLACE_MANIFEST:?AGENT_MARKETPLACE_MANIFEST is required (legacy path)}"

if [[ ! -f "$AGENT_MARKETPLACE_CONFIG_INPUT" ]]; then
  echo "render-config.sh [hermes-agent]: config input not found: $AGENT_MARKETPLACE_CONFIG_INPUT" >&2
  exit 1
fi
if [[ ! -f "$AGENT_MARKETPLACE_MANIFEST" ]]; then
  echo "render-config.sh [hermes-agent]: manifest not found: $AGENT_MARKETPLACE_MANIFEST" >&2
  exit 1
fi

INPUT="$AGENT_MARKETPLACE_CONFIG_INPUT"
MF="$AGENT_MARKETPLACE_MANIFEST"

# Locate both configs[] entries. Currently two: index 0 = config.yaml,
# index 1 = .env. The pattern '.. | objects | select(.file == "X")' is
# resilient to future reordering.
YAML_INDEX=$(jq -r '.configs | to_entries | map(select(.value.file == "config.yaml")) | .[0].key // 0' "$MF")
ENV_INDEX=$(jq -r '.configs | to_entries | map(select(.value.file == ".env")) | .[0].key // 1' "$MF")

# Verify both exist.
if ! jq -e ".configs[$YAML_INDEX].file == \"config.yaml\"" "$MF" >/dev/null; then
  echo "render-config.sh [hermes-agent]: manifest.configs[$YAML_INDEX] is not config.yaml" >&2
  exit 1
fi
if ! jq -e ".configs[$ENV_INDEX].file == \".env\"" "$MF" >/dev/null; then
  echo "render-config.sh [hermes-agent]: manifest.configs[$ENV_INDEX] is not .env" >&2
  exit 1
fi

# --- 1. required: HERMES_PROVIDER --------------------------------------------
PROVIDER=$(jq -r '.HERMES_PROVIDER // empty' "$INPUT")
if [[ -z "$PROVIDER" ]]; then
  echo "render-config.sh [hermes-agent]: missing HERMES_PROVIDER in input" >&2
  exit 70
fi

BUILTIN_IDS=$(jq -r ".configs[$YAML_INDEX].supported_providers.builtin[].id" "$MF")
IS_BUILTIN=false
while IFS= read -r id; do
  [[ -z "$id" ]] && continue
  if [[ "$id" == "$PROVIDER" ]]; then IS_BUILTIN=true; break; fi
done <<< "$BUILTIN_IDS"

CUSTOM_NPM_DEFAULT=$(jq -r ".configs[$YAML_INDEX].supported_providers.custom.npm_default // empty" "$MF")

if $IS_BUILTIN; then
  PROVIDER_NPM=""
  PROVIDER_BASE_URL=""
else
  PROVIDER_NPM=$(jq -r '.HERMES_PROVIDER_NPM // empty' "$INPUT")
  PROVIDER_BASE_URL=$(jq -r '.HERMES_PROVIDER_BASE_URL // empty' "$INPUT")
  if [[ -z "$PROVIDER_NPM" || -z "$PROVIDER_BASE_URL" ]]; then
    echo "render-config.sh [hermes-agent]: custom provider '$PROVIDER' requires HERMES_PROVIDER_NPM and HERMES_PROVIDER_BASE_URL" >&2
    exit 70
  fi
fi

# --- 2. required: <PROVIDER>_API_KEY (for both config.yaml + .env) -----------
if $IS_BUILTIN; then
  KEY_ENV=$(jq -r --arg id "$PROVIDER" \
    ".configs[$YAML_INDEX].supported_providers.builtin[] | select(.id == \$id) | .api_key_env // empty" \
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
  ".configs[$YAML_INDEX].supported_providers.builtin[] | select(.id == \$id) | .auth // \"api_key\"" \
  "$MF")

if [[ "$IS_OAUTH" != "oauth" && -n "$KEY_ENV" && -z "$API_KEY" ]]; then
  echo "render-config.sh [hermes-agent]: missing API key — looked for ${KEY_ENV} in input" >&2
  exit 70
fi

# --- 3. validate enum values upfront -----------------------------------------
# Iterate all optional_inputs from both config blocks; reject unknown enum values.
# Use jq -c to emit NDJSON (one JSON object per line) so the while-read loop can parse each row.
ENUM_ERRORS=$(jq -c '
  .configs | map(
    .optional_inputs // {} | to_entries[] | select(.value.type == "enum") |
    {key: .key, allowed: .value.enum_values}
  ) | .[]
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
  echo "render-config.sh [hermes-agent]: $ENUM_ERR" >&2
  exit 71
fi

# --- 4. validate required_when conditions -------------------------------------
# required_when: "custom_provider" means the field is required iff
# $is_builtin == false. The list of keys with required_when lives in
# manifest; we check the input for each one.
REQ_WHEN_ERR=""
for ci in "$YAML_INDEX" "$ENV_INDEX"; do
  REQUIRED_WHEN_KEYS=$(jq -r ".configs[$ci].optional_inputs | to_entries[] | select(.value.required_when != null) | .key" "$MF")
  for k in $REQUIRED_WHEN_KEYS; do
    val=$(jq -r --arg k "$k" '.[$k] // empty' "$INPUT")
    required_when=$(jq -r --arg k "$k" ".configs[$ci].optional_inputs[\$k].required_when" "$MF")
    needed=false
    case "$required_when" in
      custom_provider) $IS_BUILTIN || needed=true ;;
      builtin_provider) $IS_BUILTIN && needed=true ;;
    esac
    if $needed && [[ -z "$val" ]]; then
      REQ_WHEN_ERR+="${REQ_WHEN_ERR:+; }$k (required_when=$required_when) missing"
    fi
  done
done
if [[ -n "$REQ_WHEN_ERR" ]]; then
  echo "render-config.sh [hermes-agent]: $REQ_WHEN_ERR" >&2
  exit 70
fi

# --- 5. build config.yaml (YAML) ----------------------------------------------
mkdir -p "$HOME/.hermes"
YAML_OUT="$HOME/.hermes/config.yaml"

# Build a JSON tree representing the YAML structure. The provider block is
# synthesized; everything else comes from optional_inputs + json_path.
#
# Path syntax:
#   - "a.b.c"     → setpath(["a","b","c"]; v)
#   - "providers.<name>.x" → setpath(["providers",$provider,"x"]; v)
#
# Types (we coerce in jq):
#   - string / secret_string → raw
#   - integer                 → tonumber
#   - boolean                 → (raw == "true") ? true : false
#   - number (float)          → tonumber
#   - enum                    → (validated above)
#   - json_array/json_object  → fromjson | try catch

YAML_JSON_TMP="$(mktemp -t hermes-yaml.XXXXXX)"
trap 'rm -f "$YAML_JSON_TMP"' EXIT

jq -n \
  --arg provider "$PROVIDER" \
  --arg is_builtin "$IS_BUILTIN" \
  --arg provider_npm "$PROVIDER_NPM" \
  --arg provider_base_url "$PROVIDER_BASE_URL" \
  --arg provider_api_key  "${API_KEY:-}" \
  --argjson input "$(cat "$INPUT")" \
  --argjson config "$(jq -c ".configs[$YAML_INDEX]" "$MF")" \
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
  # (array of objects with input_key). Each is normalized to [k, schema].
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
       { ($provider): { api_key_env: ((($provider | ascii_upcase) + "_API_KEY")) } }
     else
       { ($provider): {
           npm: $provider_npm,
           base_url: $provider_base_url,
           api_key_env: ((($provider | ascii_upcase) + "_API_KEY"))
         } }
     end) as $prov
  | $base
  | .providers = (if ($base.providers // null) == null then $prov
                  else ($base.providers + $prov) end)
  | .model = (($base.model // {}) + {provider: $provider})
  ' > "$YAML_JSON_TMP"

# Translate JSON → YAML via Python with PyYAML. PyYAML may live in hermes venv
# (after install) or in system python3.12 (after install.sh's 9b step). Try
# both.
write_yaml() {
  local json_path="$1"
  local yaml_path="$2"
  local py
  # Priority: $HERMES_PYTHON_BIN override → hermes venv python → system python3.12
  if [[ -n "${HERMES_PYTHON_BIN:-}" && -x "$HERMES_PYTHON_BIN" ]]; then
    py="$HERMES_PYTHON_BIN"
  elif [[ -x "${AGENT_MARKETPLACE_DEPLOY_ROOT:-}/venv/bin/python" ]]; then
    py="${AGENT_MARKETPLACE_DEPLOY_ROOT}/venv/bin/python"
  else
    py="python3.12"
  fi
  if ! "$py" -c "import yaml,sys,json; d=json.load(open('$json_path')); sys.stdout.write(yaml.dump(d, default_flow_style=False, sort_keys=False, allow_unicode=True))" > "$yaml_path" 2>/tmp/yaml-err.$$; then
    # Fall back to system python3.12
    if ! python3.12 -c "import yaml,sys,json; d=json.load(open('$json_path')); sys.stdout.write(yaml.dump(d, default_flow_style=False, sort_keys=False, allow_unicode=True))" > "$yaml_path" 2>/tmp/yaml-err.$$; then
      echo "render-config.sh [hermes-agent]: PyYAML not available (run install.sh first)" >&2
      cat /tmp/yaml-err.$$ >&2 || true
      rm -f /tmp/yaml-err.$$
      exit 71
    fi
  fi
  rm -f /tmp/yaml-err.$$
}
write_yaml "$YAML_JSON_TMP" "$YAML_OUT"
chmod 0600 "$YAML_OUT"

# --- 6. build .env (KEY=value) ------------------------------------------------
# Strategy: .env only carries *literal* legacy env vars (declared in
# configs[1].optional_inputs with a "key" field). The synthesized API key
# (e.g. ANTHROPIC_API_KEY) is always written because hermes reads it
# directly from the shell. The config.yaml block's required_inputs (e.g.
# HERMES_PROVIDER, HERMES_MODEL, <PROVIDER>_API_KEY) are skipped here —
# they belong in config.yaml, not in .env.
ENV_OUT="$HOME/.hermes/.env"
: > "$ENV_OUT.tmp"

# API key line (always present when there's a key env).
if [[ -n "$KEY_ENV" && -n "$API_KEY" ]]; then
  printf "%s=%s\n" "$KEY_ENV" "$API_KEY" >> "$ENV_OUT.tmp"
fi

# .env block's optional_inputs (each has "key" + "type").
jq -r --argjson input "$(cat "$INPUT")" '
  .configs[1].optional_inputs // {} | to_entries[] |
  .key as $k | .value.key as $env_key | .value.type as $tt |
  (try ($input[$k] // null) catch null) as $v |
  if $v != null then
    {key: $env_key, value: (if $tt == "integer" then ((try ($v | tonumber) catch $v) | tostring) else $v end)}
  else empty end
' "$MF" | jq -r '.key + "=" + .value' >> "$ENV_OUT.tmp" || true

mv "$ENV_OUT.tmp" "$ENV_OUT"

# Drop empty/unset lines (defensive).
grep -vE '^[A-Z_]+=$' "$ENV_OUT" > "$ENV_OUT.tmp" || true
mv "$ENV_OUT.tmp" "$ENV_OUT"
chmod 0600 "$ENV_OUT"