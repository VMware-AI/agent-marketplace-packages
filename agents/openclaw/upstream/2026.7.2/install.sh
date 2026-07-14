#!/usr/bin/env bash
# install.sh — install openclaw from this tarball into $HOME/.local
#
# Contract: see docs/install-protocol.md
#
# What this script does, in order:
#   1. Refuse root (this is a user-level installer)
#   2. Check system tools (tar, sha256sum, curl, etc.) are present
#   3. Verify this tarball's file checksums against manifest.checksums
#   4. Read existing state.json (if any); decide fresh-install vs upgrade
#   5. Run a matching migration script from migrate/ (if any)
#   6. Deploy the bundled Node 22 runtime to $HOME/.local/openclaw/runtime/node
#   7. Use that Node's `npm` to install openclaw@2026.7.2 globally under the
#      user's $HOME/.local/openclaw/2026.7.2 prefix
#   8. Symlink $HOME/.local/bin/openclaw to the installed CLI
#   9. Run `openclaw --version` to confirm install works
#  10. Write the new state.json
#
# The npm package itself is NOT bundled inside this tarball — install.sh
# pulls it from registry.npmjs.org on first install using the bundled Node.
# (This matches the runtime strategy where the build host has internet but
# the eventual consumer machine may or may not. The Node runtime IS bundled
# because it is large and stable enough to amortize that cost.)
#
# Future: a fully-offline variant of this installer could pre-`npm pack` the
# openclaw tarball + all transitive deps into payload/ and `npm install --offline`.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$SCRIPT_DIR"

AGENT="openclaw"
SOURCE_TREE="upstream"
VERSION="2026.7.2"
TARGET_ROOT="${AGENT_MARKETPLACE_TARGET_ROOT:-$HOME/.local}"
STATE_DIR="$TARGET_ROOT/state"
STATE_FILE="$STATE_DIR/openclaw.state.json"
RUNTIME_ROOT="$TARGET_ROOT/openclaw/runtime"
DEPLOY_ROOT="$TARGET_ROOT/openclaw/$VERSION"
NPM_PREFIX="$DEPLOY_ROOT"   # npm installs go here
NODE_BIN="$RUNTIME_ROOT/node/bin/node"
NPM_BIN="$RUNTIME_ROOT/node/bin/npm"

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

# --- 5. verify file checksums (skip missing-runtime gracefully) --------------
log "verifying file checksums against manifest.checksums ..."
ENTRIES=$(jq -r '.checksums | to_entries[]? | "\(.key)\t\(.value)"' manifest.json)
while IFS=$'\t' read -r path expected; do
  [[ -z "$path" ]] && continue
  expected_hex="${expected#sha256:}"
  if [[ "$expected_hex" == "TBD" || -z "$expected_hex" ]]; then
    continue  # upstream-only field; resolved at install time
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

# --- 6. check existing install + migration -----------------------------------
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
    emit "INSTALLED_FILES" "$(jq -r '.installed_files | join(":")' "$STATE_FILE")"
    emit "INSTALLED_VERSION" "$(jq -r '.installed_version // ""' "$STATE_FILE")"
    exit 10
  fi
  log "previous install: $PREV_VERSION ($PREV_SOURCE); upgrading to $VERSION"
fi

# --- 7. run matching migration script ----------------------------------------
if [[ -n "$PREV_VERSION" && -n "$PREV_SOURCE" && "$PREV_SOURCE" == "$SOURCE_TREE" ]]; then
  # Find matching migration script (exact → wildcard)
  MAJOR_MINOR="${PREV_VERSION%.*}"   # e.g. "2026.7" from "2026.7.1"
  MAJOR="${MAJOR_MINOR%.*}"          # "2026"
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

# --- 8. deploy runtime --------------------------------------------------------
log "deploying Node 22 runtime to $RUNTIME_ROOT/node/ ..."
mkdir -p "$RUNTIME_ROOT"
cp -a runtime/node/. "$RUNTIME_ROOT/node/"
ok "  runtime deployed"

# --- 9. npm install -----------------------------------------------------------
log "installing openclaw@$VERSION via bundled npm ..."
mkdir -p "$NPM_PREFIX"
# `--prefix` makes npm put node_modules + bin under $NPM_PREFIX
# `--no-audit --no-fund` keep the install quiet and fast
PATH="$RUNTIME_ROOT/node/bin:$PATH" \
  "$NODE_BIN" "$NPM_BIN" install \
    --prefix "$NPM_PREFIX" \
    --no-audit --no-fund \
    --global \
    --no-save \
    "openclaw@$VERSION" \
  || fail "npm install openclaw@$VERSION failed (network? registry unavailable?)" 60

# Sanity check the installed binary
if [[ ! -x "$NPM_PREFIX/bin/openclaw" ]]; then
  fail "openclaw binary not found at $NPM_PREFIX/bin/openclaw after install (exit 30)" 30
fi
ok "  openclaw@$VERSION installed to $NPM_PREFIX"

# --- 10. symlink to $TARGET_ROOT/bin -----------------------------------------
mkdir -p "$TARGET_ROOT/bin"
ln -sf "$NPM_PREFIX/bin/openclaw" "$TARGET_ROOT/bin/openclaw"
ok "  linked: $TARGET_ROOT/bin/openclaw -> $NPM_PREFIX/bin/openclaw"

# --- 11. verify it runs -------------------------------------------------------
log "verifying install with 'openclaw --version' ..."
set +e
VERSION_OUTPUT=$("$NPM_PREFIX/bin/openclaw" --version 2>&1)
RC=$?
set -e
if [[ $RC -ne 0 ]] || [[ -z "$VERSION_OUTPUT" ]]; then
  fail "openclaw --version failed (exit=$RC, output='$VERSION_OUTPUT')" 60
fi
ok "verified: $VERSION_OUTPUT"

# --- 12. write state ----------------------------------------------------------
NOW=$(date -u +%Y-%m-%dT%H:%M:%SZ)

INSTALLED_FILES=(
  "bin/openclaw"
  "$VERSION/bin/openclaw"
  "$VERSION/bin/openclaw.mjs"
  "$VERSION/lib/"
  "$VERSION/package.json"
  "$VERSION/node_modules/"
  "runtime/node/bin/node"
)

# Include the symlink at $TARGET_ROOT/bin/openclaw
REL_BIN_LINK="../bin/openclaw"

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
  --arg runtime_node_version "22.22.3" \
  --arg previous_version "$PREV_VERSION" \
  --arg previous_source "$PREV_SOURCE" \
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
    runtime: { node: $runtime_node_version },
    previous: { version: $previous_version, source: $previous_source }
  }' > "$STATE_FILE.new"
mv "$STATE_FILE.new" "$STATE_FILE"
chmod 0644 "$STATE_FILE"
ok "wrote state file: $STATE_FILE"

# --- 13. summary --------------------------------------------------------------
emit "STATE_PATH"        "$STATE_FILE"
emit "INSTALLED_VERSION" "$VERSION_OUTPUT"
emit "DEPLOY_ROOT"       "$DEPLOY_ROOT"
emit "RUNTIME_ROOT"      "$RUNTIME_ROOT"
emit "INSTALLED_FILES"   "$(IFS=:; echo "${INSTALLED_FILES[*]}")"

log "openclaw $VERSION installed."
log "binary:  $TARGET_ROOT/bin/openclaw"
log "runtime: $RUNTIME_ROOT/node/bin/node (Node $("$NODE_BIN" --version 2>&1))"
log "PATH:    $TARGET_ROOT/bin (add 'export PATH=\$HOME/.local/bin:\$PATH' to your shell rc)"
exit 0
