#!/usr/bin/env bash
# install.sh — install openclaw from this tarball into $HOME/.local
#
# Contract: see docs/install-protocol.md
#
# Design:
#   - Runtime (Node.js) is NOT bundled. The target machine is expected to
#     have a compatible Node.js pre-installed. See manifest.runtime_requirements.
#   - The openclaw npm package + 274 transitive deps ARE bundled in
#     payload/openclaw/ (built at pack time via `npm install --global --prefix`).
#     install.sh just copies the pre-resolved tree to $TARGET_ROOT — fully offline.
#
# Steps:
#   1. Refuse root
#   2. Check system tools
#   3. Verify Node.js version meets manifest.runtime_requirements
#   4. Verify file checksums against manifest.checksums
#   5. Read existing state.json (if any); decide fresh-install vs upgrade
#   6. Run a matching migration script from migrate/ (if any)
#   7. Copy the bundled payload tree to $DEPLOY_ROOT (no network)
#   8. Symlink $TARGET_ROOT/bin/openclaw
#   9. Run `openclaw --version` to confirm install works
#  10. Write state.json

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$SCRIPT_DIR"

AGENT="openclaw"
SOURCE_TREE="upstream"
VERSION="2026.7.1-2"
TARGET_ROOT="${AGENT_MARKETPLACE_TARGET_ROOT:-$HOME/.local}"
STATE_DIR="$TARGET_ROOT/state"
STATE_FILE="$STATE_DIR/openclaw.state.json"
DEPLOY_ROOT="$TARGET_ROOT/openclaw/$VERSION"

# --- helpers ------------------------------------------------------------------
log()  { echo "[$AGENT/$VERSION] $*" >&2; }
ok()   { echo "[$AGENT/$VERSION] OK: $*" >&2; }
warn() { echo "[$AGENT/$VERSION] WARN: $*" >&2; }
fail() { echo "[$AGENT/$VERSION] FAIL: $*" >&2; exit "${2:-1}"; }
emit() { printf '%s=%s\n' "$1" "$2"; }

# --- 1. refuse root -----------------------------------------------------------
if [[ "$(id -u)" -eq 0 ]]; then
  fail "this installer must not run as root (uses $TARGET_ROOT); exit 20" 20
fi

# --- 2. check system tools ----------------------------------------------------
REQUIRED_TOOLS=(tar gzip sha256sum bash grep sed awk find xargs mkdir cp chmod jq node)
for tool in "${REQUIRED_TOOLS[@]}"; do
  if ! command -v "$tool" >/dev/null 2>&1; then
    fail "required system tool '$tool' not found on PATH (exit 40)" 40
  fi
done

# --- 3. parse manifest --------------------------------------------------------
if [[ ! -f manifest.json ]]; then
  fail "manifest.json not found in $SCRIPT_DIR (corrupt tarball?)" 30
fi
m() { jq -r "$1" manifest.json; }
CHANNEL=$(m '.channel // "stable"')

# --- 4. platform checks -------------------------------------------------------
case "$(uname -s)" in Linux) ;; *) fail "unsupported OS: $(uname -s)" 20 ;; esac
case "$(uname -m)"    in x86_64) ;; *) fail "unsupported arch: $(uname -m)" 20 ;; esac

# --- 5. verify runtime (Node.js) ---------------------------------------------
log "verifying Node.js runtime..."
NODE_REQ=$(jq -r '.runtime_requirements[] | select(.name=="node") | .version' manifest.json)
NODE_HINT=$(jq -r '.runtime_requirements[] | select(.name=="node") | .install_hint' manifest.json)
NODE_ACTUAL=$(node --version 2>/dev/null | sed 's/^v//' || echo "0.0.0")
log "  required: $NODE_REQ"
log "  actual:   v$NODE_ACTUAL"
# We accept the spec literally (e.g. ">=22.22.3 <23, >=24.15.0 <25, or >=25.9.0")
# by asking node itself to check via its OWN semver parser. But to keep this
# script portable (no node-driven self-check), we do a conservative range
# check using sort -V: extract the major + minimum minor from the spec and
# require the actual major to be one of {22, 24, 25} and the (major, minor)
# to be at least (22, 22) / (24, 15) / (25, 9).
NODE_MAJOR=$(echo "$NODE_ACTUAL" | cut -d. -f1)
NODE_MINOR=$(echo "$NODE_ACTUAL" | cut -d. -f2)
ok_runtime=0
case "$NODE_MAJOR" in
  22) [[ "$NODE_MINOR" -ge 22 ]] && ok_runtime=1 ;;
  24) [[ "$NODE_MINOR" -ge 15 ]] && ok_runtime=1 ;;
  25) [[ "$NODE_MINOR" -ge 9 ]]  && ok_runtime=1 ;;
  *) ;; # major > 25 — assume forward-compat
