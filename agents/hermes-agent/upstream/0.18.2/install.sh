#!/usr/bin/env bash
# install.sh — install hermes-agent from this tarball into $HOME/.local
#
# Contract: see docs/install-protocol.md
#
# Steps:
#   1. Refuse root (user-level only)
#   2. Check system tools (curl etc.) are present
#   3. Verify this tarball's file checksums against manifest.checksums
#   4. Read existing state.json (if any); decide fresh-install vs upgrade
#   5. Run a matching migration script from migrate/ (if any)
#   6. Deploy bundled Python 3.12 + uv runtimes to $HOME/.local/hermes-agent/runtime/
#   7. Use uv to create a venv and pip-install the bundled hermes-agent wheel +
#      all transitive deps from a configured PyPI mirror
#   8. Symlink hermes (CLI) into $HOME/.local/bin
#   9. Run `hermes --version` to verify
#  10. Write state.json
#
# NOT bundled:
#   - hermes-agent's 30+ transitive deps (openai, pydantic, fastapi, mcp, ...)
#     These are pulled live from a PyPI mirror on first install.
#     Run tools/fetch.sh to pre-populate payload/wheels/ for fully-offline install.
#
# Mirrors tested at packaging time:
#   - https://mirrors.aliyun.com/pypi/simple/  (China-friendly)
#   - https://pypi.tuna.tsinghua.edu.cn/simple/
#   - https://pypi.org/simple/  (default if no override)
#
# Override at install time with environment variable:
#   AGENT_MARKETPLACE_PYPI_MIRROR=https://pypi.tuna.tsinghua.edu.cn/simple/ \
#     ./install.sh

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$SCRIPT_DIR"

AGENT="hermes-agent"
SOURCE_TREE="upstream"
VERSION="0.18.2"
TARGET_ROOT="${AGENT_MARKETPLACE_TARGET_ROOT:-$HOME/.local}"
STATE_DIR="$TARGET_ROOT/state"
STATE_FILE="$STATE_DIR/hermes-agent.state.json"
RUNTIME_ROOT="$TARGET_ROOT/hermes-agent/runtime"
DEPLOY_ROOT="$TARGET_ROOT/hermes-agent/$VERSION"
VENV_ROOT="$DEPLOY_ROOT/venv"
WHEEL_DIR="$DEPLOY_ROOT/cache"

PYTHON_BIN="$RUNTIME_ROOT/python/bin/python3.12"
UV_BIN="$RUNTIME_ROOT/uv/uv"
PYPI_MIRROR="${AGENT_MARKETPLACE_PYPI_MIRROR:-https://mirrors.aliyun.com/pypi/simple/}"

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
REQUIRED_TOOLS=(tar gzip sha256sum bash grep sed awk find xargs mkdir cp chmod curl jq)
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

# --- 5. verify file checksums -------------------------------------------------
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

# --- 6. existing install + migration -----------------------------------------
mkdir -p "$STATE_DIR"
PREV_VERSION=""
PREV_SOURCE=""
PREV_DEPLOY=""
if [[ -f "$STATE_FILE" ]]; then
  PREV_VERSION=$(jq -r '.version // ""' "$STATE_FILE")
  PREV_SOURCE=$(jq -r '.source // ""' "$STATE_FILE")
  PREV_DEPLOY=$(jq -r '.deploy_root // ""' "$STATE_FILE")
  if [[ "$PREV_VERSION" == "$VERSION" && "$PREV_SOURCE" == "$SOURCE_TREE" ]]; then
    log "version $VERSION ($SOURCE_TREE) already installed — exit 10"
    emit "STATE_PATH" "$STATE_FILE"
    emit "INSTALLED_VERSION" "$(jq -r '.installed_version // ""' "$STATE_FILE")"
    exit 10
  fi
  log "previous install: $PREV_VERSION ($PREV_SOURCE); upgrading to $VERSION"
fi

if [[ -n "$PREV_VERSION" && -n "$PREV_SOURCE" && "$PREV_SOURCE" == "$SOURCE_TREE" ]]; then
  MAJOR_MINOR="${PREV_VERSION%.*}"   # 0.18
  MAJOR="${MAJOR_MINOR%.*}"          # 0
  for s in "migrate/from-${PREV_VERSION}.sh" "migrate/from-${MAJOR_MINOR}.x.sh" "migrate/from-${MAJOR}.x.x.sh"; do
    if [[ -x "$s" ]]; then
      log "running migration: $s"
      TARGET_ROOT="$TARGET_ROOT" AGENT_MARKETPLACE_PREVIOUS_VERSION="$PREV_VERSION" \
        "$s" || fail "migration $s failed" 20
      break
    fi
  done
fi

# --- 7. deploy runtime --------------------------------------------------------
log "deploying Python 3.12 + uv runtime to $RUNTIME_ROOT ..."
mkdir -p "$RUNTIME_ROOT"
cp -a runtime/python/. "$RUNTIME_ROOT/python/"
cp -a runtime/uv/. "$RUNTIME_ROOT/uv/"
ok "  runtime deployed"

