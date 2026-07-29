#!/usr/bin/env bash
# One-command launcher for the agent-marketplace-api **container image**.
#
# ─── What this script does ────────────────────────────────────────────────
#   Two phases, run in this order on `up`:
#     1. Configure  — copy deploy/config/marketplace-api.example.yaml →
#                    deploy/config/config.yaml (idempotent: existing
#                    config.yaml is left alone) + generate a self-signed
#                    TLS cert at deploy/tls/{tls.crt,tls.key} with
#                    EXTERNAL_IP as SAN.
#     2. Run       — wrap `docker run` with dist/, config.yaml, and the TLS
#                    dir bind-mounted. Only MARKETPLACE_API_PASSWORD travels
#                    via env; everything else reads from config.yaml at start
#                    time.
#
#   All structural settings live in deploy/config/marketplace-api.example.yaml.
#   Edit that file (or the generated deploy/config/config.yaml) to change them.
#   The binary re-reads config.yaml on every (re)start.
#
# ─── TLS — required. Self-signed when EXTERNAL_IP is provided ───────────
#   EXTERNAL_IP is REQUIRED (the script exits if unset). When set, a
#   self-signed cert + key are generated at deploy/tls/{tls.crt,tls.key}
#   with EXTERNAL_IP as both CN and Subject Alternative Name (IP + DNS).
#   The cert is bind-mounted into the container at /etc/agent-marketplace/tls/,
#   matching the tls_cert/tls_key paths in the generated config.yaml.
#
#   This is appropriate for a quick local-trust smoke test. For production
#   trust, replace the generated files with a CA-signed cert (mkcert, your
#   internal CA, Let's Encrypt, etc.) — the binary will pick them up on
#   next restart without code changes.
#
#   Loopback / unspecified EXTERNAL_IP values (localhost, 127.0.0.1, ::1)
#   are rejected because they'd produce a cert nothing on the network can
#   validate against. If you run a public-facing TLS terminator (nginx /
#   caddy / etc.) in front of marketplace-api, pass the terminator's IP
#   or hostname — the container itself still speaks HTTPS locally.
#
# ─── Required layout ──────────────────────────────────────────────────────
#   - dist/ at the repo root (build with: ./tools/pack.sh <agent> <source> <version>)
#   - deploy/config/marketplace-api.example.yaml present (ships in the repo)
#   - MARKETPLACE_API_PASSWORD env var or deploy/config/.env file (the secret)
#   - EXTERNAL_IP — REQUIRED. The self-signed cert's SAN matches this value;
#     pass your public IP/FQDN (or the IP/FQDN your TLS terminator exposes).
#
# Usage:
#   EXTERNAL_IP=192.168.1.42 ./start_marketplace_docker.sh          # configure + up (TLS)
#   ./start_marketplace_docker.sh                                   # fails fast (EXTERNAL_IP required)
#   ./start_marketplace_docker.sh up                                # alias
#   ./start_marketplace_docker.sh configure                         # emit config.yaml (and TLS)
#   ./start_marketplace_docker.sh down                              # stop container
#   ./start_marketplace_docker.sh clean                             # stop + remove
#   ./start_marketplace_docker.sh status                            # show container state
#   ./start_marketplace_docker.sh logs                              # tail -f container logs
#   ./start_marketplace_docker.sh --help                            # show this help
#
# After it boots:
#   curl --cacert deploy/tls/tls.crt -u admin:$MARKETPLACE_API_PASSWORD \\
#        https://<EXTERNAL_IP>:8443/api/v1/index
set -euo pipefail

# ╔═══════════════════════════════════════════════════════════════════════╗
# ║ USER-FACING SETTINGS                                                   ║
# ╚═══════════════════════════════════════════════════════════════════════╝

# TLS — REQUIRED. The self-signed cert's Subject Alternative Name (SAN) is
# set to this value, so it must be the IP or FQDN clients will reach. Pass
# your public IP (or the IP/FQDN your public TLS terminator exposes, if
# you put nginx/caddy in front). Loopback addresses (localhost, 127.0.0.1,
# 0.0.0.0, ::1) are rejected because they'd produce a cert nothing on the
# network can validate against. Empty string → fails fast below.
EXTERNAL_IP="${EXTERNAL_IP:-}"

# Image.
IMAGE_REPO="${IMAGE_REPO:-quay.io/vmware-ai/agent-marketplace-api}"
IMAGE_TAG="${IMAGE_TAG:-latest}"
IMAGE="${IMAGE_REPO}:${IMAGE_TAG}"

# Container name. Override to run a second instance side-by-side.
CONTAINER_NAME="${CONTAINER_NAME:-agent-marketplace-api}"