esac
# major > 25 always passes
if [[ "$NODE_MAJOR" -gt 25 ]]; then
  ok_runtime=1
fi
if [[ "$ok_runtime" -ne 1 ]]; then
  fail "Node.js $NODE_REQ required (you have v$NODE_ACTUAL). $NODE_HINT

Or run 'tools/install-runtime.sh install --from-manifest $(pwd)/manifest.json' on the target machine (Ubuntu 24.04)." 50
fi
ok "Node.js v$NODE_ACTUAL satisfies $NODE_REQ"

# --- 6. verify file checksums --------------------------------------------------
log "verifying file checksums against manifest.checksums ..."
ENTRIES=$(jq -r '.checksums | to_entries[]? | "\(.key)\t\(.value)"' manifest.json)
while IFS=$'\t' read -r path expected; do
  [[ -z "$path" ]] && continue
  expected_hex="${expected#sha256:}"
  if [[ "$expected_hex" == "TBD" || -z "$expected_hex" ]]; then
    continue
  fi
  if [[ ! -f "$path" ]]; then
    fail "manifest.checksums['$path'] references '$path' which is missing in this tarball" 30
  fi
  actual=$(sha256sum "$path" | awk '{print $1}')
  if [[ "$actual" != "$expected_hex" ]]; then
    fail "checksum mismatch for $path:
  expected: $expected_hex
  actual:   $actual
This tarball may be corrupted or tampered with. Re-download." 50
  fi
done <<< "$ENTRIES"
ok "manifest checksums verified"

# --- 7. check existing install + migration -----------------------------------
mkdir -p "$STATE_DIR"
PREV_VERSION=""
PREV_SOURCE=""
if [[ -f "$STATE_FILE" ]]; then
  PREV_VERSION=$(jq -r '.version // ""' "$STATE_FILE")
  PREV_SOURCE=$(jq -r '.source // ""' "$STATE_FILE")
  if [[ "$PREV_VERSION" == "$VERSION" && "$PREV_SOURCE" == "$SOURCE_TREE" ]]; then
    log "version $VERSION ($SOURCE_TREE) already installed — exit 10"
    emit "STATE_PATH" "$STATE_FILE"
    emit "INSTALLED_FILES" "$(jq -r '.installed_files | join(":")' "$STATE_FILE")"
    emit "INSTALLED_VERSION" "$(jq -r '.installed_version // ""' "$STATE_FILE")"
    exit 10
  fi
  log "previous install: $PREV_VERSION ($PREV_SOURCE); upgrading to $VERSION"
fi

# --- 8. run matching migration script ----------------------------------------
if [[ -n "$PREV_VERSION" && -n "$PREV_SOURCE" && "$PREV_SOURCE" == "$SOURCE_TREE" ]]; then
  MAJOR_MINOR="${PREV_VERSION%.*}"
  MAJOR="${MAJOR_MINOR%.*}"
  SCRIPT_EXACT="migrate/from-${PREV_VERSION}.sh"
  SCRIPT_WILDCARD="migrate/from-${MAJOR_MINOR}.x.sh"
  SCRIPT_MAJOR="migrate/from-${MAJOR}.x.x.sh"
  FOUND=""
  for s in "$SCRIPT_EXACT" "$SCRIPT_WILDCARD" "$SCRIPT_MAJOR"; do
    if [[ -x "$s" ]]; then
      FOUND="$s"; break
    fi
  done
  if [[ -n "$FOUND" ]]; then
    log "running migration: $FOUND"
    TARGET_ROOT="$TARGET_ROOT" AGENT_MARKETPLACE_PREVIOUS_VERSION="$PREV_VERSION" \
      "$FOUND" || fail "migration $FOUND failed" 20
  else
    log "no migration script for $PREV_VERSION → $VERSION (assumed compatible)"
  fi
fi

