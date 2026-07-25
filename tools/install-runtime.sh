#!/usr/bin/env bash
# install-runtime.sh — install (or remove) a runtime that an agent's
# install.sh expects to find on the target machine.
#
# Why this exists:
#   Per design, the agent tarball does NOT bundle the runtime itself
#   (Node.js, Python, uv). The target machine is expected to provide
#   those. This tool performs that provisioning for the supported
#   platform (Ubuntu 24.04 / "noble").
#
# Usage:
#   tools/install-runtime.sh install --from-manifest <path/to/manifest.json>
#   tools/install-runtime.sh install <name> <version>
#   tools/install-runtime.sh list
#   tools/install-runtime.sh remove <name> <version>
#   tools/install-runtime.sh verify <name> <version>
#
# Examples:
#   # Auto-derive from an agent manifest (uses .runtime_requirements[])
#   tools/install-runtime.sh install --from-manifest agents/openclaw/upstream/2026.7.1-2/manifest.json
#
#   # Explicit
#   tools/install-runtime.sh install node 22.22.3
#   tools/install-runtime.sh install python 3.12.13
#   tools/install-runtime.sh install uv 0.11.31
#
#   # Inventory / inspection / removal
#   tools/install-runtime.sh list
#   tools/install-runtime.sh verify node 22.22.3
#   tools/install-runtime.sh remove node 22.22.3
#
# Supported runtimes:
#   node    — NodeSource apt repo (deb.nodesource.com/setup_<major>.x)
#   python  — apt (Ubuntu 24.04 ships python3.12 in main)
#   uv      — official astral.sh installer (apt uv is too old)
#
# State:
#   /var/lib/agent-marketplace/runtime/<name>-<version>.json
# Bin (symlinks for direct PATH access):
#   /usr/local/bin/<name>
#
# Requires: root, Ubuntu 24.04.

set -euo pipefail

readonly STATE_DIR="/var/lib/agent-marketplace/runtime"
readonly BIN_DIR="/usr/local/bin"
readonly SUPPORTED_NAMES=(node python uv)

# --- preflight ---------------------------------------------------------------
preflight() {
  if [[ "$(id -u)" -ne 0 ]]; then
    err "this tool must run as root (it touches /usr/local/bin and apt)"
  fi
  if [[ ! -f /etc/os-release ]]; then
    err "/etc/os-release missing — cannot detect distro"
  fi
  local id_like
  id_like=$(. /etc/os-release && echo "${ID:-}_${VERSION_ID:-}")
  if [[ "${id_like}" != ubuntu_24.* && "${id_like}" != noble_* ]]; then
    err "this tool targets Ubuntu 24.04 (detected: ${id_like}). Edit the per-runtime install_* functions to support another distro."
  fi
}

log()  { echo "[install-runtime] $*" >&2; }
ok()   { echo "[install-runtime] OK: $*" >&2; }
warn() { echo "[install-runtime] WARN: $*" >&2; }
err()  { echo "[install-runtime] ERR: $*" >&2; exit 1; }
emit() { printf '%s=%s\n' "$1" "$2"; }

state_path() {
  echo "$STATE_DIR/$1-$2.json"
}

# Read a state file into stdout (empty {} if missing).
state_get() {
  local p; p="$(state_path "$1" "$2")"
  [[ -f "$p" ]] || { echo "{}"; return; }
  jq '.' "$p"
}

# Write a JSON state file (overwrites).
state_set() {
  local name="$1" version="$2" body="$3"
  mkdir -p "$STATE_DIR"
  local p; p="$(state_path "$name" "$version")"
  echo "$body" | jq '.' > "$p"
  chmod 0644 "$p"
}

