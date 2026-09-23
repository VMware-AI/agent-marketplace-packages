#!/bin/bash
# skills-smoke.sh — End-to-end exercise of the skills registry lifecycle.
#
# Launches a local marketplace-api on 127.0.0.1:18443 against a fresh dist
# under /tmp/skills-smoke, then runs the full author → repo → consumer →
# delete flow against it via the agentpkg CLI. Used by `make skills-smoke`.
#
# Assumes `make build` has produced ./bin/{marketplace-api,agentpkg}.
# Sets a trap so a Ctrl-C / failure cleans up the spawned server + temp dir.

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
SMOKE_DIR=/tmp/skills-smoke
PORT=18443
PASSWORD=testpw
API="https://127.0.0.1:${PORT}"
CREDS_DIR="$SMOKE_DIR/creds"
WORK_DIR="$SMOKE_DIR/work"

cleanup() {
  if [[ -n "${SERVER_PID:-}" ]]; then
    kill "$SERVER_PID" 2>/dev/null || true
    wait "$SERVER_PID" 2>/dev/null || true
  fi
  # Leave $SMOKE_DIR around on success for post-mortem inspection;
  # `make skills-smoke-clean` removes it explicitly.
  if [[ "${SMOKE_CLEAN:-0}" == "1" ]]; then
    rm -rf "$SMOKE_DIR"
  fi
}
trap cleanup EXIT

# 0. Reset the smoke dir + seed it with the real repo's dist (so the
# agents index loads — we still verify the skills paths are empty).
mkdir -p "$SMOKE_DIR" "$CREDS_DIR" "$WORK_DIR"
cp -r "$REPO_ROOT/dist" "$SMOKE_DIR/dist"
# Wipe any pre-existing skills (so the test starts from a clean slate).
rm -rf "$SMOKE_DIR/dist/skills" "$SMOKE_DIR/dist/skills-index.json"

# 1. Write the marketplace-api config + credentials.
cat > "$SMOKE_DIR/config.yaml" <<EOF
server:
  listen: "127.0.0.1:${PORT}"
  tls_cert: "${REPO_ROOT}/deploy/tls/tls.crt"
  tls_key:  "${REPO_ROOT}/deploy/tls/tls.key"
auth:
  password_env: "MARKETPLACE_API_PASSWORD"
repo:
  dist_dir: "${SMOKE_DIR}/dist"
logging:
  level: "warn"
  format: "text"
  file: ""
EOF

# The agent CLI gets its OWN config.yaml (login would overwrite it with
# the CLI's format, which would then break subsequent marketplace-api reads
# if we pointed --config at the same path).
cat > "$CREDS_DIR/credentials" <<EOF
password: ${PASSWORD}
EOF
chmod 600 "$CREDS_DIR/credentials"
CLI_CONFIG="$CREDS_DIR/cli-config.yaml"
cat > "$CLI_CONFIG" <<EOF
server: "${API}"
skip_cert_verify: true
EOF

# 2. Launch the server in the background.
MARKETPLACE_API_PASSWORD="$PASSWORD" "$REPO_ROOT/bin/marketplace-api" \
  --config "$SMOKE_DIR/config.yaml" > "$SMOKE_DIR/server.log" 2>&1 &
SERVER_PID=$!
# Wait for /api/v1/health to come up (max 5s).
for i in $(seq 1 25); do
  if curl -ksf "$API/api/v1/health" >/dev/null 2>&1; then
    break
  fi
  sleep 0.2
  if (( i == 25 )); then
    echo "FAIL: marketplace-api didn't start within 5s" >&2
    cat "$SMOKE_DIR/server.log" >&2
    exit 1
  fi
done

CLI=("$REPO_ROOT/bin/agentpkg"
     --config    "$CLI_CONFIG"
     --credentials "$CREDS_DIR/credentials")

step() { printf '\n\033[1;34m== %s ==\033[0m\n' "$*"; }
ok()   { printf '  \033[1;32m✓\033[0m %s\n' "$*"; }

# 3. login populates the config.yaml (already seeded above — login again
# to confirm it works against the running server).
step "login"
"${CLI[@]}" login --server "$API" --skip-cert-verify --password-stdin <<<"$PASSWORD" >/dev/null
ok "logged in"

# 4. Author-side: init → verify → build.
step "author: init + verify + build"
SKILL_DIR="$WORK_DIR/skills/hello"
"$REPO_ROOT/bin/agentpkg" skills init hello --dir "$SKILL_DIR" >/dev/null
"$REPO_ROOT/bin/agentpkg" skills verify "$SKILL_DIR" >/dev/null
ZIP_PATH="$WORK_DIR/dist/skills/hello-community-0.1.0.zip"
"$REPO_ROOT/bin/agentpkg" skills build "$SKILL_DIR" --version 0.1.0 --out "$WORK_DIR/dist" >/dev/null
[[ -f "$ZIP_PATH" ]] || { echo "FAIL: build didn't produce zip" >&2; exit 1; }
ok "built $ZIP_PATH"

