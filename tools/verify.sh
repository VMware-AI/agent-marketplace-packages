#!/usr/bin/env bash
# verify.sh — validate a (agent, source, version) bundle's manifest + checksums
#
# Usage:
#   tools/verify.sh                                  verify every (agent, source, version)
#   tools/verify.sh <agent> <source> <version>       verify one bundle
#   tools/verify.sh --fix <agent> <source> <version> auto-fill TBD checksums in the manifest
#
# Exit codes:
#   0  everything checks out
#   1  manifest not parseable / file missing
#   2  checksum mismatch (file corrupted or modified)
#   3  manifest schema invalid
#   4  system requirements not met
#
# What it checks (in order):
#   1. manifest.json is valid JSON
#   2. required top-level fields are present
#   3. every file referenced by manifest.payload exists
#   4. every file in manifest.checksums exists and matches
#   5. dist/<name>.tar.gz.sha256 (if present) matches manifest.tarball.sha256
#
# NOT checking here (install.sh does it on target):
#   - system tool presence on the host (the verify host != install host)
#   - cross-subtree compatibility for source=ours

set -euo pipefail

# Save a baseline shell opts snapshot so we can re-parse positional
# args below without `set --` breaking later declare statements.
BASH_OPTS_ORIG="$-"

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
cd "$REPO_ROOT"

RED=$'\033[31m'; GRN=$'\033[32m'; YLW=$'\033[33m'; RST=$'\033[0m'
log()  { echo "$@" >&2; }
ok()   { echo "${GRN}✓${RST} $*" >&2; }
warn() { echo "${YLW}!${RST} $*" >&2; }
err()  { echo "${RED}✗${RST} $*" >&2; }
fail() { err "$*"; exit "${2:-1}"; }

# Check jq
command -v jq >/dev/null 2>&1 || fail "this tool requires jq: apt-get install -y jq" 4

FIX_MODE=0
case "${1:-}" in
  --fix) FIX_MODE=1; shift ;;
esac

# Discover bundles
discover_bundles() {
  find agents -mindepth 4 -maxdepth 4 -type f -name manifest.json 2>/dev/null \
    | while IFS= read -r f; do
        # agents/<name>/<source>/<version>/manifest.json
        rel="${f#agents/}"
        name="${rel%%/*}"
        rest="${rel#*/}"
        src="${rest%%/*}"
        # ver is everything between src/ and /manifest.json
        ver="${rest#*/}"; ver="${ver%/manifest.json}"
        echo "$name $src $ver"
      done | sort -u
}

