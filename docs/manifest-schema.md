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
  "runtime_requirements": [ ... ],
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

## runtime_requirements (system-side runtime constraints, schema 1.1+)

```json
"runtime_requirements": [
  {
    "name":         "python",
    "version":      ">=3.12,<3.14",
    "verify_cmd":   "python3.12 --version",
    "install_hint": "apt install -y python3.12 (Ubuntu 22.04+) or use pyenv: 'pyenv install 3.12.13'"
  }
]
```

| Field | Required | Notes |
|-------|----------|-------|
| `name` | yes | Runtime name (`node`, `python`, `uv`, ...) |
| `version` | yes | Constraint expression parsed by the agent's `install.sh` and by `tools/install-runtime.sh`. Examples: `">=22.22.3 <23, >=24.15.0 <25, or >=25.9.0"`, `">=3.12,<3.14"`. |
| `verify_cmd` | yes | Shell command that prints the installed runtime's version on stdout. Used by `install.sh` for the `>=` floor check. |
| `install_hint` | no | Human-readable install instructions. Surfaced by `install.sh` on `FAIL: ...` exit 50 and by `tools/install-runtime.sh`. |

> **区别**：`runtime[]` 是「本 tarball 自带的运行时」（schema 1.0，仅 openclaw 用）；`runtime_requirements[]` 是「需要从系统获取的运行时版本约束」（schema 1.1+，openclaw / hermes-agent）。两者并存，互不替代。

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

