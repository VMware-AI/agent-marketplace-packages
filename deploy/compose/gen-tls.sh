#!/usr/bin/env bash
# Generate a self-signed TLS cert into deploy/tls/ for use with the
# docker-compose stack (which bind-mounts ../tls → /etc/agent-marketplace/tls
# read-only). Adapted from generate_tls() in start_marketplace_docker.sh —
# kept in sync with that script so the two paths produce identical certs.
#
# Usage:
#   EXTERNAL_IP=<ip-or-fqdn> ./gen-tls.sh
#
# Output:
#   ../tls/tls.crt
#   ../tls/tls.key
#
# The cert is valid for 1 year, RSA 2048. SAN = EXTERNAL_IP as both IP and
# DNS forms so clients can validate regardless of how they spell the address.
# CN is also set to EXTERNAL_IP for older TLS clients that look at CN instead
# of SAN.
#
# EXTERNAL_IP is required (the cert SAN must be reachable from clients, not
# loopback). Set it to your LAN/public IP, or the IP/FQDN a TLS terminator
# in front of the API exposes.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
TLS_DIR="${SCRIPT_DIR}/../tls"

EXTERNAL_IP="${EXTERNAL_IP:-}"
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

if ! command -v openssl >/dev/null 2>&1; then
  echo "error: openssl not found in PATH (required to generate cert)." >&2
  echo "         install openssl, then re-run." >&2
  exit 1
fi

echo "generating self-signed cert (EXTERNAL_IP=${EXTERNAL_IP}) → ${TLS_DIR}/"
mkdir -p "${TLS_DIR}"
chmod 700 "${TLS_DIR}"

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