# 5. Repo-side: upload + 409 re-upload + list + show + download.
step "repo: upload + 409 + list + show + download"
"${CLI[@]}" skills upload "$ZIP_PATH" >/dev/null
ok "uploaded community/hello/0.1.0"
# Re-upload should return 409. Capture stderr+stdout to a tmpfile so the
# pipefail strict-mode doesn't trip on the failing exit status.
TMP_REUP=$(mktemp)
"${CLI[@]}" skills upload "$ZIP_PATH" >"$TMP_REUP" 2>&1 || true
if grep -q 'HTTP 409' "$TMP_REUP"; then
  ok "re-upload returned 409 (immutability)"
else
  echo "FAIL: re-upload did not return 409 (got: $(cat "$TMP_REUP"))" >&2
  rm -f "$TMP_REUP"; exit 1
fi
rm -f "$TMP_REUP"
LIST_OUT=$("${CLI[@]}" skills list 2>/dev/null || true)
echo "$LIST_OUT" | grep -q 'hello' || { echo "FAIL: hello not in skills list" >&2; exit 1; }
ok "list shows hello"
SHOW_OUT=$("${CLI[@]}" skills show hello 2>/dev/null || true)
echo "$SHOW_OUT" | grep -q '0.1.0' || { echo "FAIL: show missing 0.1.0" >&2; exit 1; }
ok "show includes 0.1.0"
DL_DIR="$WORK_DIR/dl"
mkdir -p "$DL_DIR"
"${CLI[@]}" skills download hello --version 0.1.0 -o "$DL_DIR/hello.zip" >/dev/null
[[ -f "$DL_DIR/hello.zip.sha256" ]] || { echo "FAIL: sidecar missing" >&2; exit 1; }
DL_HASH=$(sha256sum "$DL_DIR/hello.zip" | awk '{print $1}')
SIDECAR_HASH=$(awk '{print $1}' "$DL_DIR/hello.zip.sha256")
[[ "$DL_HASH" == "$SIDECAR_HASH" ]] || { echo "FAIL: download hash mismatch" >&2; exit 1; }
ok "download + sidecar sha256 match ($DL_HASH)"

# 6. Raw SKILL.md endpoint must serve byte-identical content.
step "raw SKILL.md byte-equality"
curl -ksu "agentpkg:$PASSWORD" "$API/api/v1/skills/community/hello/0.1.0/SKILL.md" > "$SMOKE_DIR/served.md"
diff -q "$SMOKE_DIR/served.md" "$SKILL_DIR/SKILL.md" >/dev/null \
  || { echo "FAIL: SKILL.md endpoint doesn't match local" >&2; exit 1; }
ok "SKILL.md endpoint byte-identical"

# 7. Consumer-side: install + list-installed + uninstall + list-installed.
step "consumer: install + list-installed + uninstall"
INSTALL_DIR="$SMOKE_DIR/install"
"${CLI[@]}" skills install hello --version 0.1.0 --target-dir "$INSTALL_DIR" >/dev/null
[[ -d "$INSTALL_DIR/hello/0.1.0" ]] || { echo "FAIL: install dir missing" >&2; exit 1; }
[[ -L "$INSTALL_DIR/hello/latest" ]] || { echo "FAIL: latest symlink missing" >&2; exit 1; }
LATEST=$(readlink "$INSTALL_DIR/hello/latest")
[[ "$LATEST" == "0.1.0" ]] || { echo "FAIL: latest → $LATEST, want 0.1.0" >&2; exit 1; }
ok "installed to $INSTALL_DIR/hello/0.1.0 (latest → 0.1.0)"
LI_OUT=$("$REPO_ROOT/bin/agentpkg" skills list-installed --target-dir "$INSTALL_DIR" 2>/dev/null)
echo "$LI_OUT" | grep -q 'hello' || { echo "FAIL: list-installed missing hello" >&2; exit 1; }
ok "list-installed shows hello"
# Idempotency check: re-install with the same sha256 should be a no-op.
RE_OUT=$("${CLI[@]}" skills install hello --version 0.1.0 --target-dir "$INSTALL_DIR" 2>&1 || true)
echo "$RE_OUT" | grep -q 'already installed' || { echo "FAIL: re-install wasn't idempotent" >&2; exit 1; }
ok "re-install is idempotent"
"$REPO_ROOT/bin/agentpkg" skills uninstall hello --target-dir "$INSTALL_DIR" >/dev/null
[[ ! -d "$INSTALL_DIR/hello" ]] || { echo "FAIL: uninstall left dir" >&2; exit 1; }
ok "uninstall removed skill dir"

# 8. Cleanup server-side so a re-run starts from empty.
step "server-side cleanup"
"${CLI[@]}" skills delete hello --source community --version 0.1.0 >/dev/null
LIST_FINAL=$("${CLI[@]}" skills list 2>/dev/null || true)
echo "$LIST_FINAL" | grep -q 'hello' \
  && { echo "FAIL: hello still in list after delete" >&2; exit 1; }
ok "delete removed from registry"

printf '\n\033[1;32m== ALL SMOKE TESTS PASSED ==\033[0m\n'

# Clean up only on successful exit.
SMOKE_CLEAN=1 cleanup