> **硬约束：服务端口固定为 `8080`** —— schema 没有独立的 `port` 字段；端口只能放在 `command[]` argv 里，且 **必须为 `8080`**。原因：systemd 单元的 `ExecStart=` 是把 `command` 数组原样用空格拼接而成（[internal/cli/install.go:449-451](internal/cli/install.go#L449-L451)），整个单元里没有 `Environment=` 行，`agentpkg install` 也没有 `--port` 旗标，所以无法在运行时覆盖。新增 / 修改 agent 模板时如果要调整端口，请同步修改 `manifest.json` 里的 `command[]` 和对应的 verify / install 测试断言，重新 `agentpkg package build` 发布。`agentpkg package init` 也按此约束默认生成 `["<name>", "serve", "--port", "8080"]`。

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

## Config schemas (schema 1.2+, `config/` directory)

> New in 1.2. Optional but strongly recommended. The `configs[]` block in `manifest.json` is the *delivery contract* (file path + mode + name); the `config/` directory is the *shape contract* — it tells the platform's deploy wizard what the rendered config file's structure looks like, so the form can be generated from JSON Schema and the raw YAML/JSON editor can validate user input.

Per-agent layout (relative to the version directory):

```
agents/<name>/<source>/<version>/
├── manifest.json
├── install.sh
├── render-config.sh
├── payload/
└── config/                       # NEW in 1.2
    ├── schema.json               # full multi-slot JSON Schema (draft-07)
    ├── example.yaml              # example values; "Load example" in the wizard
    ├── <slotName>.schema.json    # OPTIONAL: per-slot fragment (migration ramp)
    └── <slotName>.example.yaml   # OPTIONAL: per-slot example
```

### `config/schema.json`

Top-level multi-slot JSON Schema (draft-07). The platform's resolver reads this verbatim and stamps enrichment keywords on top (see below); the wizard's form library consumes it directly. The top-level shape is:

```json
{
  "$schema": "http://json-schema.org/draft-07/schema#",
  "type": "object",
  "additionalProperties": true,
  "properties": {
    "<slotName>": { "type": "object", "...": "..." }
  }
}
```

`<slotName>` must match `manifest.configs[].name`. Each slot's value is a full JSON Schema for the rendered config file. For multi-slot packages (e.g. `hermes-agent`, which has both `hermes` for `config.yaml` and `hermes-env` for `.env`), each slot is a sibling under `properties`.

### `config/example.yaml`

Multi-document YAML; one document per slot (use `---` between slots). The wizard's "Load example" button parses this with `yaml.v3` and pre-fills the form (or the raw editor) with the values. Use realistic-but-redacted values: real-looking model ids, real-looking provider names, but **never paste real API keys** — those are auto-injected from the bound virtual key.

### Per-slot fragments (`config/<slotName>.schema.json`)

**Migration ramp only.** When authored, the platform composes the per-slot fragments into a virtual top-level schema. Once you have a top-level `config/schema.json`, the per-slot fragments are ignored. Plan to consolidate per-slot fragments into a top-level file in a follow-up release.

### Vocabulary boundary

Schemas must use the **conservative draft-07** vocabulary. Specifically supported:

- `type` (string / integer / number / boolean / array / object / null)
- `properties` + `required` + `additionalProperties` (always `true` at the top level and at every nested object — power users can add anything in raw mode)
- `enum`, `const`, `default`
- `minimum`, `maximum`, `minLength`, `maxLength`, `pattern`, `format` (`uri`, `email`, `hostname`, `ipv4`, `ipv6`, `uuid` are recognised by `ajv-formats`)
- `minItems`, `maxItems`, `uniqueItems`
- `description`, `title`, `examples`
- `oneOf` / `anyOf` / `allOf` (use sparingly — the form library only renders the first valid branch)
- `x-*` extensions (see below)

**Not supported** (will be rejected by `ajv@8` in strict mode and by `santhosh-tekuri/jsonschema/v5`):

- `if` / `then` / `else` (draft-07 conditional; needs draft 2019-09)
- `unevaluatedProperties` (draft 2019-09)
- `$ref` to external files (the platform's resolver is single-file only; inline subschemas via `allOf`)
- `$defs` is allowed but not surfaced as separate components in the form

### `x-*` enrichment keywords

The platform stamps these on top of the user's schema; **do not write them by hand** (the server overrides). They're documented here so template authors can read their schemas and know what the form library will do.

| Keyword | Where | Meaning |
|---|---|---|
| `x-secret-source: "virtual_key"` | string field | The platform auto-injects the value from the bound virtual key at deploy time. Form: widget is locked (no input); raw mode: the user's value is discarded and replaced. |
| `x-secret-source: "user_input"` | string field | User-supplied secret. Form: `format: "password"` input; raw mode: value is passed through verbatim. The form library marks the field as `format: "password"` automatically. |
| `x-secret-source: "env_var_name"` | string field | The platform auto-injects the **name** of the env var (not the key itself) that holds the provider's API key. Used by `hermes-agent` where the config file references the env-var name and the actual key lives in the env. |
| `x-provider-choices: { "<field>": { "builtin": [...], "custom": "..." } }` | on the parent object | The form library renders the named field as a "pick provider" select (driven by `supported_providers.builtin + custom`) plus a sub-form for the chosen provider's options. The `x-provider-choices` block is stamped by the server from `manifest.configs[].supported_providers` — schema authors do not write it. |
| `x-provider-choices-source: "<slotName>"` | string field | The provider choices for this field are inherited from `<slotName>`'s `supported_providers` block. Used by `hermes-agent`'s `model.provider` (which inherits from the `hermes` slot's choices). |

The server-side enricher (in `agent-platform-backend/internal/graph/agent_marketplace_helpers.go::enrichSchema`) is the single source of truth for these — schema authors should expect the server to add/override them.

### Migration timeline

| State | Marker | Behaviour |
|---|---|---|
| **No `config/` directory** | `config_schemas_status = "missing"` | Wizard falls back to the legacy form (typed fields from `manifest.configs[].required_inputs / optional_inputs`). Raw editor only; no schema validation. |
| **Per-slot fragments only** | `config_schemas_status = "partial"` | Form renders for the schema'd slot; other slot goes raw-only. |
| **Top-level `config/schema.json`** | `config_schemas_status = "ok"` | Form renders for all slots; full schema validation. |

The publish-time validator (`make validate` in this repo, when configured) refuses to build a tarball whose `config_schemas_status` is `missing` once the package has shipped a version with schemas — there's no path back to "no schemas" once you've adopted them.

### Worked example: `opencode` 1.19.0

```json
{
  "$schema": "http://json-schema.org/draft-07/schema#",
  "type": "object",
  "additionalProperties": true,
  "properties": {
    "opencode": {
      "type": "object",
      "additionalProperties": true,
      "required": ["provider"],
      "properties": {
        "provider": {
          "type": "object",
          "minProperties": 1,
          "additionalProperties": {
            "type": "object",
            "properties": {
              "options": {
                "type": "object",
                "properties": {
                  "apiKey": { "type": "string", "x-secret-source": "virtual_key" },
                  "baseURL": { "type": "string", "format": "uri" }
                }
              }
            }
          }
        },
        "model":      { "type": "string" },
        "small_model":{ "type": "string" },
        "share":      { "type": "string", "enum": ["manual", "auto", "disabled"] },
        "server": {
          "type": "object",
          "properties": {
            "port":     { "type": "integer", "minimum": 1, "maximum": 65535 },
            "hostname": { "type": "string" }
          }
        }
      }
    }
  }
}
```

And the matching `config/example.yaml`:

```yaml
opencode:
  provider:
    openai:
      options:
        apiKey: ""          # auto-injected (x-secret-source: virtual_key)
  model: gpt-4o
  server:
    port: 8080
    hostname: 0.0.0.0
```

When this package is published and the platform syncs it, the wizard shows a form with: a "Pick provider" select (with all `supported_providers.builtin` ids + "custom"), the chosen provider's `apiKey` field locked (the user can't type — they bind a virtual key in a separate widget), `model` as a free-text input, `share` as a 3-option select, and `server.port` / `server.hostname` as a nested sub-form. The "Raw YAML/JSON" toggle drops the user into a CodeMirror editor that validates against the same schema on every keystroke.

### Worked example: `hermes-agent` 0.19.0 (multi-slot, mixed file formats)

`config/schema.json` has two siblings under `properties`:

```json
{
  "properties": {
    "hermes":     { "type": "object", "...": "YAML-shaped schema" },
    "hermes-env": { "type": "object", "...": "env-var-shaped schema" }
  }
}
```

The `hermes` slot's `model.provider` field uses `x-provider-choices-source: "hermes"` to inherit the provider list from the same slot. The `hermes-env` slot has no provider logic of its own — the dynamic `<PROVIDER>_API_KEY` env-var line is injected by the platform based on the chosen provider from the `hermes` slot.

`config/example.yaml` uses `---` to separate the two slots:

```yaml
hermes:
  model:
    provider: openai
    default: gpt-4o

---

hermes-env:
  HERMES_API_TIMEOUT: 60
  HERMES_OPENROUTER_CACHE: false
```

The in-VM `render-config.sh` knows each slot's file format (from `manifest.configs[].file` extension / name): the `hermes` slot writes YAML via `yq`, the `hermes-env` slot writes `KEY=VALUE` lines via plain `printf`.

### See also

- `agents/opencode/upstream/1.19.0/config/` — full single-slot example
- `agents/openclaw/upstream/2026.7.1-2/config/` — full single-slot example (different shape)
- `agents/hermes-agent/upstream/0.19.0/config/` — full multi-slot, mixed-format example
- `internal/manifest/manifest.go::ConfigSpec` — the Go type for the legacy `configs[].required_inputs/optional_inputs` shape (still the VM's `render-config.sh` contract during the transition)
- `internal/manifest/manifest.go::InputField` — the Go type for a single legacy field (still drives the platform's typed-form fallback when `config_schemas_status = "missing"`)