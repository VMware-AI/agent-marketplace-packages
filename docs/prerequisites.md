# System prerequisites — what must be on the target machine

install.sh assumes certain things are already installed. It will **not** install them for you (auto-`apt install` is dangerous on bare-metal / containers / production). If something is missing, install.sh refuses with exit code 40 and prints the fix.

## Universal prerequisites (all three agents)

These are needed by `install.sh` itself plus the bundled runtime:

```bash
# Ubuntu 22.04 LTS / 24.04 LTS, Debian 12
sudo apt-get update
sudo apt-get install -y \
    bash \
    coreutils \
    findutils \
    grep \
    sed \
    gawk \
    tar \
    gzip \
    ca-certificates \
    curl \
    jq
```

| Tool | Why | Check |
|------|-----|-------|
| `bash ≥ 4.0` | install.sh uses `[[ ]]` and arrays | `bash --version` |
| `coreutils` | `cp / mv / mkdir / chmod / chown` | `cat /dev/null && cp --help` |
| `findutils` | `find / xargs` | `find --version` |
| `grep`, `sed`, `gawk` | manifest + state parsing | `grep --version` |
| `tar` (GNU ≥ 1.30) | unpack the tarball | `tar --version` |
| `gzip` | the tarball is `.tar.gz` | `gzip --version` |
| `sha256sum` | verify the tarball + every file inside | `sha256sum --version` |
| `ca-certificates` | TLS verification (Node, Python, npm, uv) | `dpkg -l ca-certificates` (Debian) |
| `curl` (or `wget`) | downloading upstream npm/pip deps at install time | `curl --version` |
| `jq` | parsing `manifest.json` from shell | `jq --version` |
| `glibc ≥ 2.31` | the bundled Node/Python/uv need a modern libc | `ldd --version \| head -1` (should show 2.31+) |

## Per-agent extras

### opencode (Go static binary — no extra system deps)

None. The `opencode` binary in `payload/bin/opencode` is statically linked and brings its own C library. Just the universal prerequisites above.

### openclaw (Node 22 npm package)

Already covered by universal prerequisites. Nothing extra on top.

### hermes-agent (Python 3.12 + uv)

Already covered by universal prerequisites plus:

```bash
sudo apt-get install -y \
    libssl3 \          # Python ssl / Node TLS
    zlib1g \           # Python zlib / Node zlib
    libffi8 \          # Python ctypes
    libsqlite3-0       # hermes-agent's default SQLite persistence
```

(On Ubuntu 24.04, the names are `libssl3t64`, but `libssl3` still works.)

For multimedia tools (used by hermes-agent's optional skills):

```bash
sudo apt-get install -y ripgrep ffmpeg
```

> Note: `ripgrep` and `ffmpeg` are **not** checked by install.sh — they're optional runtime tools that hermes-agent calls via subprocess.

## Full one-liner per agent

```bash
# opencode
sudo apt-get install -y bash coreutils findutils grep sed gawk tar gzip ca-certificates curl jq

# openclaw  (same as opencode + nodejs CA certs; bundled Node 22.22.3 is statically OK)
sudo apt-get install -y bash coreutils findutils grep sed gawk tar gzip ca-certificates curl jq

# hermes-agent  (adds Python runtime deps + optional tools)
sudo apt-get install -y bash coreutils findutils grep sed gawk tar gzip ca-certificates curl jq \
    libssl3 zlib1g libffi8 libsqlite3-0 ripgrep ffmpeg
```

## OS support matrix

| Distro | Status | Notes |
|--------|--------|-------|
| **Ubuntu 22.04 LTS** | ✅ recommended baseline | glibc 2.35 |
| **Ubuntu 24.04 LTS** | ✅ | glibc 2.39; libssl3 → libssl3t64 |
| **Debian 12 (bookworm)** | ✅ | glibc 2.36 |
| **Rocky Linux 9 / RHEL 9** | ✅ | `dnf install` instead of `apt-get`; package names: `glibc openssl-libs zlib libffi sqlite-libs` |
| **Fedora 40+** | ✅ | same as RHEL 9 |
| **Alpine 3.x** | ❌ not supported | musl libc, our binaries are glibc |
| **macOS** | ❌ out of scope (Linux x86_64 only for now) | |
| **Windows** | ❌ out of scope | |
| **Linux ARM64** | ❌ not packaged yet | would need python-build-standalone `aarch64-unknown-linux-gnu` |

## If you're on a minimal container

```dockerfile
FROM ubuntu:24.04
RUN apt-get update && apt-get install -y --no-install-recommends \
        bash coreutils findutils grep sed gawk tar gzip ca-certificates curl jq \
        libssl3t64 zlib1g libffi8 libsqlite3-0 \
    && rm -rf /var/lib/apt/lists/*
```

Then copy your tarball in and run install.sh as a non-root user.