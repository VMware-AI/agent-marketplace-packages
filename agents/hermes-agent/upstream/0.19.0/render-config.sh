#!/usr/bin/env bash
# render-config.sh — schema-driven renderer for hermes-agent.
# Invoked by `agentpkg config generate hermes-agent`.
#
# Two slots, two file formats:
#   - hermes     -> ~/.hermes/config.yaml (YAML, via yq or python+yaml)
#   - hermes-env -> ~/.hermes/.env        (KEY=VALUE, via jq flatten)
#
# Each slot reads $MARKETPLACE_CONFIG_<SLOT_UPPER> (base64-encoded
# JSON, set by the OVA firstboot from the platform's
# guestinfo.marketplace_config.<slot> guestinfo keys). The slot's
# output format is inferred from the render_to filename extension:
#   - *.yaml / *.yml  -> yq -P (fallback: python3 + yaml)
#   - .env            -> jq flatten to KEY=VALUE lines
#   - default         -> JSON verbatim
#
# Post-Phase-5 contract — see agents/opencode/.../render-config.sh
# for the same one-liner rationale.
#
# Exit codes:
#   0  — success
#   1  — env var missing
#   71 — base64 decode or JSON validation failed
set -euo pipefail

: "${AGENT_MARKETPLACE_AGENT:?AGENT_MARKETPLACE_AGENT is required}"
: "${AGENT_MARKETPLACE_VERSION:?AGENT_MARKETPLACE_VERSION is required}"

write_slot() {
  local slot_name="$1"
  local env_var="$2"
  local render_to="$3"
  local mode="$4"
  if [[ -z "${!env_var:-}" ]]; then
    echo "render-config.sh [hermes-agent]: $env_var is required (the OVA firstboot must export it from guestinfo.marketplace_config.$slot_name)" >&2
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
      printf '%s' "$decoded" | jq -r 'to_entries | .[] | (.key) + "=" + (if (.value | type) == "string" then (.value | tojson) else (.value | tostring) end)' > "$render_to"
      ;;
    *)
      printf '%s' "$decoded" | jq . > "$render_to"
      ;;
  esac
  chmod "$mode" "$render_to"
  echo "render-config.sh [hermes-agent]: wrote $render_to (new schema-driven path)"
}

write_slot "hermes"     "MARKETPLACE_CONFIG_HERMES"     "$HOME/.hermes/config.yaml" 0600
write_slot "hermes-env" "MARKETPLACE_CONFIG_HERMES_ENV" "$HOME/.hermes/.env"        0600
