# Agent Marketplace Packages

Offline-installable agent bundles for `opencode`, `openclaw`, and `hermes-agent`.

Each (agent, version) ships as a **self-contained tarball** with:
- A pre-fetched upstream release artifact (npm tarball, pip wheels, or static binary)
- An idempotent `install.sh` that drops everything into `$HOME/.local` (no root required)
- A SHA256 chain that detects tampering of any byte inside the tarball

Runtimes (Node.js, Python, uv) are **NOT bundled** in the tarball — the target machine is expected to have them pre-installed at the version declared in `manifest.runtime_requirements`. Use `tools/install-runtime.sh` on Ubuntu 24.04 to provision them.

This repository **is** the source of truth. To publish, run `tools/pack.sh`; consumers fetch the resulting `dist/<name>-<source>-<version>.tar.gz` over HTTPS and verify its `.sha256`.

---

## Quick start (consumer side)

```bash
# 1. (One-time per target machine) provision the runtimes an agent needs.
#    Run as root on Ubuntu 24.04. The agent's install.sh will fail fast
#    with a clear hint if the runtime version is missing or wrong.
sudo tools/install-runtime.sh install --from-manifest agents/<agent>/upstream/<v>/manifest.json
#    Or pin explicitly:
sudo tools/install-runtime.sh install node   22.22.3
sudo tools/install-runtime.sh install python 3.12.13
sudo tools/install-runtime.sh install uv     0.11.31

# 2. Make sure the system prerequisites are present (see docs/prerequisites.md)
sudo apt-get install -y tar coreutils bash ca-certificates jq

# 3. Download a tarball + its checksum
curl -fSLO https://your-cdn.example/agents/opencode-upstream-0.0.55.tar.gz
curl -fSLO https://your-cdn.example/agents/opencode-upstream-0.0.55.tar.gz.sha256
sha256sum -c opencode-upstream-0.0.55.tar.gz.sha256

# 4. Extract and install (no root, no network)
tar -xzf opencode-upstream-0.0.55.tar.gz
cd opencode-upstream-0.0.55
./install.sh            # installs to $HOME/.local by default

# 5. Verify it actually runs
$HOME/.local/bin/opencode --version
```

The install script writes `$HOME/.local/state/<agent>.state.json` recording what was installed, where, and from which tarball.

---

## Repository layout

```
agents/
  <agent>/
    upstream/<version>/          Versions that mirror upstream releases 1:1
    ours/<version>/              Our forks / patches / internal builds
      manifest.json              Single source of truth (see docs/manifest-schema.md)
                                  Includes `runtime_requirements[]` declaring which
                                  system-installed runtimes (Node/Python/uv) install.sh needs.
      install.sh                 Self-contained install (validates tarball → verifies runtime
                                  → deploys vendored payload → verifies --version)
      uninstall.sh               Removes files recorded in state.json
      migrate/                   Version-to-version migration scripts
      payload/                   Vendored offline-install deps:
                                    - npm tree (openclaw): pre-resolved by `npm install --global`
                                    - Python wheels (hermes-agent): downloaded by `pip download`
                                    - static binary (opencode)
      files/                     Auxiliary config we ship on top
      README.md                  Notes specific to this version

tools/                            Maintenance tooling (NEVER enters a tarball)
  fetch.sh                        Pull upstream artifacts into agents/<…>/payload/
  pack.sh                         Build a tarball from a version directory
  verify.sh                       Validate manifest + checksums + tarball consistency
  install-runtime.sh               Provision Node/Python/uv on the target (Ubuntu 24.04)
  sign.sh                         Optional GPG detached signature
  publish.sh                      Upload tarball + sha256 + sig to a remote
  index-gen.sh                    Rebuild dist/index.json from existing tarballs

docs/
  prerequisites.md                System packages required on the target machine
  install-protocol.md             install.sh contract (env vars, exit codes, stdout)
  manifest-schema.md              Every manifest.json field explained
  upgrade-protocol.md             How version-to-version upgrades and migrations work
  publishing-model.md             Trust levels, signing, CDN layout
```

---

## Concepts in one paragraph

Every version directory is the **exact** root of its tarball. The tarball contains the install script, the embedded `manifest.json`, the upstream artifact (npm tree, Python wheels, or static binary), and the install.sh's helper files. Runtimes (Node, Python, uv) are **NOT** in the tarball — they live on the target machine, installed via `tools/install-runtime.sh` from the version declarations in `manifest.runtime_requirements`. The install script is **idempotent** — running it twice with the same tarball is a no-op. State is persisted in `$HOME/.local/state/<agent>.state.json` so future upgrades know what's installed. The manifest records the SHA256 of the tarball sidecar (`<name>.tar.gz.sha256`) plus per-file checksums; `tools/verify.sh` re-checks them on demand.

---

## Currently packaged

| Agent        | Source   | Version    | Runtime required (target-supplied) | Tarball size (est.) |
|--------------|----------|------------|-------------------------------------|---------------------|
| opencode     | upstream | 0.0.55     | (none — Go static)                  | ~14 MB              |
| openclaw     | upstream | 2026.7.1-2 | Node.js ≥22.22.3 / ≥24.15.0 / ≥25.9 | ~62 MB              |
| hermes-agent | upstream | 0.18.2     | Python 3.12 + uv ≥0.11              | ~47 MB              |

Runtimes are NOT bundled — install them on the target machine with `tools/install-runtime.sh install --from-manifest agents/<name>/upstream/<v>/manifest.json` (Ubuntu 24.04 only).

See `agents/<name>/upstream/<version>/README.md` for version-specific notes.

---

## Maintainer workflow

```bash
# Add a new version of an existing agent
tools/fetch.sh openclaw upstream 2026.7.2   # pulls upstream npm tgz + Node tarball
tools/runtime-fetch.sh node 22.22.3 linux-x64   # cached in runtime-pool/
# ... copy runtime into the version directory, write manifest.json + install.sh ...
tools/verify.sh openclaw upstream 2026.7.2  # cross-check checksums
tools/pack.sh openclaw upstream 2026.7.2    # → dist/openclaw-upstream-2026.7.2.tar.gz
```

See `CONTRIBUTING.md` for the full SOP.

---

## Limitations (be aware)

- **No live testing** is performed on the install scripts in this repo. They are written to be correct by construction but have not been executed end-to-end against a real Linux container as of the initial commit. Validate in your environment before deploying.
- **Linux x86_64 only** for now. The structure supports adding macOS / arm64 by extending `manifest.requires` and adding per-platform runtime directories, but those are not implemented.
- **Trusted-path publishing**. SHA256 alone does not protect against a malicious CDN mirror — it only catches accidental corruption. For production, also enable `tools/sign.sh` (GPG detached signatures) so consumers can verify provenance.