#!/usr/bin/env bash
# install.sh — install hermes-agent from this tarball into $HOME/.local
#
# Contract: see docs/install-protocol.md
#
# Design:
#   - Runtime (Python, uv) is NOT bundled. Target machine must have Python
#     3.12.x and uv pre-installed. See manifest.runtime_requirements.
#   - hermes-agent wheel + 60 transitive wheels ARE bundled in
#     payload/wheels/ (built at pack time via 'pip download --platform
#     manylinux2014_x86_64 --python-version 3.12'). install.sh uses
#     'uv pip install --no-index --find-links' — fully offline.
#
# Steps:
#   1. Refuse root
#   2. Check system tools
#   3. Verify Python + uv versions meet manifest.runtime_requirements
#   4. Verify file checksums against manifest.checksums
#   5. Existing install / migration check
#   6. Stage bundled wheel + wheels/ into $DEPLOY_ROOT/cache
#   7. uv venv + uv pip install --no-index --find-links (offline)
#   8. Symlink hermes CLI
#   9. Verify `hermes --version`
#  10. Write state.json

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$SCRIPT_DIR"

AGENT="hermes-agent"
SOURCE_TREE="upstream"
VERSION="0.19.0"
TARGET_ROOT="${AGENT_MARKETPLACE_TARGET_ROOT:-$HOME/.local}"
STATE_DIR="$TARGET_ROOT/state"
STATE_FILE="$STATE_DIR/hermes-agent.state.json"
DEPLOY_ROOT="$TARGET_ROOT/hermes-agent/$VERSION"
VENV_ROOT="$DEPLOY_ROOT/venv"
WHEEL_DIR="$DEPLOY_ROOT/cache"

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
REQUIRED_TOOLS=(tar gzip sha256sum bash grep sed awk find xargs mkdir cp chmod jq python3.12 uv)
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

# --- 5. verify runtime (Python + uv) -----------------------------------------
log "verifying runtime: Python + uv"

PY_REQ=$(jq -r '.runtime_requirements[] | select(.name=="python") | .version' manifest.json)
PY_HINT=$(jq -r '.runtime_requirements[] | select(.name=="python") | .install_hint' manifest.json)
PY_ACTUAL=$(python3.12 --version 2>/dev/null | awk '{print $2}' || echo "0.0.0")
PY_MAJOR=$(echo "$PY_ACTUAL" | cut -d. -f1)
PY_MINOR=$(echo "$PY_ACTUAL" | cut -d. -f2)
log "  python: required=$PY_REQ actual=$PY_ACTUAL"
if [[ "$PY_MAJOR" -ne 3 || "$PY_MINOR" -ne 12 ]]; then
  fail "Python $PY_REQ required (you have $PY_ACTUAL). $PY_HINT

Or run 'tools/install-runtime.sh install --from-manifest $(pwd)/manifest.json' on the target machine (Ubuntu 24.04)." 50
fi
ok "  Python $PY_ACTUAL satisfies $PY_REQ"

UV_REQ=$(jq -r '.runtime_requirements[] | select(.name=="uv") | .version' manifest.json)
UV_HINT=$(jq -r '.runtime_requirements[] | select(.name=="uv") | .install_hint' manifest.json)
UV_ACTUAL=$(uv --version 2>/dev/null | awk '{print $2}' || echo "0.0.0")
UV_REQ_MIN=$(echo "$UV_REQ" | grep -oE '[0-9]+\.[0-9]+' | head -1)
UV_ACT_MAJOR=$(echo "$UV_ACTUAL" | cut -d. -f1)
UV_ACT_MINOR=$(echo "$UV_ACTUAL" | cut -d. -f2)
UV_REQ_MAJOR=$(echo "$UV_REQ_MIN" | cut -d. -f1)
UV_REQ_MINOR=$(echo "$UV_REQ_MIN" | cut -d. -f2)
log "  uv: required=$UV_REQ actual=$UV_ACTUAL"
if [[ "$UV_ACT_MAJOR" -lt "$UV_REQ_MAJOR" ]] || { [[ "$UV_ACT_MAJOR" -eq "$UV_REQ_MAJOR" ]] && [[ "$UV_ACT_MINOR" -lt "$UV_REQ_MINOR" ]]; }; then
  fail "uv $UV_REQ required (you have $UV_ACTUAL). $UV_HINT