# --- 9. deploy bundled payload (offline) -------------------------------------
log "deploying pre-resolved payload to $DEPLOY_ROOT ..."
mkdir -p "$DEPLOY_ROOT"
cp -a payload/openclaw/. "$DEPLOY_ROOT/" \
  || fail "copy payload/openclaw/. to $DEPLOY_ROOT failed" 30
ok "  payload deployed"

# Sanity check the installed binary
if [[ ! -x "$DEPLOY_ROOT/bin/openclaw" ]]; then
  fail "openclaw binary not found at $DEPLOY_ROOT/bin/openclaw after install (exit 30)" 30
fi
ok "  openclaw@$VERSION installed to $DEPLOY_ROOT"

# --- 10. symlink to $TARGET_ROOT/bin -----------------------------------------
mkdir -p "$TARGET_ROOT/bin"
ln -sf "$DEPLOY_ROOT/bin/openclaw" "$TARGET_ROOT/bin/openclaw"
ok "  linked: $TARGET_ROOT/bin/openclaw -> $DEPLOY_ROOT/bin/openclaw"

# --- 11. verify it runs -------------------------------------------------------
log "verifying install with 'openclaw --version' ..."
set +e
VERSION_OUTPUT=$("$DEPLOY_ROOT/bin/openclaw" --version 2>&1)
RC=$?
set -e
if [[ $RC -ne 0 ]] || [[ -z "$VERSION_OUTPUT" ]]; then
  fail "openclaw --version failed (exit=$RC, output='$VERSION_OUTPUT')" 60
fi
ok "verified: $VERSION_OUTPUT"

# --- 11.5. default config (no --config-input path) -----------------------------
# When ``agentpkg install`` is invoked WITHOUT ``--config-input``, the runner
# skips ``render-config.sh`` entirely — and the gateway refuses to start
# without ``~/.openclaw/openclaw.json`` (``Missing config. Run `openclaw setup`
# or set gateway.mode=local``). The user-visible workaround was to add
# ``--allow-unconfigured`` to the systemd unit, which suppresses the
# enforcement but doesn't actually leave a usable config on disk.
#
# Render a sensible default here so a default install is runnable out of
# the box. The fields below match the canonical deployment: gateway sits
# behind a TLS-terminating reverse proxy on the same host, and the proxy
# enforces cookie-session auth on every request — the gateway itself runs
# unauthenticated + loopback-only + wildcard-origin. Operators exposing
# the gateway directly should override ``bind`` to ``lan``/``tailnet`` and
# re-enable ``auth.mode`` + tighten ``allowedOrigins``.
#
#   * ``mode=local``: required to start without ``--allow-unconfigured``.
#   * ``bind=loopback``: only listen on 127.0.0.1; the reverse proxy is
#     the only path to the gateway. Loopback-only is what makes the
#     wildcard-origin below safe (only same-host processes can reach the
#     gateway WS port).
#   * ``controlUi.allowedOrigins=["*"]``: openclaw refuses browser WS
#     connections whose ``Origin`` isn't in this list. The proxy serves
#     the page at ``https://<host>/``, so the browser Origin is the host
#     itself — there's no way to predict it at tarball-build time, and
#     any value we pick here would break every other install. Wildcard
#     is acceptable because bind is loopback.
#   * ``auth.mode=none``: the reverse proxy already enforces cookie auth
#     on every request. Re-enabling gateway auth would double-prompt for
#     credentials the browser dashboard can't supply, and break the WS
#     handshake (``reason=token_missing``) on every page load.
#   * ``trustedProxies=127.0.0.1/32,::1/128``: the proxy forwards
#     X-Forwarded-* from the same host. Without this allowlist the
#     gateway logs ``Proxy headers detected from untrusted address`` on
#     every WS frame and treats the connection as remote (affecting
#     device-pairing / local-client detection).
#
# If the config file already exists (because render-config.sh ran earlier
# in this install or a previous install left one), leave it alone — we
# never overwrite an operator-authored config.
log "checking for rendered config at $HOME/.openclaw/openclaw.json ..."
mkdir -p "$HOME/.openclaw"
DEFAULT_CFG="$HOME/.openclaw/openclaw.json"
if [[ ! -f "$DEFAULT_CFG" ]]; then
  TMP_CFG="$(mktemp -t openclaw-default-cfg.XXXXXX)"
  trap 'rm -f "$TMP_CFG"' EXIT
  jq -n '{
    gateway: {
      mode: "local",
      bind: "loopback",
      controlUi: { allowedOrigins: ["*"] },
      auth: { mode: "none" },
      trustedProxies: ["127.0.0.1/32", "::1/128"]
    }
  }' > "$TMP_CFG"
  install -m 0600 "$TMP_CFG" "$DEFAULT_CFG"
  rm -f "$TMP_CFG"
  trap - EXIT
  ok "  wrote default config: $DEFAULT_CFG (mode=local; loopback bind; wildcard origin; auth off — intended for behind-proxy deployments)"
