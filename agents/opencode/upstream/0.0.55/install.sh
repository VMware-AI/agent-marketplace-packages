#!/usr/bin/env bash
# install.sh — install opencode from this tarball into $HOME/.local
#
# Contract: see docs/install-protocol.md
# - Re-validates tarball integrity against manifest.checksums
# - Idempotent: re-running on an already-installed same version exits 10
# - Refuses to run as root (we are user-level only)
# - Refuses if a required system tool is missing
# - Writes $HOME/.local/state/opencode-<version>.state.json
# - Calls `opencode --version` to verify the install actually works
# - Prints INSTALLED_VERSION=<output> on success

set -euo pipefail

# --- locate ourselves ---------------------------------------------------------
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$SCRIPT_DIR"

# --- config -------------------------------------------------------------------
AGENT="opencode"
SOURCE_TREE="upstream"
VERSION="0.0.55"
TARGET_ROOT="${AGENT_MARKETPLACE_TARGET_ROOT:-$HOME/.local}"
STATE_DIR="$TARGET_ROOT/state"
STATE_FILE="$STATE_DIR/opencode.state.json"

# --- helpers ------------------------------------------------------------------
log()   { echo "[$AGENT/$VERSION] $*" >&2; }
ok()    { echo "[$AGENT/$VERSION] OK: $*" >&2; }
warn()  { echo "[$AGENT/$VERSION] WARN: $*" >&2; }
fail()  { echo "[$AGENT/$VERSION] FAIL: $*" >&2; exit "${2:-1}"; }

emit() { printf '%s=%s\n' "$1" "$2"; }

# --- 1. refuse root -----------------------------------------------------------
if [[ "$(id -u)" -eq 0 ]]; then
  fail "this installer must not run as root (uses $TARGET_ROOT); exit 20" 20
fi

# --- 2. check system tools ----------------------------------------------------
REQUIRED_TOOLS=(tar gzip sha256sum bash grep sed awk find xargs mkdir cp chmod jq)
for tool in "${REQUIRED_TOOLS[@]}"; do
  if ! command -v "$tool" >/dev/null 2>&1; then
    fail "required system tool '$tool' not found on PATH (exit 40)" 40
  fi
done

# jq is a hard requirement for parsing manifest.json in our scripts.
# If your distro lacks jq, run: apt-get install -y jq

# --- 3. parse manifest --------------------------------------------------------
if [[ ! -f manifest.json ]]; then
  fail "manifest.json not found in $SCRIPT_DIR (corrupt tarball?)" 30
fi

# Extract fields with jq (stays structured, no shell-quoting landmines)
m() { jq -r "$1" manifest.json; }

CHANNEL=$(m '.channel // "stable"')
REQUIRES_OS=$(m '.requires.os | join(",")')
REQUIRES_ARCH=$(m '.requires.arch | join(",")')

# --- 4. platform checks -------------------------------------------------------
case "$(uname -s)" in Linux) CURRENT_OS="linux" ;; *) fail "unsupported OS: $(uname -s)" 20 ;; esac
case "$(uname -m)"    in x86_64) CURRENT_ARCH="x86_64" ;; *) fail "unsupported arch: $(uname -m)" 20 ;; esac

if [[ ",$REQUIRES_OS," != *",$CURRENT_OS,"* ]]; then
  fail "manifest requires os=$REQUIRES_OS, host is $CURRENT_OS" 20
fi
if [[ ",$REQUIRES_ARCH," != *",$CURRENT_ARCH,"* ]]; then
  fail "manifest requires arch=$REQUIRES_ARCH, host is $CURRENT_ARCH" 20
fi

# --- 5. check existing install ------------------------------------------------
mkdir -p "$STATE_DIR"
if [[ -f "$STATE_FILE" ]]; then
  EXISTING_VERSION=$(jq -r '.version // ""' "$STATE_FILE")
  EXISTING_SOURCE=$(jq -r '.source // ""' "$STATE_FILE")
  if [[ "$EXISTING_VERSION" == "$VERSION" && "$EXISTING_SOURCE" == "$SOURCE_TREE" ]]; then
    log "version $VERSION ($SOURCE_TREE) already installed at $STATE_FILE — exit 10"
    emit "STATE_PATH" "$STATE_FILE"
    emit "INSTALLED_FILES" ""
    emit "INSTALLED_VERSION" "$(jq -r '.installed_version // ""' "$STATE_FILE")"
    exit 10
  fi
  log "previous install detected: $EXISTING_VERSION ($EXISTING_SOURCE); upgrading to $VERSION"
fi

