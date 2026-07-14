# Agent Marketplace Packages

Offline-installable agent bundles for `opencode`, `openclaw`, and `hermes-agent`.

Each (agent, version) ships as a **self-contained tarball** with:
- A pre-fetched upstream release artifact (npm tarball, pip wheels, or static binary)
- Bundled runtime (Node.js, Python, or nothing — depends on the agent)
- An idempotent `install.sh` that drops everything into `$HOME/.local` (no root required)
- A SHA256 chain that detects tampering of any byte inside the tarball

This repository **is** the source of truth. To publish, run `tools/pack.sh`; consumers fetch the resulting `dist/<name>-<source>-<version>.tar.gz` over HTTPS and verify its `.sha256`.

---

## Quick start (consumer side)

```bash
# 1. Make sure the system prerequisites are present (see docs/prerequisites.md)
sudo apt-get install -y tar coreutils bash ca-certificates

# 2. Download a tarball + its checksum
curl -fSLO https://your-cdn.example/agents/opencode-upstream-0.0.55.tar.gz
curl -fSLO https://your-cdn.example/agents/opencode-upstream-0.0.55.tar.gz.sha256
sha256sum -c opencode-upstream-0.0.55.tar.gz.sha256

# 3. Extract and install (no root, no network)
tar -xzf opencode-upstream-0.0.55.tar.gz
cd opencode-upstream-0.0.55
./install.sh            # installs to $HOME/.local by default

# 4. Verify it actually runs
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
      install.sh                 Self-contained install (validates tarball → deploys → verifies --version)
      uninstall.sh               Removes files recorded in state.json
      migrate/                   Version-to-version migration scripts
      runtime/                   Bundled runtime (Node, Python, uv, …) — per-version, copied into the tarball
      payload/                   Bundled upstream artifact (npm tgz, pip wheels, static binary)
      files/                     Auxiliary config we ship on top
      README.md                  Notes specific to this version

tools/                            Maintenance tooling (NEVER enters a tarball)
  fetch.sh                        Pull upstream artifacts into agents/<…>/payload/
  pack.sh                         Build a tarball from a version directory
  verify.sh                       Validate manifest + checksums + tarball consistency
  sign.sh                         Optional GPG detached signature
  publish.sh                      Upload tarball + sha256 + sig to a remote
  index-gen.sh                    Rebuild dist/index.json from existing tarballs

runtime-pool/                     Local cache of pre-downloaded runtimes (Node, Python, uv)
                                  Reused across versions to keep the repo small

docs/
  prerequisites.md                System packages required on the target machine
  install-protocol.md             install.sh contract (env vars, exit codes, stdout)
  manifest-schema.md              Every manifest.json field explained
  upgrade-protocol.md             How version-to-version upgrades and migrations work
  publishing-model.md             Trust levels, signing, CDN layout
```

---

## Concepts in one paragraph

Every version directory is the **exact** root of its tarball. The tarball contains everything install.sh needs (upstream artifact, runtime, install script, manifest) so the script can run on a completely offline machine with zero pre-installed tooling. The install script is **idempotent** — running it twice with the same tarball is a no-op. State is persisted in `$HOME/.local/state/<agent>.state.json` so future upgrades know what's installed. The manifest records SHA256 of every file in the tarball; `tools/verify.sh` re-checks them on demand.

---

## Currently packaged

| Agent        | Source   | Version    | Runtime           | Tarball size (est.) |
|--------------|----------|------------|-------------------|---------------------|
| opencode     | upstream | 0.0.55     | (none — Go static) | ~14 MB              |
| openclaw     | upstream | 2026.7.2   | Node 22.22.3      | ~60 MB              |
| hermes-agent | upstream | 0.18.2     | Python 3.12 + uv  | ~180 MB             |

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