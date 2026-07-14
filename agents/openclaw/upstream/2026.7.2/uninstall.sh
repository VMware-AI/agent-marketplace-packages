#!/usr/bin/env bash
# uninstall.sh — remove openclaw deployed by install.sh

set -euo pipefail

AGENT="openclaw"
SOURCE_TREE="upstream"
VERSION="2026.7.2"

TARGET_ROOT="${AGENT_MARKETPLACE_TARGET_ROOT:-$HOME/.local}"
STATE_DIR="$TARGET_ROOT/state"
STATE_FILE="$STATE_DIR/openclaw.state.json"

fail() { echo "[$AGENT/$VERSION] FAIL: $*" >&2; exit 1; }

if [[ "$(id -u)" -eq 0 ]]; then
  fail "must not run as root"
fi

if [[ ! -f "$STATE_FILE" ]]; then
  echo "[$AGENT/$VERSION] no state file at $STATE_FILE — nothing to do"
  exit 0
fi

RECORDED_VERSION=$(jq -r '.version' "$STATE_FILE")
RECORDED_SOURCE=$(jq -r '.source' "$STATE_FILE")

if [[ "$RECORDED_VERSION" != "$VERSION" || "$RECORDED_SOURCE" != "$SOURCE_TREE" ]]; then
  fail "state file records version=$RECORDED_VERSION source=$RECORDED_SOURCE, but this uninstall script targets $VERSION/$SOURCE_TREE — refusing to act."
fi

DEPLOY_ROOT=$(jq -r '.deploy_root' "$STATE_FILE")

echo "[$AGENT/$VERSION] removing deploy root: $DEPLOY_ROOT"
if [[ -d "$DEPLOY_ROOT" ]]; then
  rm -rf "$DEPLOY_ROOT"
fi

# Remove the symlink at $TARGET_ROOT/bin/openclaw
TARGET_LINK="$TARGET_ROOT/bin/openclaw"
if [[ -L "$TARGET_LINK" || -f "$TARGET_LINK" ]]; then
  rm -f "$TARGET_LINK"
  echo "  removed link: $TARGET_LINK"
fi

# Remove runtime if no other version depends on it (heuristic: check state dir)
# For simplicity, remove only this version's runtime — openclaw has a single shared
# runtime at $TARGET_ROOT/openclaw/runtime/node. If another version is still using
# it (check by scanning all state.json files for non-empty deploy), keep it.
RUNTIME_ROOT="$TARGET_ROOT/openclaw/runtime"
SHARED_NEEDED=0
for f in "$STATE_DIR"/*.state.json; do
  [[ -f "$f" ]] || continue
  if grep -q '"deploy_root"' "$f" 2>/dev/null; then
    # Some other version still installed; if it claims openclaw runtime, keep it.
    SHARED_NEEDED=1
  fi
done
if [[ $SHARED_NEEDED -eq 0 && -d "$RUNTIME_ROOT" ]]; then
  rm -rf "$RUNTIME_ROOT"
  echo "  removed runtime: $RUNTIME_ROOT"
fi

rm -f "$STATE_FILE"
echo "[$AGENT/$VERSION] state file removed: $STATE_FILE"
echo "[$AGENT/$VERSION] uninstall complete"
exit 0
