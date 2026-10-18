#!/usr/bin/env bash
# from-1.18.x.sh — major.minor wildcard migration shim for any opencode 1.18.x
#
# Per docs/upgrade-protocol.md, this major.minor-wildcard script runs when
# the previously-installed version matches 1.18.* but is NOT 1.18.9 (which
# has the more specific from-1.18.9.sh). Covers e.g. 1.18.5, 1.18.6, 1.18.7,
# 1.18.8.
#
# Like from-1.18.9.sh, this is a no-op: opencode is a single-binary Go app
# with no on-disk state outside $DEPLOY_ROOT, and 1.19.0 is binary-compatible
# with every 1.18.x (same config schema, same admin-layer render-config.sh).
#
# install.sh invokes this with $AGENT_MARKETPLACE_PREVIOUS_VERSION set to
# the actual installed 1.18.x version, so a future per-version migration
# can branch on it.

set -euo pipefail

AGENT="opencode"
VERSION="1.19.0"
TARGET_ROOT="${AGENT_MARKETPLACE_TARGET_ROOT:-$HOME/.local}"
STATE_FILE="$TARGET_ROOT/state/opencode.state.json"

fail() { echo "[$AGENT/$VERSION migrate] FAIL: $*" >&2; exit 1; }

if [[ ! -f "$STATE_FILE" ]]; then
  echo "[$AGENT/$VERSION migrate] no prior state found — nothing to migrate"
  exit 0
fi

RECORDED=$(jq -r '.version' "$STATE_FILE")
PREVIOUS_VERSION="${AGENT_MARKETPLACE_PREVIOUS_VERSION:-$RECORDED}"
DEPLOY_ROOT_OLD=$(jq -r '.deploy_root' "$STATE_FILE")

echo "[$AGENT/$VERSION migrate] prior version $PREVIOUS_VERSION (state=$RECORDED) at $DEPLOY_ROOT_OLD"
echo "[$AGENT/$VERSION migrate] -> nothing semantic to migrate (single-binary Go app)"
echo "[$AGENT/$VERSION migrate] -> install.sh will replace files; this is a no-op"

# Future migrations would slot in here. Today, every 1.18.x shares the
# same config schema as 1.19.0, so a 1.18.5 -> 1.19.0 jump is identical
# to a 1.18.9 -> 1.19.0 jump. If that ever changes, branch on
# $PREVIOUS_VERSION here.
exit 0
