# opencode 1.19.0 (upstream)

## About

This is a 1:1 mirror of the upstream [`opencode-ai/opencode`](https://github.com/anomalyco/opencode) `v1.19.0` release, re-packaged as an offline-installable tarball.

`opencode` is described upstream as "a powerful AI coding agent, built for the terminal." It's a single statically-linked Go binary — no Node, Python, or system libraries required at install time beyond the basics listed in `../../../docs/prerequisites.md`.

## How it was packaged

1. Downloaded `opencode-linux-x64.tar.gz` from the upstream release
2. Verified its SHA256 against the GitHub release manifest
3. Extracted the inner `opencode` binary into `payload/bin/opencode`
4. Discarded the outer tarball (no payload data loss — only LICENSE/README wrapper)
5. Recorded all file SHA256s in `manifest.checksums`

## Install

```bash
cd agents/opencode/upstream/1.19.0
./install.sh
```

See the contract in [`../../../docs/install-protocol.md`](../../../docs/install-protocol.md).

## Verify after install

```bash
$HOME/.local/opencode/1.19.0/bin/opencode --version
```

## What `state.json` records

After install, see `$HOME/.local/state/opencode.state.json` for:
- The exact deploy directory
- The list of files it owns (used by `uninstall.sh` and `migrate/`)
- The upstream tarball's recorded SHA256 (for audit)
- The previous version (for `agentpkg rollback`)

## Upgrade notes

- Strategy: `replace` — the prior install is fully overwritten
- Upgrading from `1.18.x` (or earlier `1.18.9`): runs `migrate/from-1.18.9.sh` (exact match) or `migrate/from-1.18.x.sh` (major.minor wildcard). Both are no-ops today — opencode is a single-binary Go app with no state outside `$DEPLOY_ROOT`.
- Upgrading from `0.0.x`: the bundled migration script `migrate/from-0.0.x.sh` is the no-op fallback (binary-only, nothing to transform).
- Cross-major upgrades (e.g. to OpenCode 2) will need a new migration script under `migrate/`.
- Rollback (`agentpkg rollback opencode`) returns to the `1.18.x` deploy; the inverse migration lives at `<target>/migrate/to-1.19.0.sh` in the older tarball (not bundled here — install protocol handles the missing-script case as a no-op WARN).

## 1.19.0-specific notes

- Upstream v1.19.0 stops the TUI from installing updates automatically and adds a dedicated `opencode upgrade --major` flow for moving to OpenCode 2. This is a runtime behavior change in the binary; it does not change how `agentpkg install` / `agentpkg upgrade` work.
- `OPENCODE_AUTOUPDATE` config schema is unchanged; defaults to `notify` upstream, which is what `render-config.sh` already maps.

## Known quirks

- The binary is significantly larger than 1.18.9 (upstream's debug build still includes Go runtime symbols). Upstream has not published a `--strip` artifact yet.
- No `install-cli.sh`-style wrapper exists — `opencode` is meant to be invoked directly.
- `--version` may print verbose multi-line output on some 0.0.x builds. We capture all of it into `INSTALLED_VERSION`.