# --- 8. create venv + install -------------------------------------------------
log "creating venv at $VENV_ROOT ..."
mkdir -p "$DEPLOY_ROOT"
"$PYTHON_BIN" -m venv "$VENV_ROOT" || fail "venv creation failed" 60

# Pre-stage the bundled wheel
mkdir -p "$WHEEL_DIR"
cp payload/hermes_agent-${VERSION}-py3-none-any.whl "$WHEEL_DIR/" || true
ok "  staged bundled wheel"

log "installing hermes-agent + transitive deps from $PYPI_MIRROR ..."
# Use uv pip for fast resolution; allow network for the transitive deps
# (the wheel itself is local; deps come from the mirror)
UV_INDEX_URL="$PYPI_MIRROR" \
  "$UV_BIN" pip install \
    --python "$VENV_ROOT/bin/python" \
    "$WHEEL_DIR/hermes_agent-${VERSION}-py3-none-any.whl" \
  || fail "uv pip install failed (mirror reachable? transitive deps compatible?)" 60

# --- 9. symlink CLI -----------------------------------------------------------
HERMES_BIN="$VENV_ROOT/bin/hermes"
if [[ ! -x "$HERMES_BIN" ]]; then
  fail "hermes CLI not found at $HERMES_BIN after install" 30
fi
mkdir -p "$TARGET_ROOT/bin"
ln -sf "$HERMES_BIN" "$TARGET_ROOT/bin/hermes"
ok "  linked: $TARGET_ROOT/bin/hermes -> $HERMES_BIN"

# --- 10. verify ---------------------------------------------------------------
log "verifying install with 'hermes --version' ..."
set +e
VERSION_OUTPUT=$("$HERMES_BIN" --version 2>&1)
RC=$?
set -e
if [[ $RC -ne 0 ]] || [[ -z "$VERSION_OUTPUT" ]]; then
  fail "hermes --version failed (exit=$RC, output='$VERSION_OUTPUT')" 60
fi
ok "verified: $VERSION_OUTPUT"

# --- 11. write state ----------------------------------------------------------
NOW=$(date -u +%Y-%m-%dT%H:%M:%SZ)

INSTALLED_FILES=(
  "$VERSION/venv/"
  "$VERSION/cache/"
  "runtime/python/bin/python3.12"
  "runtime/uv/uv"
)

jq -n \
  --arg agent "$AGENT" \
  --arg source "$SOURCE_TREE" \
  --arg version "$VERSION" \
  --arg channel "$CHANNEL" \
  --arg deploy_root "$DEPLOY_ROOT" \
  --arg target_root "$TARGET_ROOT" \
  --arg installed_version "$VERSION_OUTPUT" \
  --argjson installed_files "$(printf '%s\n' "${INSTALLED_FILES[@]}" | jq -R . | jq -s .)" \
  --arg installed_at "$NOW" \
  --arg manifest_sha256 "$(sha256sum manifest.json | awk '{print $1}')" \
  --arg runtime_python_version "3.12.13" \
  --arg runtime_uv_version "0.11.28" \
  --arg previous_version "$PREV_VERSION" \
  --arg pypi_mirror "$PYPI_MIRROR" \
  '{
    agent: $agent,
    source: $source,
    version: $version,
    channel: $channel,
    deploy_root: $deploy_root,
    target_root: $target_root,
    installed_version: $installed_version,
    installed_files: $installed_files,
    manifest_sha256: $manifest_sha256,
    installed_at: $installed_at,
    runtime: { python: $runtime_python_version, uv: $runtime_uv_version },
    pypi_mirror: $pypi_mirror,
    previous: { version: $previous_version }
  }' > "$STATE_FILE.new"
mv "$STATE_FILE.new" "$STATE_FILE"
chmod 0644 "$STATE_FILE"
ok "wrote state file: $STATE_FILE"

# --- 12. summary --------------------------------------------------------------
emit "STATE_PATH"        "$STATE_FILE"
emit "INSTALLED_VERSION" "$VERSION_OUTPUT"
emit "DEPLOY_ROOT"       "$DEPLOY_ROOT"
emit "RUNTIME_ROOT"      "$RUNTIME_ROOT"
emit "VENV_ROOT"         "$VENV_ROOT"
emit "PYPI_MIRROR"       "$PYPI_MIRROR"

log "hermes-agent $VERSION installed."
log "binary:   $TARGET_ROOT/bin/hermes"
log "python:   $("$PYTHON_BIN" --version 2>&1)"
log "uv:       $("$UV_BIN" --version 2>&1)"
log "venv:     $VENV_ROOT"
log "PATH:     $TARGET_ROOT/bin (add 'export PATH=\$HOME/.local/bin:\$PATH' to your shell rc)"
exit 0