# Host port to publish the API on. The container always listens on 8443
# (deploy/docker/Dockerfile EXPOSE); this maps host_port → 8443.
HOST_PORT="${HOST_PORT:-8443}"

# Where the launched container reads its config from on the host. By
# default the script reads the immutable template from deploy/config/
# and writes the live config to deploy/config/config.yaml (next to the
# template). User edits to the live path are preserved on subsequent runs.
CONFIG_PATH="${CONFIG_PATH:-${BASH_SOURCE[0]%/*}/config/config.yaml}"
EXAMPLE_CONFIG_PATH="${BASH_SOURCE[0]%/*}/config/marketplace-api.example.yaml"
TLS_DIR="${BASH_SOURCE[0]%/*}/tls"

# Run the container as this host UID:GID so bind-mounted files (config,
# dist/, tls/) are readable. Defaults to the current user's numeric id.
HOST_UID="${HOST_UID:-$(id -u)}"
HOST_GID="${HOST_GID:-$(id -g)}"

# ╔═══════════════════════════════════════════════════════════════════════╗
# ║ DERIVED SETTINGS                                                       ║
# ╚═══════════════════════════════════════════════════════════════════════╝

# ---------- required: EXTERNAL_IP ----------
# REQUIRED — fail fast. The cert SAN = EXTERNAL_IP, so an empty / loopback
# value would produce a useless cert. Caught here (vs inside generate_tls)
# so the error message is unambiguous and the script exits before any preflight.
if [[ -z "${EXTERNAL_IP}" ]]; then
  echo "error: EXTERNAL_IP is required (cert SAN)." >&2
  echo "         pass the IP or FQDN clients will reach, e.g.:" >&2
  echo "           EXTERNAL_IP=192.168.1.42 $0" >&2
  echo "         for a TLS-terminator-fronted deployment, pass the IP/FQDN the" >&2
  echo "         terminator exposes (the container still talks HTTPS locally)." >&2
  exit 1
fi
case "${EXTERNAL_IP}" in
  localhost|127.0.0.1|0.0.0.0|::1)
    echo "error: EXTERNAL_IP='${EXTERNAL_IP}' is a loopback / unspecified address." >&2
    echo "         cert SANs must be reachable from clients, not the local host." >&2
    echo "         Re-run with: EXTERNAL_IP=<your LAN/public IP> $0" >&2
    exit 1
    ;;
esac

usage() {
  sed -n '2,46p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//' | tr -d '\r'
}

ACTION="${1:-up}"
case "${ACTION}" in
  -h|--help|help)
    usage
    exit 0
    ;;
  up|configure|down|clean|status|logs)
    shift || true
    ;;
  *)
    echo "usage: $0 [up|configure|down|clean|status|logs]" >&2
    echo "       (default action is 'up'; '--help' for full options)" >&2
    exit 2
    ;;
esac

# Resolve paths.
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"
HOST_DIST_DIR="${REPO_ROOT}/dist"

# ---------- preflight: docker ----------
if ! command -v docker >/dev/null 2>&1; then
  echo "error: docker not found in PATH" >&2
  exit 1
fi
if ! docker info >/dev/null 2>&1; then
  echo "error: docker daemon is not reachable (is the docker desktop / service running?)" >&2
  exit 1
fi

# Helper.
container_exists() {
  docker ps -a --format '{{.Names}}' | grep -qx "${CONTAINER_NAME}"
}

# ---------- status action ----------
if [[ "${ACTION}" == "status" ]]; then
  if container_exists; then
    echo "container '${CONTAINER_NAME}':"
    docker ps -a --filter "name=^${CONTAINER_NAME}\$" --format \
      "  {{.Status}}  image={{.Image}}  ports={{.Ports}}"
  else
    echo "container '${CONTAINER_NAME}' is not present."
  fi
  exit 0
fi

# ---------- logs action ----------
if [[ "${ACTION}" == "logs" ]]; then
  if ! container_exists; then
    echo "container '${CONTAINER_NAME}' is not present." >&2
    exit 0
  fi
  exec docker logs -f --tail 200 "${CONTAINER_NAME}"
fi

# ---------- down / clean ----------
if [[ "${ACTION}" == "down" ]]; then
  if container_exists; then
    echo "stopping '${CONTAINER_NAME}'…"
    docker stop "${CONTAINER_NAME}" >/dev/null
    echo "container stopped (kept on disk; run '$0 up' to start it again)."
  else
    echo "nothing to stop: container '${CONTAINER_NAME}' is not present."
  fi
  exit 0
fi

