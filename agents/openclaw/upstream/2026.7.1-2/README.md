# openclaw 2026.7.2 (upstream)

## About

This is an offline-installable bundle for the upstream `openclaw` Node.js CLI version `2026.7.2`, sourced from the [openclaw/openclaw](https://github.com/openclaw/openclaw) GitHub repository and the npm registry.

`openclaw` is a multi-channel AI gateway — install once, then access your assistant from WhatsApp / Telegram / Slack / Discord / etc. The Linux CLI runs as a single `openclaw` binary on your machine and acts as the control plane for the desktop / mobile companion apps.

## How it was packaged

1. **Bundled runtime**: Downloaded `node-v22.22.3-linux-x64.tar.xz` from `https://nodejs.org/dist/`, verified its SHA256 against the official `SHASUMS256.txt`, and extracted into `runtime/node/`.
2. **Manifest**: Records the upstream npm URL (`https://registry.npmjs.org/openclaw/-/openclaw-2026.7.2.tgz`) but **does not bundle the npm package**. On first install, `install.sh` uses the bundled `npm` to pull `openclaw@2026.7.2` from the npm registry.

> **Why isn't the npm package bundled?** It pulls in hundreds of MB of transitive deps and changes frequently. The Node runtime alone is 200 MB and stable enough to amortize. Consumers need network access on first install; subsequent re-installs are offline.

## System prerequisites

In addition to the [universal prerequisites](../../../docs/prerequisites.md), install:

```bash
sudo apt-get install -y ca-certificates curl
```

`ca-certificates` is required for Node's HTTPS verification; `curl` is the npm client's download backend.

## Install

```bash
cd agents/openclaw/upstream/2026.7.2
./install.sh    # installs to $HOME/.local
```

The install will:
1. Verify the tarball's checksums
2. Detect any prior install and run a migration if needed
3. Deploy the bundled Node 22 to `$HOME/.local/openclaw/runtime/node`
4. Run `npm install openclaw@2026.7.2` into `$HOME/.local/openclaw/2026.7.2/`
5. Symlink `$HOME/.local/bin/openclaw` to the installed CLI
6. Run `openclaw --version` to verify
7. Write `$HOME/.local/state/openclaw.state.json`

## Verify after install

```bash
$HOME/.local/bin/openclaw --version
$HOME/.local/openclaw/runtime/node/bin/node --version    # should print v22.22.3
```

## Known limitations

- **First install requires internet** for the npm registry. The bundled Node is offline-capable; only the `openclaw@2026.7.2` package and its deps are pulled live. If you need fully-offline installs, pre-`npm pack` the package + transitive deps and update `install.sh` to use `npm install --offline`.
- **Single platform**: Linux x86_64 only. macOS / arm64 are not packaged yet.
- **No `--global-prefix` collision avoidance**: if `$HOME/.local/openclaw` already exists from a non-marketplace install of openclaw, behavior is undefined.

## State file

`$HOME/.local/state/openclaw.state.json` records:
- `version`, `source`, `channel`
- `deploy_root` (the install location)
- `runtime.node` (the Node version we shipped)
- `installed_files` (used by `uninstall.sh`)
- `previous` (the version we replaced, for migration audit)

## Upgrade / downgrade

Re-run `./install.sh` from the new version's directory. If a prior version is detected and a migration script exists for it, that migration runs first.

To uninstall this version specifically, run this directory's `uninstall.sh`. To fully remove openclaw, run uninstall.sh from each installed version's directory and then `rm -rf $HOME/.local/openclaw`.