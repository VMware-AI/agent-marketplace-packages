# manifest.json — schema reference

Every version directory must contain `manifest.json`. This file is the single source of truth for everything install.sh and verify.sh need.

## Top-level fields

```json
{
  "schema_version": "1.1",
  "agent":         "opencode",
  "source":        "upstream",
  "version":       "0.0.55",
  "channel":       "stable",
  "released_at":   "2025-06-27",

  "upstream":  { ... },
  "fork_of":   null,

  "requires":  { ... },
  "runtime":   [ ... ],
  "payload":   [ ... ],

  "checksums": { ... },
  "tarball":   { ... },
  "upgrade":   { ... },
  "scripts":   { ... },

  "services":  [ ... ],
  "configs":   [ ... ]
}
```

| Field | Required | Type | Notes |
|-------|----------|------|-------|
| `schema_version` | yes | string | `"1.1"` for current. Bumped on breaking schema changes. (1.0 manifests still work — `services` and `configs` are optional.) |
| `agent` | yes | string | One of `opencode`, `openclaw`, `hermes-agent`, ... |
| `source` | yes | enum | `upstream` (mirror of upstream release) or `ours` (internal fork) |
| `version` | yes | string | The version we're packaging. Must match `upstream.version` for `source=upstream`. |
| `channel` | yes | enum | `stable`, `beta`, `edge`, `internal` |
| `released_at` | yes | ISO-8601 date | When the bundle was packaged. Not necessarily when upstream released. |

## upstream

```json
"upstream": {
  "name":    "opencode",
  "version": "0.0.55",
  "url":     "https://github.com/opencode-ai/opencode/releases/download/v0.0.55/opencode-linux-x86_64.tar.gz",
  "sha256":  "sha256:7f1f41203e7920aec48f3e2216d334e9ce8c6d0a7b107ceb758fefa4d4c98025",
  "notes":   "Outer tarball was unpacked and discarded..."
}
```

| Field | Required | Notes |
|-------|----------|-------|
| `name` | yes | Upstream package name |
| `version` | yes | Upstream version |
| `url` | yes | Canonical download URL |
| `sha256` | yes | Upstream artifact SHA256, in `sha256:<hex>` form |
| `notes` | no | Free-form provenance / packing notes |

## fork_of

```json
"fork_of": null
```

For `source=ours`, set to:

```json
"fork_of": {
  "agent":   "openclaw",
  "version": "2026.7.2",
  "source":  "upstream"
}
```

## requires

```json
"requires": {
  "os":              ["linux"],
  "arch":            ["x86_64"],
  "system_packages": ["ca-certificates"],
  "system_tools":    ["tar", "sha256sum", "curl", "jq"]
}
```

| Field | Notes |
|-------|-------|
| `os` | List of supported OSes. install.sh refuses if `uname -s` isn't in the list. |
| `arch` | List of supported architectures. install.sh refuses if `uname -m` isn't in the list. |
| `system_packages` | Names of apt/dnf packages assumed installed. **install.sh does NOT install these** — just fails loudly with exit 40 if `dpkg -l <pkg>` is empty. |
| `system_tools` | Command names that must be on `PATH`. install.sh fails with exit 40 if any is missing. |

## runtime (bundled runtimes inside the tarball)

```json
"runtime": [
  {
    "name":    "node",
    "version": "22.22.3",
    "src":     "runtime/node/",
    "install": "{{TARGET_ROOT}}/openclaw/runtime/node/"
  }
]
```

| Field | Required | Notes |
|-------|----------|-------|
| `name` | yes | Display name (`node`, `python`, `uv`, ...) |
| `version` | yes | Runtime version |
| `src` | yes | Path inside this tarball. Must exist. |
| `install` | yes | Path on the target machine. Use `{{TARGET_ROOT}}` placeholder. |

`runtime: []` means the agent has no bundled runtime (opencode — pure static binary).

> **区别**：`runtime[]` 是「本 tarball 自带的运行时」（schema 1.0）；`runtime_constraints[]` 是「需要从系统获取的运行时版本约束」（schema 1.1+）。两者并存，互不替代。

## payload

```json
"payload": [
  {
    "src":  "payload/bin/opencode",
    "dst":  "{{TARGET_ROOT}}/opencode/0.0.55/bin/opencode",
    "mode": "0755"
  }
]
```

| Field | Required | Notes |
|-------|----------|-------|
| `src` | yes | Path inside this tarball. Must exist. |
| `dst` | yes | Path on the target machine. Use `{{TARGET_ROOT}}` placeholder. |
| `mode` | yes | Octal mode for the deployed file. Applied with `chmod`. |

