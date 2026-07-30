# install.sh — protocol contract

Every `install.sh` in `agents/<agent>/<source>/<version>/` must follow this contract.

## Inputs

### Environment variables (all optional)

| Variable | Default | Meaning |
|----------|---------|---------|
| `AGENT_MARKETPLACE_TARGET_ROOT` | `$HOME/.local` | Root directory install.sh writes into |
| `AGENT_MARKETPLACE_PREVIOUS_VERSION` | empty | Set by install.sh itself when upgrading; lets migration scripts know the source version |
| `AGENT_MARKETPLACE_TARGET_VERSION` | empty | Set by agentpkg rollback when running `migrate/to-<from>.sh`; the version being rolled back TO |
| `AGENT_MARKETPLACE_PYPI_MIRROR` | `https://mirrors.aliyun.com/pypi/simple/` | (hermes-agent only) PyPI mirror for resolving transitive deps |

### Filesystem expectations

- `manifest.json` in the same directory as install.sh
- `payload/`, `runtime/`, `files/`, `migrate/` subdirectories as listed in manifest
- `runtime/python/` etc. may or may not be present, depending on agent

### Permission expectation

- **MUST NOT be root**. install.sh refuses if `id -u == 0` (exit 20).

## Outputs

### Exit codes

| Code | Meaning |
|------|---------|
| 0  | Successful install |
| 10 | Same version already installed (idempotent) |
| 20 | Platform / version / architecture incompatibility |
| 30 | Manifest / tarball structure invalid |
| 40 | System tool / package missing |
| 50 | Checksum mismatch (tarball tampered / corrupted) |
| 60 | Post-install verification failed (e.g. binary can't run) |

### stdout

install.sh emits `KEY=VALUE` lines for downstream consumption. They are emitted in this order on success:

```
STATE_PATH=/home/user/.local/state/opencode.state.json
INSTALLED_FILES=...
INSTALLED_VERSION=opencode 0.0.55
DEPLOY_ROOT=/home/user/.local/opencode/0.0.55
```

(`INSTALLED_VERSION` is the actual output of `<agent> --version`, captured verbatim.)

### stderr

install.sh logs human-readable progress to stderr (`[opencode/0.0.55] OK: ...`). Never write normal output to stdout — the `KEY=VALUE` contract depends on stdout being machine-parseable.

### Side effects

1. Creates `$TARGET_ROOT/<agent>/<version>/` and populates it
2. Creates `$TARGET_ROOT/runtime/<name>/` for bundled runtimes
3. Creates `$TARGET_ROOT/bin/<agent>` symlink
4. Writes `$TARGET_ROOT/state/<agent>.state.json` (or updates if upgrading)
5. **Does NOT** call `apt`, `sudo`, or any package manager — system prerequisites must already be present

## state.json schema

```json
{
  "agent": "opencode",
  "source": "upstream",
  "version": "0.0.55",
  "channel": "stable",
  "deploy_root": "/home/user/.local/opencode/0.0.55",
  "target_root": "/home/user/.local",
  "installed_version": "opencode 0.0.55",
  "installed_files": ["bin/opencode", "..."],
  "manifest_sha256": "sha256:fc78...",
  "installed_at": "2026-07-14T10:30:00Z",
  "runtime": { "node": "22.22.3" },
  "previous": { "version": "0.0.54", "source": "upstream" },
  "services": [
    { "name": "web", "unit_path": "/home/user/.config/systemd/user/opencode-web.service", "started": true }
  ],
  "configs": [
    { "name": "opencode", "render_to": "/home/user/.config/opencode/opencode.json", "mode": "0600" }
  ],
  "config_dir": "/home/user/.local/opencode/0.0.55/config"
}
```

The `installed_files` list uses paths **relative to `deploy_root`** (with `../bin/<name>` for the symlink that lives outside deploy_root). uninstall.sh uses this to know exactly what to remove.

The `services` / `configs` / `config_dir` fields are appended by `agentpkg install` after `install.sh` returns (schema 1.1+). Old state.json files without them still work — agentpkg defaults to empty arrays. uninstall.sh is unaffected (it only consumes `installed_files`); agentpkg's uninstall path stops + removes each systemd unit before running uninstall.sh.

> 注：`runtime` 字段是早期实验；当前 install.sh 把 runtime 版本直接写到 `manifest.runtime_constraints` 里并用其检测 `tools/install-runtime.sh`。state.json 里的 `runtime` 已不再被 agentpkg 读取。

## render-config.sh contract

Each agent ships `render-config.sh` next to `install.sh` in the tarball. `agentpkg config generate <agent> --config-input <file>` invokes it after the daemon writes a JSON config dict to `<file>`.

**Inputs (env vars set by agentpkg before exec):**

| Env var | Meaning |
|---------|---------|
| `AGENT_MARKETPLACE_CONFIG_INPUT` | absolute path to daemon's config input JSON file |
| `AGENT_MARKETPLACE_AGENT` | agent name (e.g. `openclaw`) |
| `AGENT_MARKETPLACE_VERSION` | the version being installed (e.g. `2026.7.1-2`) |
| `AGENT_MARKETPLACE_DEPLOY_ROOT` | absolute deploy_root (informational) |

**Outputs:** the script writes config file(s) to the upstream-canonical paths declared in `manifest.configs[].render_to`. The script owns the chmod (typically `0600` for sensitive config).

**Exit codes:**
- `0` — success
- `1` — configuration error (input file missing, required key absent). agentpkg surfaces as exit 70.
- `2` — script error. agentpkg surfaces as exit 71.

## agentpkg install flow

After `install.sh` succeeds, agentpkg performs the following on the host (manifest schema 1.1+):

1. Read manifest from cached tarball.
2. For each `manifest.services[]` entry, write `~/.config/systemd/user/<agent>-<name>.service`, run `systemctl --user daemon-reload` once, then `systemctl --user enable --now <unit>`.
3. Append `services` / `configs` / `config_dir` to state.json.

If `systemctl --user` is unavailable (CI containers without `loginctl enable-linger`), the systemd step soft-fails with a warning. The install itself is not aborted.

`agentpkg uninstall` reverses the systemd step (disable + remove unit file) before running uninstall.sh.

## Exit codes (extended)

| Code | Meaning |
|------|---------|
| 0  | Successful install |
| 10 | Same version already installed (idempotent) |
| 20 | Platform / version / architecture incompatibility |
| 30 | Manifest / tarball structure invalid |
| 40 | System tool / package missing |
| 50 | Checksum mismatch (tarball tampered / corrupted) |
| 60 | Post-install verification failed |
| 70 | Required configuration missing (render-config.sh: missing required key) |
| 71 | Render script error (render-config.sh: script syntax/logic) |
| 72 | systemd --user not available (soft failure: install proceeds, no service started) |

## Idempotency

Calling install.sh twice with the same version is a no-op on the second call (exit 10). State is not re-written.

Calling install.sh with a newer version detects the prior state, runs the matching migration script, then deploys the new version. Calling install.sh with an older version... does the same (just installs the older one — install.sh is version-agnostic, but the migration script check enforces upgrades go forward).

## Migration

When a prior install is detected, install.sh runs a migration script from `migrate/`. The matching priority is:

1. Exact match: `migrate/from-<installed-version>.sh`
2. Major-minor wildcard: `migrate/from-<major>.<minor>.x.sh`
3. Major wildcard: `migrate/from-<major>.x.x.sh`
4. No match: continue without migration (assumed compatible)

The migration script gets these env vars: `AGENT_MARKETPLACE_TARGET_ROOT`, `AGENT_MARKETPLACE_PREVIOUS_VERSION`. It must be idempotent and safe to re-run.