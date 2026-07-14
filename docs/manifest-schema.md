# manifest.json — schema reference

Every version directory must contain `manifest.json`. This file is the single source of truth for everything install.sh and verify.sh need.

## Top-level fields

```json
{
  "schema_version": "1.0",
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
  "scripts":   { ... }
}
```

| Field | Required | Type | Notes |
|-------|----------|------|-------|
| `schema_version` | yes | string | `"1.0"` for now. Bumped on breaking schema changes. |
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

## runtime

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