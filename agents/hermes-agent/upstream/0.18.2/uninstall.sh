#!/usr/bin/env bash
# uninstall.sh — remove hermes-agent deployed by install.sh

set -euo pipefail

AGENT="hermes-agent"
SOURCE_TREE="upstream"
VERSION="0.18.2"

TARGET_ROOT="${AGENT_MARKETPLACE_TARGET_ROOT:-$HOME/.local}"
STATE_DIR="$TARGET_ROOT/state"
STATE_FILE="$STATE_DIR/hermes-agent.state.json"

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
DEPLOY_ROOT=$(jq -r '.deploy_root' "$STATE_FILE")

if [[ "$RECORDED_VERSION" != "$VERSION" || "$RECORDED_SOURCE" != "$SOURCE_TREE" ]]; then
  fail "state file records version=$RECORDED_VERSION source=$RECORDED_SOURCE, but this uninstall script targets $VERSION/$SOURCE_TREE — refusing to act."
fi

TARGET_LINK="$TARGET_ROOT/bin/hermes"
if [[ -L "$TARGET_LINK" || -f "$TARGET_LINK" ]]; then
  rm -f "$TARGET_LINK"
  echo "  removed link: $TARGET_LINK"
fi

if [[ -d "$DEPLOY_ROOT" ]]; then
  rm -rf "$DEPLOY_ROOT"
  echo "  removed deploy_root: $DEPLOY_ROOT"
fi

# Runtime is shared across versions; remove only if no other version still uses it
RUNTIME_ROOT="$TARGET_ROOT/hermes-agent/runtime"
SHARED_NEEDED=0
for f in "$STATE_DIR"/*.state.json; do
  [[ -f "$f" ]] || continue
  if grep -q '"deploy_root"' "$f" 2>/dev/null && [[ "$(basename "$f")" != "hermes-agent.state.json" ]]; then
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