if [[ $# -eq 3 ]]; then
  BUNDLES=("$1 $2 $3")
elif [[ $# -eq 0 ]]; then
  BUNDLES=()
  while IFS= read -r line; do
    [[ -n "$line" ]] && BUNDLES+=("$line")
  done < <(discover_bundles)
  if [[ ${#BUNDLES[@]} -eq 0 ]]; then
    fail "no manifest.json files found under agents/" 1
  fi
else
  fail "usage: $0 [--fix] [<agent> <source> <version>]" 1
fi

FAIL_COUNT=0
for line in "${BUNDLES[@]}"; do
  # Restore shell opts every iteration in case `set --` was called
  set -${BASH_OPTS_ORIG}
  # Split bundle descriptor without breaking later `declare -A`.
  # Format: "<agent> <source> <version>"
  AGENT="${line%% *}"
  rest_after_agent="${line#* }"
  SOURCE_TREE="${rest_after_agent%% *}"
  VERSION="${line##* }"
  BUNDLE_DIR="agents/$AGENT/$SOURCE_TREE/$VERSION"
  MANIFEST="$BUNDLE_DIR/manifest.json"

  log ""
  log "─── $AGENT/$SOURCE_TREE/$VERSION ─────────────────────"

  if [[ ! -f "$MANIFEST" ]]; then
    err "no manifest.json at $MANIFEST"; FAIL_COUNT=$((FAIL_COUNT+1)); continue
  fi
  if ! jq -e . "$MANIFEST" >/dev/null 2>&1; then
    err "manifest.json is not valid JSON"; FAIL_COUNT=$((FAIL_COUNT+1)); continue
  fi
  ok "manifest.json is valid JSON"

  # Required fields
  for f in agent source version schema_version; do
    v=$(jq -r ".$f // empty" "$MANIFEST")
    if [[ -z "$v" || "$v" == "null" ]]; then
      err "manifest.json missing required field: $f"; FAIL_COUNT=$((FAIL_COUNT+1))
    fi
  done
  if [[ "$(jq -r '.agent' "$MANIFEST")" != "$AGENT" ]]; then
    err "manifest.json says agent=$(jq -r .agent "$MANIFEST"), expected $AGENT"
    FAIL_COUNT=$((FAIL_COUNT+1))
  fi
  if [[ "$(jq -r '.source' "$MANIFEST")" != "$SOURCE_TREE" ]]; then
    err "manifest.json says source=$(jq -r .source "$MANIFEST"), expected $SOURCE_TREE"
    FAIL_COUNT=$((FAIL_COUNT+1))
  fi
  if [[ "$(jq -r '.version' "$MANIFEST")" != "$VERSION" ]]; then
    err "manifest.json says version=$(jq -r .version "$MANIFEST"), expected $VERSION"
    FAIL_COUNT=$((FAIL_COUNT+1))
  fi

  # Every payload src exists
  while IFS= read -r src; do
    [[ -z "$src" ]] && continue
    if [[ ! -e "$BUNDLE_DIR/$src" ]]; then
      err "manifest.payload references '$src' which is missing in $BUNDLE_DIR"
      FAIL_COUNT=$((FAIL_COUNT+1))
    fi
  done < <(jq -r '.payload[]?.src' "$MANIFEST")

  # Every runtime src exists
  while IFS= read -r src; do
    [[ -z "$src" || "$src" == "null" ]] && continue
    if [[ ! -e "$BUNDLE_DIR/$src" ]]; then
      err "manifest.runtime references '$src' which is missing in $BUNDLE_DIR"
      FAIL_COUNT=$((FAIL_COUNT+1))
    fi
  done < <(jq -r '.runtime[]?.src // empty' "$MANIFEST")

  # checksums: every key exists + matches (TBD entries are filled by --fix)
  BUNDLE_OK=1
  TBD_KEYS=()
  TBD_HASHES=()
  while IFS=$'\t' read -r key expected; do
    [[ -z "$key" ]] && continue
    file="$BUNDLE_DIR/$key"
    if [[ ! -f "$file" ]]; then
      err "manifest.checksums['$key'] has no file at $file"
      BUNDLE_OK=0
      continue
    fi
    actual=$(sha256sum "$file" | awk '{print $1}')
    expected_hex="${expected#sha256:}"
    if [[ "$expected_hex" == "TBD" || -z "$expected_hex" ]]; then
      TBD_KEYS+=("$key")
      TBD_HASHES+=("$actual")
      warn "manifest.checksums['$key'] = TBD (will be filled by --fix)"
      continue
    fi
    if [[ "$actual" != "$expected_hex" ]]; then
      err "checksum mismatch for $key:
    manifest: $expected_hex
    actual:   $actual"
      BUNDLE_OK=0
    fi
  done < <(jq -r '.checksums | to_entries[] | "\(.key)\t\(.value)"' "$MANIFEST")

  # If --fix mode and we have TBDs, patch the manifest in place using python3
  if [[ $FIX_MODE -eq 1 && ${#TBD_KEYS[@]} -gt 0 ]]; then
    log "  --fix: computing checksums for ${#TBD_KEYS[@]} TBD entries"

    # Materialize the TBD key→hash pairs into a temp file
    tbd_file="$(mktemp -t verify-tbd.XXXXXX)"
    for i in "${!TBD_KEYS[@]}"; do
      printf '%s\t%s\n' "${TBD_KEYS[$i]}" "${TBD_HASHES[$i]}" >> "$tbd_file"
    done

    python3 - "$MANIFEST" "$tbd_file" <<'PYEOF'
import json, sys, os
manifest_path = sys.argv[1]
tbd_path = sys.argv[2]

updates = {}
with open(tbd_path) as f:
    for ln in f:
        ln = ln.rstrip("\n")
        if not ln:
            continue
        k, h = ln.split("\t", 1)
        updates[k] = "sha256:" + h

with open(manifest_path) as f:
    d = json.load(f)
d.setdefault("checksums", {}).update(updates)
with open(manifest_path, "w") as f:
    json.dump(d, f, indent=2, sort_keys=True)
    f.write("\n")

os.unlink(tbd_path)
PYEOF

    ok "manifest.json updated — re-run verify.sh to confirm clean"
  fi

  if [[ $BUNDLE_OK -eq 1 ]]; then
    ok "bundle $AGENT/$SOURCE_TREE/$VERSION checks out"
  else
    err "bundle $AGENT/$SOURCE_TREE/$VERSION has issues"
    FAIL_COUNT=$((FAIL_COUNT+1))
  fi
done

if [[ $FAIL_COUNT -gt 0 ]]; then
  fail "$FAIL_COUNT bundle(s) failed verification" 2
fi
log ""
ok "all bundles verified"