# === Subcommand dispatch =====================================================
cmd="${1:-}"
[[ $# -gt 0 ]] && shift

usage() {
  cat >&2 <<EOF
install-runtime.sh — install/remove a runtime for an agent tarball (Ubuntu 24.04)

USAGE
  install-runtime.sh install --from-manifest <path/to/manifest.json>
  install-runtime.sh install <name> <version>
  install-runtime.sh list
  install-runtime.sh verify <name> <version>
  install-runtime.sh remove <name> <version>

SUPPORTED RUNTIMES
  node    — NodeSource apt repo
  python  — apt (python3.12)
  uv      — official astral.sh installer

STATE
  /var/lib/agent-marketplace/runtime/<name>-<version>.json
EOF
  exit 0
}


# === install =================================================================
cmd_install() {
  preflight
  local name version
  if [[ "${1:-}" == "--from-manifest" ]]; then
    local mf="${2:?--from-manifest requires a path}"
    [[ -f "$mf" ]] || err "manifest not found: $mf"
    log "reading runtime_requirements from $mf"
    # Install each requirement in order. If any one fails, abort.
    local -a reqs
    mapfile -t reqs < <(jq -c '.runtime_requirements[]' "$mf")
    if [[ ${#reqs[@]} -eq 0 ]]; then
      log "no runtime_requirements in manifest — nothing to install"
      exit 0
    fi
    for r in "${reqs[@]}"; do
      name=$(echo "$r" | jq -r '.name')
      # Extract the lowest-allowed numeric version from the spec.
      # For ">=22.22.3 <23, >=24.15.0 <25, or >=25.9.0" we pick the first
      # numeric >= comparison; for ">=3.12,<3.13" we pick "3.12".
      version=$(echo "$r" | jq -r '
        .version
        | capture(">=(?<v>[0-9]+\\.[0-9]+(\\.[0-9]+)?)")
          .v // empty
      ')
      [[ -n "$version" ]] || err "could not parse a concrete version from spec: $(echo "$r" | jq -r '.version')"
      install_one "$name" "$version"
    done
  else
    [[ $# -ge 2 ]] || { usage; exit 1; }
    name="$1"; version="$2"
    install_one "$name" "$version"
  fi
}

install_one() {
  local name="$1" version="$2"
  case "$name" in
    node)   install_node   "$version" ;;
    python) install_python "$version" ;;
    uv)     install_uv     "$version" ;;
    *) err "unsupported runtime: $name (supported: ${SUPPORTED_NAMES[*]})" ;;
  esac
}

# --- node -------------------------------------------------------------------
# NodeSource ships official apt repos per major version:
#   https://deb.nodesource.com/setup_<major>.x
install_node() {
  local version="$1"
  local major; major="$(echo "$version" | cut -d. -f1)"

  # Already installed at the right major?
  if command -v node >/dev/null 2>&1; then
    local cur; cur="$(node --version | sed 's/^v//')"
    if [[ "$(echo "$cur" | cut -d. -f1)" == "$major" ]]; then
      ok "node already installed: v$cur (matches major $major)"
      state_set node "$version" "$(jq -n --arg v "$version" --arg cv "$cur" --arg m "$major" --arg method "apt-nodesource" --arg bin "$(command -v node)" '{name:"node",version:$v,installed_version:$cv,major:$m,install_method:$method,binary:$bin,installed_at:now|todate}')"
      ln -sf "$(command -v node)" "$BIN_DIR/node"
      ok "linked: $BIN_DIR/node"
      return 0
    fi
    warn "node v$cur present but wrong major; will reinstall"
  fi

  log "installing node $version via NodeSource apt repo"
  apt-get update -qq
  apt-get install -y -qq ca-certificates curl gnupg
  curl -fsSL "https://deb.nodesource.com/setup_${major}.x" | bash - 2>&1 | tail -5
  apt-get install -y -qq "nodejs"
  local installed; installed="$(node --version | sed 's/^v//')"
  ok "node v$installed installed"

  state_set node "$version" "$(jq -n --arg v "$version" --arg iv "$installed" --arg m "$major" --arg method "apt-nodesource" --arg bin "$(command -v node)" '{name:"node",version:$v,installed_version:$iv,major:$m,install_method:$method,binary:$bin,installed_at:now|todate}')"
  ln -sf "$(command -v node)" "$BIN_DIR/node"
  ok "linked: $BIN_DIR/node"
}

# --- python -----------------------------------------------------------------
install_python() {
  local version="$1"
  local major_minor; major_minor="$(echo "$version" | cut -d. -f1-2)"   # e.g. 3.12

  if command -v "python${major_minor}" >/dev/null 2>&1; then
    ok "python${major_minor} already installed"
    state_set python "$version" "$(jq -n --arg v "$version" --arg mm "$major_minor" --arg method "apt" --arg bin "$(command -v python${major_minor})" '{name:"python",version:$v,installed_version:$mm,install_method:$method,binary:$bin,installed_at:now|todate}')"
    ln -sf "$(command -v python${major_minor})" "$BIN_DIR/python${major_minor}"
    ok "linked: $BIN_DIR/python${major_minor}"
    return 0
  fi

  log "installing python${major_minor} via apt"
  apt-get update -qq
  # Ubuntu 24.04 ships python3.12 in main; for other minors we'd need a PPA.
  apt-get install -y -qq "python${major_minor}" "python${major_minor}-venv" "python${major_minor}-dev"
  ok "python${major_minor} installed"

  state_set python "$version" "$(jq -n --arg v "$version" --arg mm "$major_minor" --arg method "apt" --arg bin "$(command -v python${major_minor})" '{name:"python",version:$v,installed_version:$mm,install_method:$method,binary:$bin,installed_at:now|todate}')"
  ln -sf "$(command -v python${major_minor})" "$BIN_DIR/python${major_minor}"
  ok "linked: $BIN_DIR/python${major_minor}"
}

# --- uv ---------------------------------------------------------------------
install_uv() {
  local version="$1"

  # uv is single-file — install by downloading the pinned binary.
  local url="https://github.com/astral-sh/uv/releases/download/${version}/uv-x86_64-unknown-linux-gnu.tar.gz"
  local tmp; tmp="$(mktemp -d)"
  log "downloading $url"
  curl -fsSL --connect-timeout 15 -m 300 -o "$tmp/uv.tar.gz" "$url" \
    || err "uv $version download failed — pin may not exist; see https://github.com/astral-sh/uv/releases"
  tar -xzf "$tmp/uv.tar.gz" -C "$tmp"
  cp "$tmp/uv-x86_64-unknown-linux-gnu/uv" /usr/local/bin/uv
  cp "$tmp/uv-x86_64-unknown-linux-gnu/uvx" /usr/local/bin/uvx
  chmod 0755 /usr/local/bin/uv /usr/local/bin/uvx
  rm -rf "$tmp"
  ok "uv $(uv --version | awk '{print $2}') installed at /usr/local/bin/uv"

  state_set uv "$version" "$(jq -n --arg v "$version" --arg iv "$(uv --version | awk '{print $2}')" --arg method "binary-tarball" --arg bin /usr/local/bin/uv '{name:"uv",version:$v,installed_version:$iv,install_method:$method,binary:$bin,installed_at:now|todate}')"
  ok "linked: /usr/local/bin/uv + /usr/local/bin/uvx"
}

# === list ====================================================================
cmd_list() {
  mkdir -p "$STATE_DIR"
  echo "Installed runtimes:"
  local found=0
  for f in "$STATE_DIR"/*.json; do
    [[ -f "$f" ]] || continue
    found=1
    jq -r '"  \(.name) \(.version) [\(.install_method)] -> \(.binary)"' "$f"
  done
  [[ $found -eq 0 ]] && echo "  (none)"
}

# === verify ==================================================================
cmd_verify() {
  [[ $# -ge 2 ]] || { usage; exit 1; }
  local name="$1" version="$2"
  local state; state="$(state_get "$name" "$version")"
  local verify_cmd
  verify_cmd=$(jq -r '.verify_cmd // ""' <<<"$state")
  if [[ -z "$verify_cmd" ]]; then
    case "$name" in
      node)   verify_cmd="node --version" ;;
      python) verify_cmd="python${version%.*} --version" ;;
      uv)     verify_cmd="uv --version" ;;
    esac
  fi
  log "$verify_cmd"
  if eval "$verify_cmd" >/dev/null 2>&1; then
    ok "OK"
  else
    err "verify failed: $verify_cmd"
  fi
}

# === remove ==================================================================
cmd_remove() {
  [[ $# -ge 2 ]] || { usage; exit 1; }
  local name="$1" version="$2"
  local state; state="$(state_get "$name" "$version")"
  local method; method=$(jq -r '.install_method // ""' <<<"$state")
  local bin; bin=$(jq -r '.binary // ""' <<<"$state")

  case "$name:$method" in
    node:apt-nodesource)
      log "removing NodeSource repo + nodejs"
      rm -f /etc/apt/sources.list.d/nodesource.list
      apt-get remove -y -qq nodejs || true
      apt-get autoremove -y -qq || true
      ;;
    python:apt)
      log "apt remove python${version%.*}"
      apt-get remove -y -qq "python${version%.*}" "python${version%.*}-venv" "python${version%.*}-dev" || true
      apt-get autoremove -y -qq || true
      ;;
    uv:binary-tarball)
      log "removing /usr/local/bin/{uv,uvx}"
      rm -f /usr/local/bin/uv /usr/local/bin/uvx
      ;;
    *)
      err "don't know how to remove $name (method=$method)"
      ;;
  esac

  # Remove state file + best-effort symlink cleanup.
  rm -f "$(state_path "$name" "$version")"
  [[ -n "$bin" && -L "$BIN_DIR/$(basename "$bin")" ]] && rm -f "$BIN_DIR/$(basename "$bin")"
  ok "removed $name $version"
}

case "$cmd" in
  install) cmd_install "$@" ;;
  list)    cmd_list ;;
  remove)  cmd_remove "$@" ;;
  verify)  cmd_verify "$@" ;;
  -h|--help|help|"") usage ;;
  *) err "unknown subcommand: $cmd (try 'install-runtime.sh help')" ;;
esac