if [[ "${ACTION}" == "clean" ]]; then
  if container_exists; then
    echo "removing '${CONTAINER_NAME}'…"
    docker rm -f "${CONTAINER_NAME}" >/dev/null
    echo "container removed (next '$0 up' will re-pull the image and create a fresh one)."
  else
    echo "nothing to remove: container '${CONTAINER_NAME}' is not present."
  fi
  exit 0
fi

# ---------- configure phase (also runs as part of `up`) ----------

# Emit deploy/config/config.yaml by copying the example template.
# Idempotent: if config.yaml already exists we leave it alone (so user
# edits survive).
emit_config() {
  if [[ -f "${CONFIG_PATH}" ]]; then
    echo "${CONFIG_PATH} already exists; leaving it untouched."
    return
  fi
  if [[ ! -f "${EXAMPLE_CONFIG_PATH}" ]]; then
    echo "error: ${EXAMPLE_CONFIG_PATH} not found (ships in the repo; re-clone?)" >&2
    exit 1
  fi
  cp "${EXAMPLE_CONFIG_PATH}" "${CONFIG_PATH}"
  echo "wrote ${CONFIG_PATH} (copy of example)"
}

# Generate a self-signed cert at deploy/tls/. EXTERNAL_IP is REQUIRED
# (the script exits earlier if unset), so the "skipping TLS" branch is
# gone — this function always generates. EXTERNAL_IP validation (loopback
# / unspecified) is done in the preflight above, so we don't repeat it here.
generate_tls() {
  echo "generating self-signed cert (EXTERNAL_IP=${EXTERNAL_IP}) → ${TLS_DIR}/"
  mkdir -p "${TLS_DIR}"
  chmod 700 "${TLS_DIR}"

  if ! command -v openssl >/dev/null 2>&1; then
    echo "error: openssl not found in PATH (required to generate cert)." >&2
    echo "         install openssl, then re-run." >&2
    exit 1
  fi

  # 1-year validity, RSA 2048, SHA-256. SAN = EXTERNAL_IP as both IP and
  # DNS forms so the cert validates regardless of how the client spells
  # the address. CN is also set to EXTERNAL_IP for older TLS clients
  # that look at CN instead of SAN.
  if ! openssl req -x509 -newkey rsa:2048 -nodes \
       -keyout "${TLS_DIR}/tls.key" \
       -out "${TLS_DIR}/tls.crt" \
       -days 365 \
       -subj "/CN=${EXTERNAL_IP}" \
       -addext "subjectAltName=IP:${EXTERNAL_IP},DNS:${EXTERNAL_IP}" \
       2>/dev/null; then
    echo "error: openssl failed to generate cert in ${TLS_DIR}" >&2
    rm -rf "${TLS_DIR}"
    exit 1
  fi
  chmod 600 "${TLS_DIR}/tls.key"
  chmod 644 "${TLS_DIR}/tls.crt"
  echo "  ${TLS_DIR}/tls.crt  (CN=${EXTERNAL_IP}, SAN=IP:${EXTERNAL_IP},DNS:${EXTERNAL_IP})"
  echo "  ${TLS_DIR}/tls.key"
}

# ---------- preflight for up ----------
# (Runs below the configure helpers so the configure helpers can be invoked
# standalone via `$0 configure`.)

if [[ "${ACTION}" == "configure" ]]; then
  emit_config
  generate_tls
  exit 0
fi

# All actions below require docker, dist/, password, and a free HOST_PORT.
# (EXTERNAL_IP is REQUIRED + preflighted above; generate_tls always runs.)
generate_tls

# ---------- preflight: layout ----------
if [[ ! -d "${HOST_DIST_DIR}" ]]; then
  echo "error: ${HOST_DIST_DIR} not found." >&2
  echo "         populate it with: (cd ${REPO_ROOT} && ./tools/pack.sh <agent> <source> <version>)" >&2
  exit 1
fi
if [[ ! -f "${HOST_DIST_DIR}/index.json" ]]; then
  echo "error: ${HOST_DIST_DIR}/index.json not found (build tarballs first)" >&2
  echo "         (cd ${REPO_ROOT} && ./tools/pack.sh <agent> <source> <version>)" >&2
  exit 1
fi

# ---------- preflight: config.yaml present ----------
# Emit it from the template if it doesn't exist. Distinguish "the script
# itself didn't emit yet" from "user deleted and wants to start fresh."
# (emit_config is no-op when file exists, matching idempotence goal.)
emit_config