Or run 'tools/install-runtime.sh install --from-manifest $(pwd)/manifest.json' on the target machine (Ubuntu 24.04)." 50
fi
ok "  uv $UV_ACTUAL satisfies $UV_REQ"

# --- 6. verify file checksums -------------------------------------------------
log "verifying file checksums against manifest.checksums ..."
ENTRIES=$(jq -r '.checksums | to_entries[]? | "\(.key)\t\(.value)"' manifest.json)
while IFS=$'\t' read -r path expected; do
  [[ -z "$path" ]] && continue
  expected_hex="${expected#sha256:}"
  if [[ "$expected_hex" == "TBD" || -z "$expected_hex" ]]; then
    continue
  fi
  if [[ ! -f "$path" ]]; then
    fail "manifest.checksums['$path'] references '$path' which is missing" 30
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

# --- 7. existing install + migration -----------------------------------------
mkdir -p "$STATE_DIR"
PREV_VERSION=""
PREV_SOURCE=""
if [[ -f "$STATE_FILE" ]]; then
  PREV_VERSION=$(jq -r '.version // ""' "$STATE_FILE")
  PREV_SOURCE=$(jq -r '.source // ""' "$STATE_FILE")
  if [[ "$PREV_VERSION" == "$VERSION" && "$PREV_SOURCE" == "$SOURCE_TREE" ]]; then
    log "version $VERSION ($SOURCE_TREE) already installed — exit 10"
    emit "STATE_PATH" "$STATE_FILE"
    emit "INSTALLED_VERSION" "$(jq -r '.installed_version // ""' "$STATE_FILE")"
    exit 10
  fi
  log "previous install: $PREV_VERSION ($PREV_SOURCE); upgrading to $VERSION"
fi

if [[ -n "$PREV_VERSION" && -n "$PREV_SOURCE" && "$PREV_SOURCE" == "$SOURCE_TREE" ]]; then
  MAJOR_MINOR="${PREV_VERSION%.*}"
  MAJOR="${MAJOR_MINOR%.*}"
  for s in "migrate/from-${PREV_VERSION}.sh" "migrate/from-${MAJOR_MINOR}.x.sh" "migrate/from-${MAJOR}.x.x.sh"; do
    if [[ -x "$s" ]]; then
      log "running migration: $s"
      TARGET_ROOT="$TARGET_ROOT" AGENT_MARKETPLACE_PREVIOUS_VERSION="$PREV_VERSION" \
        "$s" || fail "migration $s failed" 20
      break
    fi
  done
fi

