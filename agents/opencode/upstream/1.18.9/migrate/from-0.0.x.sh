#!/usr/bin/env bash
# from-0.0.x.sh — migration shim from any opencode 0.0.x prior version
#
# opencode is a self-contained Go binary with no state outside $DEPLOY_ROOT.
# The "migration" is just removing the previous version's deploy directory
# before the new install.sh writes its own files. install.sh handles the
# actual file removal by checking the state.json's installed_files list
# and running uninstall's logic first.

set -euo pipefail

AGENT="opencode"
VERSION="1.18.9"
TARGET_ROOT="${AGENT_MARKETPLACE_TARGET_ROOT:-$HOME/.local}"
STATE_FILE="$TARGET_ROOT/state/opencode.state.json"

fail() { echo "[$AGENT/$VERSION migrate] FAIL: $*" >&2; exit 1; }

if [[ ! -f "$STATE_FILE" ]]; then
  echo "[$AGENT/$VERSION migrate] no prior state found — nothing to migrate"
  exit 0
fi

RECORDED=$(jq -r '.version' "$STATE_FILE")
DEPLOY_ROOT_OLD=$(jq -r '.deploy_root' "$STATE_FILE")

echo "[$AGENT/$VERSION migrate] prior version $RECORDED detected at $DEPLOY_ROOT_OLD"
echo "[$AGENT/$VERSION migrate] -> nothing semantic to migrate (single-binary Go app)"
echo "[$AGENT/$VERSION migrate] -> install.sh will replace files; this is a no-op"

# Future migrations would slot in here — e.g. config format conversion,
# breaking change in CLI, etc. Today, opencode 0.0.x is purely additive.
exit 0