# ---------- preflight: password ----------
PASS_SOURCE="env"
ENV_FILE="${SCRIPT_DIR}/config/.env"
if [[ -z "${MARKETPLACE_API_PASSWORD:-}" && -f "${ENV_FILE}" ]]; then
  # shellcheck disable=SC1090
  ( set +e; source "${ENV_FILE}"; set -e ) >/dev/null 2>&1 || true
  PASS_LINE="$(grep -E '^MARKETPLACE_API_PASSWORD=' "${ENV_FILE}" || true)"
  if [[ -z "${MARKETPLACE_API_PASSWORD:-}" && -n "${PASS_LINE}" && "${PASS_LINE}" != *"change-me"* ]]; then
    MARKETPLACE_API_PASSWORD="${PASS_LINE#MARKETPLACE_API_PASSWORD=}"
    PASS_SOURCE=".env"
  fi
fi
if [[ -z "${MARKETPLACE_API_PASSWORD:-}" ]] || [[ "${MARKETPLACE_API_PASSWORD}" == *"change-me"* ]]; then
  echo "error: MARKETPLACE_API_PASSWORD is unset or still the placeholder." >&2
  echo "         set it in your shell, or in deploy/config/.env, then re-run." >&2
  exit 1
fi

# ---------- preflight: HOST_PORT is free ----------
if command -v lsof >/dev/null 2>&1; then
  if lsof -nP -iTCP:"${HOST_PORT}" -sTCP:LISTEN >/dev/null 2>&1; then
    echo "error: host port ${HOST_PORT} is already in use (HOST_PORT=${HOST_PORT})." >&2
    lsof -nP -iTCP:"${HOST_PORT}" -sTCP:LISTEN >&2 || true
    exit 1
  fi
elif command -v ss >/dev/null 2>&1; then
  if ss -ltn "sport = :${HOST_PORT}" 2>/dev/null | tail -n +2 | grep -q .; then
    echo "error: host port ${HOST_PORT} is already in use (HOST_PORT=${HOST_PORT})." >&2
    exit 1
  fi
else
  if netstat -ltn 2>/dev/null | awk '{print $4}' | grep -E "[.:]${HOST_PORT}\$" >/dev/null; then
    echo "error: host port ${HOST_PORT} is already in use (HOST_PORT=${HOST_PORT})." >&2
    exit 1
  fi
fi

# ---------- preflight: remove a stale container with our name ----------
if container_exists; then
  echo "removing stale container '${CONTAINER_NAME}' from a previous run"
  docker rm -f "${CONTAINER_NAME}" >/dev/null
fi

# ---------- determine TLS volume mount ----------
# EXTERNAL_IP is REQUIRED (preflighted above), so generate_tls always
# succeeded and tls.crt/tls.key are guaranteed to exist. Mount unconditionally.
TLS_VOLUME=( -v "${TLS_DIR}:/etc/agent-marketplace/tls:ro" )

# ---------- run ----------
echo
echo "────────────────────────────────────────────────────────────"
echo " agent-marketplace-api (docker)"
echo "   image:       ${IMAGE}"
echo "   container:   ${CONTAINER_NAME}"
echo "   HOST_PORT=${HOST_PORT}  →  container :8443"
echo "   config:      ${CONFIG_PATH}  (read-only mount)"
echo "   dist:        ${HOST_DIST_DIR}  →  /var/lib/agent-marketplace/dist (read-only mount)"
echo "   TLS:         ${TLS_DIR}  →  /etc/agent-marketplace/tls (read-only mount)"
echo "   password:    ${PASS_SOURCE}  (env var MARKETPLACE_API_PASSWORD)"
echo "   TLS SAN:     EXTERNAL_IP=${EXTERNAL_IP}"
echo "                trust: --cacert ${TLS_DIR}/tls.crt"
echo "────────────────────────────────────────────────────────────"
echo
echo "(pulling ${IMAGE}; Ctrl-C to stop and remove the container)"
echo

# --rm         remove container on clean exit
# --pull=always always re-resolve the tag (:dev → fresh build on every run)
# -p           honor HOST_PORT by mapping host_port → 8443 (image's EXPOSE)
# -v           bind-mount config.yaml + dist/ [ + tls/ ] so the container reads from host
# -u HOST_UID:HOST_GID so the container can read bind-mounted files owned
#              by the host user (config.yaml, dist/, tls/)
# -e           pass MARKETPLACE_API_PASSWORD (the only secret). All other
#              settings come from config.yaml at start time.
exec docker run \
  --rm \
  -d \
  --pull=always \
  --name "${CONTAINER_NAME}" \
  -p "${HOST_PORT}:8443" \
  -u "${HOST_UID}:${HOST_GID}" \
  -e "MARKETPLACE_API_PASSWORD=${MARKETPLACE_API_PASSWORD}" \
  -v "${CONFIG_PATH}:/etc/agent-marketplace/config.yaml:ro" \
  -v "${HOST_DIST_DIR}:/var/lib/agent-marketplace/dist:ro" \
  "${TLS_VOLUME[@]}" \
  "${IMAGE}"