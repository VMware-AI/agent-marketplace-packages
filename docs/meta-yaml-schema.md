# meta.yaml schema

`meta.yaml` lives at `agents/<name>/meta.yaml` and stores **agent-level** marketing metadata. It is version-independent: all versions of an agent share the same `meta.yaml`.

`agentpkg package build` reads it, validates it, and embeds a copy into every tarball's root. The marketplace-api also reads it (during `package build` / `reindex`) and folds the fields into `dist/index.json` for frontend consumption.

## Schema

```yaml
# Required
display_name: "OpenCode"        # human-readable name (UI label); falls back to agent name if blank
description: "AI 编码智能体..."  # one-line description, 10–200 chars
category:    "developer"        # from docs/category-catalog.md

# Optional
logo:        ""                 # empty, http(s) URL, or data:image/...;base64,... (see docs/logo-format.md)
runtime_type: "vm"              # vm | container | k8s — see "Runtime type" below
tags:                            # 0–10 search tags, lowercase, dash-separated
  - ai
  - terminal
  - code
```

## Field reference

| Field | Type | Required | Constraints |
|-------|------|----------|-------------|
| `display_name` | string | no (defaults to `agent` name) | — |
| `description` | string | **yes** | 10–200 characters |
| `category` | string | **yes** | must be in [category catalog](category-catalog.md) |
| `logo` | string | no | empty, http(s) URL, or `data:image/...;base64,...`; see [logo format](logo-format.md) |
| `runtime_type` | string | no (defaults to `vm`) | one of `vm`, `container`, `k8s` — see below |
| `tags` | list[string] | no | 0–10 items, each matches `[a-z0-9-]{1,32}` |

## Runtime type

`runtime_type` declares the deployment target the agent is packaged for. It is **declared once in `meta.yaml`** (not per-version in `manifest.json`) because for a given agent it is stable across all versions — a k8s-targeted agent ships k8s-targeted versions, and so on. If `runtime_type` is omitted, `Defaults()` fills in `"vm"`.

| Value | Meaning |
|-------|---------|
| `vm` | systemd --user on a VM / bare-metal host. This is the default and matches every current agent (`opencode`, `openclaw`, `hermes-agent`). |
| `container` | Single-container run — the agent ships its own image / entrypoint and `install.sh` (if any) just drops a launcher. |
| `k8s` | Helm chart or k8s-manifest driven install — `install.sh` emits a `Deployment` / `Service` rather than a systemd unit. |

The marketplace frontend can filter on `runtime_type` to show only agents that match the operator's target environment.

## Validation

`agentpkg package verify` (and `agentpkg package build`) check `meta.yaml` against the schema before touching any tarball or dist/ contents. On validation failure the build aborts with a clear error message.

Validation source-of-truth lives in:

- `internal/manifest/meta.go` — Go validation (incl. `validateLogo` for the logo format)
- `internal/manifest/catalogs.go` — category catalog

## Example

```yaml
# agents/opencode/meta.yaml
display_name: "OpenCode"
description: "AI 编码智能体，专注于终端内代码理解、生成与调试"
logo: "https://example.com/opencode.svg"
category: "developer"
tags:
  - ai
  - terminal
  - code
```

## How it flows into the index

`agentpkg package build` reads meta.yaml from `agents/<name>/`, then for each tarball it puts the same meta.yaml at the tarball root. `agentpkg package reindex` (and the build step after) reads every tarball's manifest.json + meta.yaml, aggregates by (name, source), and writes `dist/index.json` like this:

```json
{
  "agents": [
    {
      "name":         "opencode",
      "display_name": "OpenCode",
      "description":  "AI 编码智能体，...",
      "logo":         "https://example.com/opencode.svg",
      "category":     "developer",
      "tags":         ["ai", "terminal", "code"],
      "runtime_type": "vm",
      "versions": [
        { "version": "0.0.55", "source": "upstream", "channel": "stable", "tarball": {...}, "manifest": {...} },
        { "version": "0.0.54", "source": "upstream", "channel": "stable", "tarball": {...}, "manifest": {...} }
      ]
    }
  ]
}
```

The frontend never has to read meta.yaml directly — it gets everything through `/api/v1/index`.

## When meta.yaml is missing

If `agents/<name>/meta.yaml` is absent:

- `agentpkg package init` is run and meta is created with placeholder values
- `agentpkg package build` proceeds but logs a warning
- The agent still appears in `dist/index.json` with empty marketing fields
- `agentpkg package verify` reports a warning but does not fail

The intent is: meta.yaml missing should not block publishing. Frontend can decide how to render an agent with no marketing data.