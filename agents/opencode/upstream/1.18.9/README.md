# opencode 1.18.9 (upstream)

## About

This is a 1:1 mirror of the upstream [`opencode-ai/opencode`](https://github.com/opencode-ai/opencode) `v1.18.9` release, re-packaged as an offline-installable tarball.

`opencode` is described upstream as "a powerful AI coding agent, built for the terminal." It's a single statically-linked Go binary — no Node, Python, or system libraries required at install time beyond the basics listed in `../..`/../docs/prerequisites.md.

## How it was packaged

1. Downloaded `opencode-linux-x86_64.tar.gz` from the upstream release
2. Verified its SHA256 against the GitHub release manifest
3. Extracted the inner `opencode` binary into `payload/bin/opencode`
4. Discarded the outer tarball (no payload data loss — only LICENSE/README wrapper)
5. Recorded all file SHA256s in `manifest.checksums`

## Install

```bash
cd agents/opencode/upstream/1.18.9
./install.sh
```

See the contract in [`../../../docs/install-protocol.md`](../../../docs/install-protocol.md).

## Verify after install

```bash
$HOME/.local/opencode/1.18.9/bin/opencode --version
```

## What `state.json` records

After install, see `$HOME/.local/state/opencode.state.json` for:
- The exact deploy directory
- The list of files it owns (used by `uninstall.sh` and `migrate/from-0.0.x.sh`)
- The upstream tarball's recorded SHA256 (for audit)

## Upgrade notes

- Strategy: `replace` — the prior install is fully overwritten
- Migrating from any prior `0.0.x` or `1.0.x` … `1.17.x` invokes the matching migration script under `migrate/` (currently a no-op — no state to transform between patch releases)
- Cross-major upgrades will need a new migration script under `migrate/`

## Known quirks

- The binary is ~43 MB (still has Go runtime symbols — debug build). Upstream has not published a `--strip` artifact yet.
- No `install-cli.sh`-style wrapper exists — `opencode` is meant to be invoked directly.
- `--version` may print verbose multi-line output on some 0.0.x builds. We capture all of it into `INSTALLED_VERSION`.