else
  ok "  config already present at $DEFAULT_CFG — leaving as-is"
fi

# --- 12. write state ----------------------------------------------------------
NOW=$(date -u +%Y-%m-%dT%H:%M:%SZ)

INSTALLED_FILES=(
  "bin/openclaw"
  "$VERSION/bin/openclaw"
  "$VERSION/bin/openclaw.mjs"
  "$VERSION/lib/"
  "$VERSION/package.json"
  "$VERSION/node_modules/"
)

REL_BIN_LINK="../bin/openclaw"

PREV_STATE_JSON="{}"
if [[ -f "$STATE_FILE" ]]; then
  PREV_STATE_JSON=$(jq -c 'del(.installed_at)' "$STATE_FILE" 2>/dev/null || echo "{}")
fi

# Mirror manifest.services[] and manifest.configs[] into state.json so
# uninstall (run by `agentpkg uninstall` OR by hand) can find the systemd
# --user unit + rendered config to remove. Without these, an install done
# by running this script directly — without `agentpkg install` writing
# them post-install — would leave ~/.openclaw/ and
# ~/.config/systemd/user/openclaw-gateway.service behind on uninstall.
#
# The unit_path is computed the same way the CLI's writeSystemdUserUnit
# does: $HOME/.config/systemd/user/<agent>-<svc.name>.service.
SERVICES_JSON=$(jq -c --arg agent "$AGENT" --arg home "$HOME" '
    .services // [] | map({
        name: .name,
        unit_path: ($home + "/.config/systemd/user/" + $agent + "-" + .name + ".service"),
        started: false
    })
' manifest.json)
CONFIGS_JSON=$(jq -c '
    .configs // [] | map({
        name: .name,
        render_to: .render_to,
        mode: .mode
    })
' manifest.json)

jq -n \
  --arg agent "$AGENT" \
  --arg source "$SOURCE_TREE" \
  --arg version "$VERSION" \
  --arg channel "$CHANNEL" \
  --arg deploy_root "$DEPLOY_ROOT" \
  --arg target_root "$TARGET_ROOT" \
  --arg installed_version "$VERSION_OUTPUT" \
  --argjson installed_files "$(printf '%s\n' "${INSTALLED_FILES[@]}" | jq -R . | jq -s .)" \
  --argjson services "$SERVICES_JSON" \
  --argjson configs "$CONFIGS_JSON" \
  --arg installed_at "$NOW" \
  --arg manifest_sha256 "$(sha256sum manifest.json | awk '{print $1}')" \
  --arg runtime_node_version "v$NODE_ACTUAL" \
  --argjson previous "$PREV_STATE_JSON" \
  '{
    agent: $agent,
    source: $source,
    version: $version,
    channel: $channel,
    deploy_root: $deploy_root,
    target_root: $target_root,
    installed_version: $installed_version,
    installed_files: $installed_files,
    services: $services,
    configs: $configs,
    manifest_sha256: $manifest_sha256,
    installed_at: $installed_at,
    runtime: { node: $runtime_node_version },
    previous: $previous
  }' > "$STATE_FILE.new"
mv "$STATE_FILE.new" "$STATE_FILE"
chmod 0644 "$STATE_FILE"
ok "wrote state file: $STATE_FILE"

# --- 13. summary --------------------------------------------------------------
emit "STATE_PATH"        "$STATE_FILE"
emit "INSTALLED_VERSION" "$VERSION_OUTPUT"
emit "DEPLOY_ROOT"       "$DEPLOY_ROOT"
emit "INSTALLED_FILES"   "$(IFS=:; echo "${INSTALLED_FILES[*]}")"

log "openclaw $VERSION installed."
log "binary:  $TARGET_ROOT/bin/openclaw"
log "node:    $(node --version 2>&1) (system)"
log "PATH:    $TARGET_ROOT/bin (add 'export PATH=\$HOME/.local/bin:\$PATH' to your shell rc)"
exit 0
