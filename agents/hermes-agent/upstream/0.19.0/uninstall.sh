#!/usr/bin/env bash
# uninstall.sh — remove hermes-agent deployed by install.sh

set -euo pipefail

AGENT="hermes-agent"
SOURCE_TREE="upstream"
VERSION="0.19.0"

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

# Walk up the parent chain under TARGET_ROOT and rmdir anything that's now
# empty (depth-first; rmdir is silent on non-empty dirs). Stops at
# TARGET_ROOT — never delete the user's install root.
parent="$(dirname "$DEPLOY_ROOT")"
while [[ "$parent" != "$TARGET_ROOT" && "$parent" != "/" && "$parent" != "." ]]; do
  if [[ -d "$parent" ]] && [[ -z "$(ls -A "$parent" 2>/dev/null)" ]]; then
    rmdir "$parent" 2>/dev/null || break
    echo "  removed empty dir: $parent"
  else
    break
  fi
  parent="$(dirname "$parent")"
done

# No runtime is bundled anymore — nothing to remove on the runtime side.
# (Earlier versions of this installer vendored Python + uv under
# $TARGET_ROOT/hermes-agent/runtime; that dir will simply not exist
# going forward.)

rm -f "$STATE_FILE"
echo "[$AGENT/$VERSION] state file removed: $STATE_FILE"
echo "[$AGENT/$VERSION] uninstall complete"
exit 0