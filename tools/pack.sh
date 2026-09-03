#!/usr/bin/env bash
# pack.sh — produce dist/<agent>-<source>-<version>.tar.gz + .sha256
#
# Usage:
#   tools/pack.sh <agent> <source> <version> [out-dir]
#
# The tarball's root is the version directory itself — i.e. when extracted,
# you land in agents/<agent>/<source>/<version>/ and can immediately run
# ./install.sh.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
cd "$REPO_ROOT"

log()  { echo "[pack] $*" >&2; }
ok()   { echo "[pack] OK: $*" >&2; }
err()  { echo "[pack] ERR: $*" >&2; exit 1; }

[[ $# -ge 3 ]] || { echo "usage: $0 <agent> <source> <version> [out-dir]" >&2; exit 1; }
AGENT="$1"; SOURCE="$2"; VERSION="$3"; OUT_DIR="${4:-dist}"
BUNDLE_DIR="agents/$AGENT/$SOURCE/$VERSION"

[[ -d "$BUNDLE_DIR" ]] || err "$BUNDLE_DIR does not exist"
[[ -f "$BUNDLE_DIR/manifest.json" ]] || err "$BUNDLE_DIR/manifest.json missing"
[[ -x "$BUNDLE_DIR/install.sh" ]] || err "$BUNDLE_DIR/install.sh is missing or not executable"

mkdir -p "$OUT_DIR"

TARBALL_NAME="${AGENT}-${SOURCE}-${VERSION}.tar.gz"
TARBALL_PATH="$OUT_DIR/$TARBALL_NAME"
SHA_PATH="$TARBALL_PATH.sha256"

log "creating $TARBALL_PATH"
log "  root:  $BUNDLE_DIR/"

# Count files we will pack: every file under $BUNDLE_DIR, plus meta.yaml at
# the agent root if present. meta.yaml is agent-level (not version-level)
# marketing data — display_name / description / logo / category / tags /
# runtime_type — and the marketplace-api reindex expects to find it at
# the tarball root so each Agent entry in dist/index.json gets populated.
# Without it, the consumer UI shows blank cards. See buildIndexFromDir
# in internal/cli/packagecmd/build_index.go for the reader side.
META_FILE="agents/$AGENT/meta.yaml"
HAS_META=0
if [[ -f "$META_FILE" ]]; then
  HAS_META=1
fi
# Where meta.yaml lives: two dirname hops above BUNDLE_DIR.
#   BUNDLE_DIR         = agents/<agent>/<source>/<version>
#   dirname x1         = agents/<agent>/<source>
#   dirname x2         = agents/<agent>             ← where meta.yaml lives
AGENT_ROOT_DIR="$(dirname "$(dirname "$BUNDLE_DIR")")"
FILE_COUNT=$(find "$BUNDLE_DIR" -type f | wc -l | tr -d ' ')
if [[ "$HAS_META" -eq 1 ]]; then
  FILE_COUNT=$((FILE_COUNT + 1))
fi
log "  files: $FILE_COUNT"

# Build the tarball. We cd into the parent so the tarball's root is the
# version directory name itself (not its absolute path).
BUNDLE_PARENT="$(dirname "$BUNDLE_DIR")"
BUNDLE_BASE="$(basename "$BUNDLE_DIR")"

# Self-referential sha is fundamentally unstable: the embedded manifest
# is part of the bytes being hashed, so any "real" value we stamp in
# makes the hash stale. We sidestep this by keeping
# manifest.tarball.sha256 = "sha256:TBD" as the SOURCE-OF-TRUTH-FOR-EMBEDDED-MANIFEST
# marker, and storing the authoritative hash only in two places:
#   1. $TARBALL_PATH.sha256 (sidecar next to the tarball)
#   2. dist/index.json (consumed by marketplace-api via package reindex)
#
# If a user accidentally committed a real (but stale) hash in tarball.sha256,
# fix it here. We match the literal field shape to avoid false positives
# from "upstream.sha256" (which is genuinely TBD for npm-style agents).
python3 - "$BUNDLE_DIR/manifest.json" "$TARBALL_NAME" <<'PY'
import json, os, sys, hashlib
manifest_path = sys.argv[1]
tarball_name = sys.argv[2]
bundle_dir = os.path.dirname(manifest_path)
with open(manifest_path) as f:
    d = json.load(f)

# 1) tarball.sha256 must be TBD in the embedded manifest.
d.setdefault("tarball", {})
d["tarball"]["sha256"] = "sha256:TBD"
d["tarball"]["filename"] = tarball_name

# 2) Recompute checksums for every file that ends up in the tarball.
# install.sh iterates manifest.checksums, so any stale entry there causes
# an install-time "checksum mismatch" failure even when the bytes are fine.
# We compute fresh sha256 for every file referenced in payload[], runtime[],
# plus install.sh / uninstall.sh. If a key in the existing checksums no
# longer matches any source, drop it.
def sha256_file(rel):
    p = os.path.join(bundle_dir, rel)
    h = hashlib.sha256()
    with open(p, "rb") as f:
        for chunk in iter(lambda: f.read(1 << 16), b""):
            h.update(chunk)
    return "sha256:" + h.hexdigest()

referenced = set()
for src in d.get("payload", []) or []:
    if src.get("src"):
        referenced.add(src["src"])
for src in d.get("runtime", []) or []:
    s = src.get("src")
    if s:
        referenced.add(s)
for script in ("install.sh", "uninstall.sh"):
    if os.path.exists(os.path.join(bundle_dir, script)):
        referenced.add(script)

# Only regular files go into checksums — install.sh iterates this map and
# sha256sums each entry, so directories would error out. Openclaw/hermes
# ship whole directories (npm tree, python runtime) and intentionally omit
# them from checksums; the per-file verification there is the install.sh's
# job via `cp -a`.
new_checksums = {}
for rel in sorted(referenced):
    full = os.path.join(bundle_dir, rel)
    if not os.path.isfile(full):
        continue
    new_checksums[rel] = sha256_file(rel)
d["checksums"] = new_checksums

with open(manifest_path, "w") as f:
    json.dump(d, f, indent=2, sort_keys=True)
    f.write("\n")
print("[pack] OK: rewrote checksums for %d files" % len(new_checksums))
PY

# Stage tarball into a temp file so we can hash it before publishing.
TMP_TARBALL="$(mktemp -t pack.XXXXXX.tar.gz)"
trap 'rm -f "$TMP_TARBALL"' EXIT

# Build the tarball. The first time after a `cp -r` from a non-Linux host,
# macOS-extended tar will pick up AppleDouble (._*) fork files and xattr
# headers — those confuse the Linux extractor downstream (Python's venv
# aborts on the `._activate` files, etc.). COPYFILE_DISABLE=1 + --no-xattrs
# + --no-mac-metadata strip them so the consumer only sees the canonical
# payload. Same set of flags works under GNU tar on Linux (no-ops there).
TMP_TARBALL="$(mktemp -t pack.XXXXXX.tar.gz)"
trap 'rm -f "$TMP_TARBALL"' EXIT

if [[ "$HAS_META" -eq 1 ]]; then
  # Embed meta.yaml at the tarball root + the version dir below it, in one
  # tar invocation. This mirrors what `package build`'s build.go does on
  # the Go side, and what buildIndexFromDir's extractTarballFiles expects
  # to read.
  #
  # bsdtar (macOS default) doesn't accept multiple `-C` flags, so we stage
  # into a temp dir first: symlink meta.yaml + the version dir side by
  # side, then tar the staging root. This is platform-portable (GNU tar
  # on Linux would do it in one shot via two `-C`s, but we want the same
  # script to work on both without GNU-tar dependency).
  STAGE="$(mktemp -d -t pack-stage.XXXXXX)"
  trap 'rm -rf "$STAGE"' EXIT
  ln -s "$REPO_ROOT/$BUNDLE_PARENT/$BUNDLE_BASE" "$STAGE/$BUNDLE_BASE"
  ln -s "$REPO_ROOT/$AGENT_ROOT_DIR/meta.yaml" "$STAGE/meta.yaml"
  if COPYFILE_DISABLE=1 tar -czf "$TMP_TARBALL" \
       -h \
       --owner=0 --group=0 --numeric-owner \
       --no-xattrs --no-mac-metadata \
       -C "$STAGE" "meta.yaml" "$BUNDLE_BASE"; then
    :
  else
    err "tar failed (with meta.yaml)"
  fi
else
  if COPYFILE_DISABLE=1 tar -czf "$TMP_TARBALL" \
       --owner=0 --group=0 --numeric-owner \
       --no-xattrs --no-mac-metadata \
       -C "$BUNDLE_PARENT" \
       "$BUNDLE_BASE"; then
    :
  else
    err "tar failed"
  fi
fi

TAR_SHA="$(sha256sum "$TMP_TARBALL" | awk '{print $1}')"
ok "tarball SHA256: $TAR_SHA"

# Move staged tarball into place + write sidecar sha256 file. The sidecar
# IS the authoritative hash for these bytes — install.sh and the CLI
# both trust it (the latter via the /sha256 sidecar endpoint).
mv "$TMP_TARBALL" "$TARBALL_PATH"
trap - EXIT  # disarm the cleanup; we already moved the file
sha256sum "$TARBALL_PATH" > "$SHA_PATH"

# Also patch dist/index.json in-place if it exists, so the marketplace-api
# serves the correct size + sha without a separate `package reindex` step.
INDEX_PATH="$OUT_DIR/index.json"
if [[ -f "$INDEX_PATH" ]]; then
  INDEX_PATH_REAL="$INDEX_PATH" \
    TAR_NAME="$TARBALL_NAME" TAR_PATH="$TARBALL_PATH" TAR_SHA_VAL="$TAR_SHA" \
    AGENT_VAL="$AGENT" SRC_VAL="$SOURCE" VER_VAL="$VERSION" \
    python3 - <<'PY'
import json, os
p = os.environ["INDEX_PATH_REAL"]
idx = json.load(open(p))
tbs = os.path.getsize(os.environ["TAR_PATH"])
sha = os.environ["TAR_SHA_VAL"]
agent_name = os.environ["AGENT_VAL"]
source = os.environ["SRC_VAL"]
version = os.environ["VER_VAL"]
patched = False
for a in idx.get("agents", []):
    if a.get("name") != agent_name:
        continue
    for v in a.get("versions", []):
        if v.get("source") == source and v.get("version") == version:
            v["tarball"]["filename"] = os.environ["TAR_NAME"]
            v["tarball"]["size_bytes"] = tbs
            v["tarball"]["sha256"] = "sha256:" + sha
            if "manifest" in v and isinstance(v["manifest"], dict) and "tarball" in v["manifest"]:
                v["manifest"]["tarball"]["sha256"] = "sha256:TBD"
            patched = True
if patched:
    json.dump(idx, open(p, "w"), indent=2, ensure_ascii=False)
    print("[pack] OK: patched index.json entry for %s/%s/%s" % (agent_name, source, version))
else:
    print("[pack] WARN: no matching entry in %s — run the package reindex subcommand" % p)
PY
fi

ok "wrote $TARBALL_PATH ($(du -h "$TARBALL_PATH" | awk '{print $1}'))"
ok "wrote $SHA_PATH"
log ""
log "to ship:"
log "  rsync -avP $TARBALL_PATH $SHA_PATH your-cdn:/path/to/agents/"