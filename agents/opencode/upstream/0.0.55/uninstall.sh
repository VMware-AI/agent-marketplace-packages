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

mapfile -t FILES < <(jq -r '.installed_files[]' "$STATE_FILE")

echo "[$AGENT/$VERSION] removing installed files from $DEPLOY_ROOT ..."
for rel in "${FILES[@]}"; do
  # Files were recorded relative to DEPLOY_ROOT (see install.sh)
  path="$DEPLOY_ROOT/$rel"
  # Resolve symlinks: the actual binary lives under DEPLOY_ROOT, the symlink under TARGET_ROOT/bin
  case "$rel" in
    ../bin/opencode)
      target="$TARGET_ROOT/bin/opencode"
      if [[ -L "$target" || -f "$target" ]]; then
        rm -f "$target"
        echo "  removed link: $target"
      fi
      ;;
    *)
      if [[ -f "$path" ]]; then
        rm -f "$path"
        echo "  removed: $path"
      fi
      ;;
  esac
done

# Remove the version directory if empty
if [[ -d "$DEPLOY_ROOT" ]] && [[ -z "$(ls -A "$DEPLOY_ROOT" 2>/dev/null)" ]]; then
  rmdir "$DEPLOY_ROOT" 2>/dev/null || true
fi

# Remove shared/ or agent root if empty
AGENT_ROOT="$TARGET_ROOT/opencode"
if [[ -d "$AGENT_ROOT" ]] && [[ -z "$(ls -A "$AGENT_ROOT" 2>/dev/null)" ]]; then
  rmdir "$AGENT_ROOT" 2>/dev/null || true
fi

rm -f "$STATE_FILE"
echo "[$AGENT/$VERSION] state file removed: $STATE_FILE"
echo "[$AGENT/$VERSION] uninstall complete"
exit 0