If `src` ends with `/`, install.sh recursively copies the directory tree into `dst`.

## checksums

```json
"checksums": {
  "manifest.json":                  "sha256:fc78...",     // intentionally excluded — see note
  "install.sh":                     "sha256:dea9...",
  "uninstall.sh":                   "sha256:b71a...",
  "payload/bin/opencode":           "sha256:f7fe...",
  "runtime/python/bin/python3.12":  "sha256:10a4..."
}
```

A map from relative path to `sha256:<hex>`.

- **Excludes `manifest.json`** by convention — putting the manifest's own checksum in itself creates a chicken-and-egg problem. verify.sh handles this implicitly.
- **Excludes files inside `migrate/`** for now. install.sh does not verify migration scripts before running them (it's a trust-the-publisher model).
- **Files not listed** in `checksums` are NOT verified — verify.sh only checks files in this map.
- Use `tools/verify.sh --fix` to auto-populate TBD entries.

## tarball

```json
"tarball": {
  "filename":       "opencode-upstream-0.0.55.tar.gz",
  "sha256":         "sha256:6ab77142cf9e8460e01f25f57c847408c87e314ba32c3472fb67a89a5bd5125c",
  "signature_type": "none"
}
```

Auto-filled by `tools/pack.sh` after a successful tar. `signature_type` is `"none"` until you run `tools/sign.sh` (which doesn't update this field today — consumers check `.sig` file presence separately).

## upgrade

```json
"upgrade": {
  "strategy":         "replace",
  "compatible_from":  ["0.0.54", "0.0.53", "0.0.52"],
  "migrations": [
    { "from": "0.0.x", "script": "migrate/from-0.0.x.sh" }
  ]
}
```

| Field | Notes |
|-------|-------|
| `strategy` | `replace` (overwrite) or `side-by-side` (install alongside) |
| `compatible_from` | List of known-compatible prior versions (informational — not enforced) |
| `migrations` | Migration scripts install.sh should consider when upgrading from a prior version. install.sh walks them in priority order: exact match → minor wildcard → major wildcard. |

## scripts

```json
"scripts": {
  "install":   "install.sh",
  "uninstall": "uninstall.sh"
}
```

Names of the install + uninstall scripts in this directory. (Default: same names as the JSON keys.)

## services (schema 1.1+)

```json
"services": [
  {
    "name":        "gateway",
    "command":     ["openclaw", "gateway", "--port", "8080"],
    "args":        [],
    "restart":     "on-failure",
    "working_dir": "{{DEPLOY_ROOT}}",
    "description": "openclaw gateway daemon"
  }
]
```

| Field | Required | Notes |
|-------|----------|-------|
| `name` | yes | Unique within the agent. Becomes the suffix on the systemd unit: `<agent>-<name>.service` |
| `command` | yes | argv to exec as `ExecStart=` |
| `args` | no | Appended to `command` (informational) |
| `restart` | yes | One of `on-failure`, `always`, `no` |
| `working_dir` | no | systemd `WorkingDirectory=`. `{{DEPLOY_ROOT}}` is substituted with `$TARGET_ROOT/<agent>/<version>`. Defaults to `{{DEPLOY_ROOT}}` |
| `description` | no | systemd `Description=` |

agentpkg runs after `install.sh` succeeds and: writes the unit file to `~/.config/systemd/user/<agent>-<name>.service`, runs `systemctl --user daemon-reload`, then `systemctl --user enable --now <unit>`. Soft-fails (warning) on `systemctl --user` absence. The agent's `WorkingDirectory=` is created by `install.sh` before the unit starts.

## configs (schema 1.1+)

```json
"configs": [
  {
    "name":     "openclaw",
    "file":     "openclaw.json",
    "render_to": "~/.openclaw/openclaw.json",
    "mode":     "0600"
  }
]
```

| Field | Required | Notes |
|-------|----------|-------|
| `name` | yes | Logical name (informational) |
| `file` | yes | Basename the agent's `render-config.sh` writes |
| `render_to` | yes | Final on-host path (e.g. `~/.openclaw/openclaw.json`). agentpkg records this for uninstall; the agent's render script owns the actual write |
| `mode` | yes | chmod mode (string, e.g. `"0600"`). Informational — the render script performs the chmod |

The render-config.sh script for each agent lives at `<version>/render-config.sh` in the tarball and is invoked by `agentpkg config generate <agent> --config-input <file>` (Stage 1 of install). The script reads `$AGENT_MARKETPLACE_CONFIG_INPUT` (a JSON file the daemon writes), parses its keys, and writes the config file to `render_to` with the declared `mode`.