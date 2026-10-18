#!/usr/bin/env bash
# from-1.18.9.sh — migration shim from opencode 1.18.9 to 1.19.0
#
# Per docs/upgrade-protocol.md, this exact-match script runs when the
# previously-installed version equals 1.18.9. The major.minor wildcard
# from-1.18.x.sh handles every other 1.18.x prior version.
#
# opencode is a self-contained Go binary with no state outside
# $DEPLOY_ROOT, so 1.18.9 -> 1.19.0 is purely additive: the new
# `opencode upgrade --major` subcommand and the change to
# non-automatic updates only affect runtime behavior of the new binary.
# The render-config.sh schema is unchanged, so admin-layer
# ~/.config/opencode/opencode.json does not need rewriting.
#
# This script is idempotent and a no-op today; if a future 1.18.9 -> 1.19.x
# upgrade ever needs to rewrite config, drop the logic in here.

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
DEPLOY_ROOT_OLD=$(jq -r '.deploy_root' "$STATE_FILE")

echo "[$AGENT/$VERSION migrate] prior version $RECORDED detected at $DEPLOY_ROOT_OLD"
echo "[$AGENT/$VERSION migrate] -> nothing semantic to migrate (single-binary Go app)"
echo "[$AGENT/$VERSION migrate] -> install.sh will replace files; this is a no-op"

# Future migrations would slot in here — e.g. config format conversion,
# a 1.18.9 -> 1.19.0 autoupdate default flip (true -> notify), etc. Today
# the schema is identical and the binary swap is the whole upgrade.
exit 0