# --- 6. verify checksum of every file in manifest.checksums ------------------
log "verifying file checksums against manifest.checksums ..."
ENTRIES=$(jq -r '.checksums | to_entries[] | "\(.key)\t\(.value)"' manifest.json)
while IFS=$'\t' read -r path expected; do
  [[ -z "$path" ]] && continue
  [[ -f "$path" ]] || fail "manifest references '$path' but it is not present (corrupt tarball?)" 30
  actual=$(sha256sum "$path" | awk '{print $1}')
  expected_hex="${expected#sha256:}"
  if [[ "$actual" != "$expected_hex" ]]; then
    fail "checksum mismatch for $path:
  expected: $expected_hex
  actual:   $actual
This tarball may be corrupted or tampered with. Re-download." 50
  fi
done <<< "$ENTRIES"
ok "all ${#REQUIRED_TOOLS[@]} manifest checksums match"

# --- 7. deploy payload --------------------------------------------------------
DEPLOY_ROOT="$TARGET_ROOT/opencode/$VERSION"
BIN_DIR="$DEPLOY_ROOT/bin"

mkdir -p "$BIN_DIR"

log "deploying payload to $DEPLOY_ROOT ..."
INSTALLED_FILES=()
while IFS=$'\t' read -r src dst mode; do
  [[ -z "$src" ]] && continue
  src="$SCRIPT_DIR/$src"
  dst_expanded="${dst//\{\{TARGET_ROOT\}\}/$TARGET_ROOT}"

  dstdir="$(dirname "$dst_expanded")"
  mkdir -p "$dstdir"

  cp -f "$src" "$dst_expanded"
  chmod "$mode" "$dst_expanded"

  # Record relative path under DEPLOY_ROOT for clean uninstall
  rel="${dst_expanded#"$DEPLOY_ROOT"/}"
  INSTALLED_FILES+=("$rel")
  ok "  deployed: $dst_expanded (mode $mode)"
done <<< "$(jq -r '.payload[] | "\(.src)\t\(.dst)\t\(.mode)"' manifest.json)"

# --- 8. link a stable command name into PATH --------------------------------
mkdir -p "$TARGET_ROOT/bin"
ln -sf "$DEPLOY_ROOT/bin/opencode" "$TARGET_ROOT/bin/opencode"
INSTALLED_FILES+=("../bin/opencode")   # relative to DEPLOY_ROOT for uninstall
ok "  linked:    $TARGET_ROOT/bin/opencode -> $DEPLOY_ROOT/bin/opencode"

# --- 9. verify the binary actually runs --------------------------------------
log "verifying install with '$BIN_DIR/opencode --version' ..."
set +e
VERSION_OUTPUT=$("$BIN_DIR/opencode" --version 2>&1)
RC=$?
set -e

if [[ $RC -ne 0 ]] || [[ -z "$VERSION_OUTPUT" ]]; then
  fail "opencode --version failed (exit=$RC, output='$VERSION_OUTPUT').
This typically means a library is missing on this host (e.g. glibc, libstdc++)." 60
fi
ok "verified: $VERSION_OUTPUT"

# --- 10. write state ----------------------------------------------------------
# PREV_STATE_JSON must be valid JSON for --argjson. Default to "{}" so a
# fresh install (no prior state.json) doesn't pass an empty string.
PREV_STATE_JSON="{}"
if [[ -f "$STATE_FILE" ]]; then
  PREV_STATE_JSON=$(jq -c 'del(.installed_at)' "$STATE_FILE" 2>/dev/null || echo "{}")
fi

NOW=$(date -u +%Y-%m-%dT%H:%M:%SZ)

jq -n \
  --arg agent "$AGENT" \
  --arg source "$SOURCE_TREE" \
  --arg version "$VERSION" \
  --arg channel "$CHANNEL" \
  --arg deploy_root "$DEPLOY_ROOT" \
  --arg target_root "$TARGET_ROOT" \
  --arg installed_version "$VERSION_OUTPUT" \
  --argjson installed_files "$(printf '%s\n' "${INSTALLED_FILES[@]}" | jq -R . | jq -s .)" \
  --argjson previous "$PREV_STATE_JSON" \
  --arg installed_at "$NOW" \
  --arg manifest_sha256 "$(sha256sum manifest.json | awk '{print $1}')" \
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
    previous: $previous
  }' > "$STATE_FILE.new"
mv "$STATE_FILE.new" "$STATE_FILE"
chmod 0644 "$STATE_FILE"
ok "wrote state file: $STATE_FILE"

# --- 11. emit machine-readable summary ---------------------------------------
emit "STATE_PATH"        "$STATE_FILE"
emit "INSTALLED_FILES"   "$(IFS=:; echo "${INSTALLED_FILES[*]}")"
emit "INSTALLED_VERSION" "$VERSION_OUTPUT"
emit "DEPLOY_ROOT"       "$DEPLOY_ROOT"

log "opencode $VERSION installed successfully."
log "binary: $BIN_DIR/opencode"
log "PATH:   $TARGET_ROOT/bin (add 'export PATH=\$HOME/.local/bin:\$PATH' to your shell rc)"
exit 0
