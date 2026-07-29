#!/usr/bin/env bash
# render-config.sh — manifest-driven renderer for opencode.json.
# Invoked by `agentpkg config generate opencode`.
#
# This script is entirely driven by $AGENT_MARKETPLACE_MANIFEST (the
# tarball's embedded manifest.json). Adding/changing a config field is a
# matter of editing manifest.json — no shell changes required.
#
# Inputs (env vars set by agentpkg):
#   $AGENT_MARKETPLACE_CONFIG_INPUT — path to daemon's config-input JSON
#   $AGENT_MARKETPLACE_MANIFEST     — path to manifest.json (extracted by CLI)
#   $AGENT_MARKETPLACE_AGENT        — "opencode"
#   $AGENT_MARKETPLACE_VERSION      — e.g. "1.18.9"
#   $AGENT_MARKETPLACE_DEPLOY_ROOT  — e.g. "$HOME/.local/opencode/1.18.9"
#
# Output:
#   Path declared by manifest.configs[0].render_to, default
#   ~/.config/opencode/opencode.json (mode 0600).
#
# Two-layer model:
#   - ADMIN-layer fields (provider/apiKey/model/server/share/permission/...)
#     are declared in manifest.configs[0].required_inputs / optional_inputs.
#   - USER-layer fields (agent/commands/mcp/instructions/...) are out of
#     scope; opencode reads them from .opencode/ and OPENCODE_CONFIG_CONTENT.
#
# Exit codes:
#   0  — success
#   1  — internal error (env / input / manifest missing)
#   70 — required-input missing or invalid
#   71 — script error (bad type / enum value)
set -euo pipefail

: "${AGENT_MARKETPLACE_CONFIG_INPUT:?AGENT_MARKETPLACE_CONFIG_INPUT is required}"
: "${AGENT_MARKETPLACE_MANIFEST:?AGENT_MARKETPLACE_MANIFEST is required}"

if [[ ! -f "$AGENT_MARKETPLACE_CONFIG_INPUT" ]]; then
  echo "render-config.sh [opencode]: config input not found: $AGENT_MARKETPLACE_CONFIG_INPUT" >&2
  exit 1
fi
if [[ ! -f "$AGENT_MARKETPLACE_MANIFEST" ]]; then
  echo "render-config.sh [opencode]: manifest not found: $AGENT_MARKETPLACE_MANIFEST" >&2
  exit 1
fi

INPUT="$AGENT_MARKETPLACE_CONFIG_INPUT"
MF="$AGENT_MARKETPLACE_MANIFEST"

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
  # Default npm package if caller only gave one of the two (currently
  # we require both — see above). If we ever relax, default here:
  if [[ -z "$CUSTOM_NPM_DEFAULT" ]]; then
    :  # no default declared; require explicit
  fi
fi

# --- 2. required: <PROVIDER>_API_KEY -----------------------------------------
# For builtin providers, look up the declared api_key_env. For custom, the
# key env follows the same <PROVIDER>_API_KEY convention.
if $IS_BUILTIN; then
  KEY_ENV=$(jq -r --arg id "$PROVIDER" \
    '.configs[0].supported_providers.builtin[] | select(.id == $id) | .api_key_env // empty' \
    "$MF")
  if [[ -n "$KEY_ENV" ]]; then
    API_KEY=$(jq -r --arg k "$KEY_ENV" '.[$k] // empty' "$INPUT")
  fi
  # If key_env is null (e.g. ollama), accept no key.
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
# Use jq to build the rendered config object incrementally. We pre-validate
# the easy cases (enum, required_when) here to give precise error messages
# before invoking jq.

# Collect keys of optional_inputs that should be required given current state.
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
    echo "render-config.sh [opencode]: required_when '$key' is missing (required because provider is non-builtin or builtin depending on rule)" >&2
    exit 70
  fi
done

# Validate enum values upfront with jq.
ENUM_ERRORS=$(jq -r --arg input "$INPUT" '
  def lines: split("\n");
  [
    (.configs[0].optional_inputs | to_entries[]) as $f
    | $f.key as $k
    | $f.value.type as $t
    | ($f.value.enum_values // null) as $ev
    | if $t == "enum" and $ev != null then
        (try ($input | fromjson) catch null) as $inp
        | ($inp[$k] // null) as $val
        | if $val != null and ($val | tostring | IN($ev | tostring | lines)) | not then
            "\($k)=\($val) is not a valid value; allowed: \($ev | join(","))"
          else empty
        end
      else empty
    end
  ]
  | .[]
' --argjson input "$(cat "$INPUT")" "$MF" 2>/dev/null || true)

# Above is a complex one-liner; simpler approach: do enum check separately.
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

# We let jq drive the whole rendering: for each optional_input, look up the
# daemon value, coerce per its type, and assign via setpath. The path syntax
# "a.b.c" maps to setpath(["a","b","c"]); "provider.<name>.x" resolves
# <name> against $provider. Note: jq --arg vars are visible inside `def`
# bodies only if passed as a parameter — see sp($provider; ...).
jq -n \
  --arg provider "$PROVIDER" \
  --arg is_builtin "$IS_BUILTIN" \
  --arg provider_npm "$PROVIDER_NPM" \
  --arg provider_base_url "$PROVIDER_BASE_URL" \
  --arg provider_api_key  "${API_KEY:-}" \
  --argjson input "$(cat "$INPUT")" \
  --argjson config "$(jq -c '.configs[0]' "$MF")" \
  '
  # Split "a.b.<name>.x" → ["a","b",$provider,"x"]; "." literal maps "server.port".
  def sp(provider; p):
    p | split(".") | map(if . == "<name>" then provider else . end);

  # Coerce a string value to its declared type. Anything that fails parse
  # returns null, which the caller skips via set_at_path.
  def coerce(type; raw):
    if raw == null or raw == "" then null
    elif type == "integer"            then (try (raw | tonumber) catch null)
    elif type == "boolean"            then (raw == "true")
    elif type == "json_array" or type == "json_object" then
      (try (raw | fromjson) catch raw)
    else
      # string / enum / secret_string — pass through
      raw
    end;

  # Drive the loop: for each optional_inputs entry, set its coerced value at the declared path.
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