# --- 8. stage bundled wheels ---------------------------------------------------
log "staging bundled wheels into $WHEEL_DIR ..."
mkdir -p "$WHEEL_DIR"
# Copy main wheel + transitive wheels
cp payload/hermes_agent-${VERSION}-py3-none-any.whl "$WHEEL_DIR/" || true
if [[ -d payload/wheels ]]; then
  cp payload/wheels/*.whl "$WHEEL_DIR/" || true
fi
WHEEL_COUNT=$(ls "$WHEEL_DIR"/*.whl 2>/dev/null | wc -l)
ok "  staged $WHEEL_COUNT wheels"

# --- 9. create venv + offline install -----------------------------------------
log "creating venv at $VENV_ROOT ..."
mkdir -p "$DEPLOY_ROOT"
python3.12 -m venv "$VENV_ROOT" || fail "venv creation failed" 60

log "installing hermes-agent + transitive deps from bundled wheels (offline) ..."
# uv --no-index --find-links reads only from $WHEEL_DIR (no network).
uv pip install \
  --python "$VENV_ROOT/bin/python" \
  --no-index \
  --find-links "$WHEEL_DIR" \
  "hermes-agent==$VERSION" \
  || fail "uv pip install failed (wheels complete? python version match?)" 60

# --- 9b. ensure PyYAML is available for render-config.sh --------------------
# render-config.sh is invoked by `agentpkg config generate` BEFORE the venv
# is necessarily built, so we cannot rely on $VENV_ROOT. Install PyYAML
# into the system Python (cpython 3.12 binary that's already on PATH) so
# the manifest-driven renderer can yaml.dump() the config.yaml output.
# hermes-agent wheels bundle PyYAML because hermes itself depends on it; if
# a future agent drops that dep, this step still guarantees availability.
log "ensuring PyYAML on system python3.12 (for render-config.sh yaml output) ..."
PYYAML_VENV=$("$VENV_ROOT/bin/python" -c "import yaml; print(yaml.__version__)" 2>&1 || true)
PYYAML_SYS=$(python3.12 -c "import yaml; print(yaml.__version__)" 2>&1 || true)
if [[ "$PYYAML_VENV" == *"Error"* ]] || [[ -z "$PYYAML_VENV" ]]; then
  fail "PyYAML missing in hermes venv — bundle is corrupt (re-pack with tools/pack.sh)" 60
fi
log "  venv PyYAML=$PYYAML_VENV"
if [[ "$PYYAML_SYS" == *"Error"* ]] || [[ -z "$PYYAML_SYS" ]]; then
  # Not strictly fatal — render-config.sh can still fall back to the venv
  # binary if it exists, but the user-host python is what gets invoked
  # before the venv is created. Try the wheels/ PyYAML first (offline).
  PY_WHEEL=$(ls "$WHEEL_DIR"/pyyaml-*.whl 2>/dev/null | head -1 || true)
  if [[ -n "$PY_WHEEL" ]]; then
    if uv pip install --python python3.12 --no-index --find-links "$WHEEL_DIR" pyyaml 2>/dev/null; then
      ok "  system PyYAML installed from bundled wheel"
    else
      warn "  could not install pyyaml into system python3.12 (will retry on first config generate)"
    fi
  else
    warn "  no pyyaml wheel in cache and system python3.12 lacks pyyaml"
  fi
else
  ok "  system PyYAML=$PYYAML_SYS"
fi

# --- 10. symlink CLI ----------------------------------------------------------
HERMES_BIN="$VENV_ROOT/bin/hermes"
if [[ ! -x "$HERMES_BIN" ]]; then
  fail "hermes CLI not found at $HERMES_BIN after install" 30
fi
mkdir -p "$TARGET_ROOT/bin"
ln -sf "$HERMES_BIN" "$TARGET_ROOT/bin/hermes"
ok "  linked: $TARGET_ROOT/bin/hermes -> $HERMES_BIN"

# --- 11. verify ---------------------------------------------------------------
log "verifying install with 'hermes --version' ..."
set +e
VERSION_OUTPUT=$("$HERMES_BIN" --version 2>&1)
RC=$?
set -e
if [[ $RC -ne 0 ]] || [[ -z "$VERSION_OUTPUT" ]]; then
  fail "hermes --version failed (exit=$RC, output='$VERSION_OUTPUT')" 60
fi
ok "verified: $VERSION_OUTPUT"

# --- 12. write state ----------------------------------------------------------
NOW=$(date -u +%Y-%m-%dT%H:%M:%SZ)

INSTALLED_FILES=(
  "$VERSION/venv/"
  "$VERSION/cache/"
)

PREV_STATE_JSON="{}"
if [[ -f "$STATE_FILE" ]]; then
  PREV_STATE_JSON=$(jq -c 'del(.installed_at)' "$STATE_FILE" 2>/dev/null || echo "{}")
fi

# Mirror manifest.services[] and manifest.configs[] into state.json so
# uninstall (run by `agentpkg uninstall` OR by hand) can find the systemd
# --user unit + rendered config to remove. Without these, an install done
# by running this script directly — without `agentpkg install` writing
# them post-install — would leave ~/.hermes/ and
# ~/.config/systemd/user/hermes-agent-dashboard.service behind on uninstall.
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
  --arg python_version "$PY_ACTUAL" \
  --arg uv_version "$UV_ACTUAL" \
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
    runtime: { python: $python_version, uv: $uv_version },
    previous: $previous
  }' > "$STATE_FILE.new"
mv "$STATE_FILE.new" "$STATE_FILE"
chmod 0644 "$STATE_FILE"
ok "wrote state file: $STATE_FILE"

# --- 13. summary --------------------------------------------------------------
emit "STATE_PATH"        "$STATE_FILE"
emit "INSTALLED_VERSION" "$VERSION_OUTPUT"
emit "DEPLOY_ROOT"       "$DEPLOY_ROOT"
emit "VENV_ROOT"         "$VENV_ROOT"

log "hermes-agent $VERSION installed (offline)."
log "binary:   $TARGET_ROOT/bin/hermes"
log "python:   $PY_ACTUAL (system)"
log "uv:       $UV_ACTUAL (system)"
log "venv:     $VENV_ROOT"
log "PATH:     $TARGET_ROOT/bin (add 'export PATH=\$HOME/.local/bin:\$PATH' to your shell rc)"
exit 0