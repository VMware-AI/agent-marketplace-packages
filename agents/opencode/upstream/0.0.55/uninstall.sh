#!/usr/bin/env bash
# uninstall.sh — remove opencode deployed by install.sh
#
# Reads state.json, removes every file it lists, then deletes the state file.
# Refuses to remove anything if no state file exists.

set -euo pipefail

AGENT="opencode"
SOURCE_TREE="upstream"
VERSION="0.0.55"

TARGET_ROOT="${AGENT_MARKETPLACE_TARGET_ROOT:-$HOME/.local}"
STATE_DIR="$TARGET_ROOT/state"
STATE_FILE="$STATE_DIR/opencode.state.json"

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

# Remove the symlink under $TARGET_ROOT/bin/ first (it lives outside DEPLOY_ROOT).
TARGET_LINK="$TARGET_ROOT/bin/opencode"
if [[ -L "$TARGET_LINK" || -f "$TARGET_LINK" ]]; then
  rm -f "$TARGET_LINK"
  echo "  removed link: $TARGET_LINK"
fi

# Wipe the whole deploy_root tree. install.sh places every payload file
# under $DEPLOY_ROOT, so a single rm -rf is the authoritative cleanup —
# iterating state.json.installed_files leaves the parent dirs (bin/, etc.)
# behind as empty cruft. Per-file lists are still written to state.json for
# auditability but uninstall doesn't trust them as the source of truth.
echo "[$AGENT/$VERSION] removing deploy_root: $DEPLOY_ROOT"
if [[ -d "$DEPLOY_ROOT" ]]; then
  rm -rf "$DEPLOY_ROOT"
fi

# Walk up the parent chain under TARGET_ROOT and rmdir anything that's now
# empty. We do this depth-first so child empties before parents (rmdir fails
# on non-empty dirs, hence the silent ||true). Stop at TARGET_ROOT itself —
# we never delete the user's install root.
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

rm -f "$STATE_FILE"
echo "[$AGENT/$VERSION] state file removed: $STATE_FILE"
echo "[$AGENT/$VERSION] uninstall complete"
exit 0
