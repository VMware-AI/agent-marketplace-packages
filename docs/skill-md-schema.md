# SKILL.md schema reference

`SKILL.md` is the manifest inside every skill zip. It uses YAML
frontmatter (delimited by `---`) plus a Markdown body. Compatible with
[Anthropic Skills](https://mintlify.wiki/anthropics/skills/creating-skills/frontmatter):
Anthropic agents see only `name` + `description` and ignore everything
else, but our registry reads the full schema below.

## Required fields

```yaml
name: my-skill                       # kebab-case, ^[a-z0-9][a-z0-9-]{0,63}$
description: >                       # 1-1024 chars; no `<` or `>`; WHAT and WHEN
  Adds web search capability. Takes a query string and returns the
  top results. Use when the user asks for current information or links.
category: search                     # Schema v2.0: ops|dev|data|search|media|content|integration|productivity|other
agents: [opencode, hermes]           # Schema v2.0: ≥1 of opencode|openclaw|hermes|all
```

`name` rules (per Anthropic + the registry's [validator][manifest.go]):
- kebab-case (lowercase + digits + internal hyphens)
- 1–64 characters total
- no leading or trailing hyphen
- no consecutive hyphens (`--`)

`description` rules:
- non-empty
- 1–1024 characters
- must not contain `<` or `>` (escape via HTML entities if needed)

`category` rules (schema v2.0):
- required
- one of the IDs in [docs/skill-catalog.md](skill-catalog.md#categories)

`agents` rules (schema v2.0):
- required, ≥1 entry
- each entry is one of `opencode`, `openclaw`, `hermes`, `all`
- `"all"` must be the sole entry (cannot be combined with concrete agents)

## Anthropic-compatible optional fields

```yaml
license: MIT                          # ≤40 chars
allowed-tools: [Read, Grep, Bash]     # tool whitelist for runtime agents
compatibility: "Requires Python 3.10+, jq"
```

## Registry extensions (top-level)

These are ignored by Anthropic Skills but read by the marketplace-api
and stored in `dist/skills-index.json`.

```yaml
version: 1.0.0                        # strict semver MAJOR.MINOR.PATCH[-prerelease]
author: Jane Developer                # ≤80 chars
tags: [search, web]                   # ≤10 items; each matches ^[a-z0-9-]{1,32}$
created_at: 2026-09-01                # ISO 8601 date YYYY-MM-DD
homepage: https://example.com/skill   # http(s) URL; ≤200 chars
```

`version` is **required** for registry uploads (the Anthropic spec
treats it as optional). It must be strict semver; `v1.0`, `1.0`,
and `latest` are all rejected by [`semver.IsValid`][strictSemver].

[strictSemver]: https://pkg.go.dev/golang.org/x/mod/semver

## Registry extensions under `metadata`

The Anthropic Skills spec allows arbitrary `metadata` keys. The
marketplace reads a small schema under that key.

```yaml
metadata:
  requires:
    os: [linux, darwin]               # subset of linux, darwin, windows
    arch: [amd64, arm64]              # subset of amd64, arm64, 386, arm
    tools: [bash, jq]                 # ≤20 basename entries
  inputs:                             # informational; runtime decides enforcement
    - name: target_dir
      description: Directory where files will be written
      required: true
      type: string                    # string | integer | boolean | number | enum
      default: /tmp/output
    - name: max_files
      description: Maximum number of files
      required: false
      type: integer
      default: "100"
    - name: mode
      type: enum
      enum_values: [read, write, append]   # required when type=enum
  entry_point: scripts/run.sh         # ≤200 chars; must resolve inside the zip
  # Schema v2.0 — install pipeline + per-agent path overrides:
  install_method: zip-extract         # zip-extract | pip-wheel | npm-pack | tarball
  install_paths:                      # per-agent override of DefaultAgentInstallPaths
    opencode: "$HOME/.config/opencode/skills/$NAME"
    hermes:   "$HOME/.hermes/optional-skills/$NAME"
  install_config:                     # per-method config; shape varies by install_method
    # zip-extract example:
    entry_point: scripts/run.sh
    # pip-wheel example:
    # wheel: pkg/foo-1.0.0-py3-none-any.whl
    # npm-pack example:
    # package: "@scope/foo"
    # tarball example:
    # strip_components: 1
```

`entry_point` is informational only at the registry level. The
`agentpkg skills install` command does NOT execute scripts — skills
are pure content, never run by the registry.

`install_method` is informational at upload time; the actual install
behavior is driven by `agentpkg skills install` which dispatches on
this value. See [docs/skill-catalog.md](skill-catalog.md#install-methods)
for the closed enum and what each does.

`install_paths.<agent>` overrides the built-in agent→path table
(`DefaultAgentInstallPaths`); the key must be one of `opencode`,
`openclaw`, `hermes`, or `all`. The `all` key is the universal-sentinel
override: when paired with `agents: [all]`, the value replaces the
central fallback path at install time. Useful when an operator wants
the skill to land in a custom central tree (e.g.
`/srv/skills/$NAME`) without giving up the "system-wide" semantic:

```yaml
agents: [all]
metadata:
  install_method: zip-extract
  install_paths:
    all: "/srv/agent-skills/$NAME"
```

The value must contain `$NAME` (so the template expands deterministically
per skill). `$HOME` is not required — some setups use shared system trees.

## What `source` and `channel` are NOT

The fields `source` and `channel` live in `SkillVersion` (per release),
**not** in SKILL.md. They're set at upload time:

- `source`: passed as `--source` on `agentpkg skills upload` (default
  `community`). Indicates whether the upload is from a community
  contributor (`community`) or the official maintainers (`internal`).
- `channel`: passed as `--channel` on `agentpkg skills upload` (default
  `stable`). Dist-tag equivalent of npm — `stable`, `beta`, `edge`,
  `internal`. The CLI's `--channel stable` selects the highest-semver
  stable version when no explicit `--version` is given.

If your SKILL.md accidentally contains a `source:` field, the CLI
warns and ignores it:

```
WARN: SKILL.md has a 'source' field but it's ignored; use --source flag instead
```

## Install methods + per-agent paths

`metadata.install_method` selects the install pipeline (see
[skill-catalog.md](skill-catalog.md#install-methods)). `agents`
declares which runtimes the skill targets; `metadata.install_paths`
overrides the built-in agent→path table.

The CLI's `install` command (in schema v2.0) handles the per-agent
fan-out:

```bash
# agents: [opencode]  →  ~/.config/opencode/skills/foo/1.0.0/
agentpkg skills install foo

# agents: [opencode, hermes]  →  installs into BOTH
agentpkg skills install foo

# agents: [all]  →  ~/.local/share/agentpkg/skills/foo/1.0.0/  (central stash)
agentpkg skills install foo

# --target-dir overrides everything (legacy behavior):
agentpkg skills install foo --target-dir /tmp/staging
# → /tmp/staging/foo/1.0.0/  for all agents
```

See [skills-install-layout.md](skills-install-layout.md) for the on-disk
layout and [skill-catalog.md](skill-catalog.md) for the catalog
reference.

## Body

Everything after the closing `---` is the Markdown body. It's preserved
verbatim in `dist/skills-index.json` (one per version, embedded in the
`body` field of `SkillVersion`) so UI list views can render cards
without ever unzipping the artifact.

The canonical bytes still live in the zip's `SKILL.md` entry. Fetch
them via `GET /api/v1/skills/{source}/{name}/{version}/SKILL.md` for
diff tools that need byte-level parity with what the author uploaded.

## Validation

`agentpkg skills verify <path-to-skill-dir>` parses the frontmatter,
validates against the schema, and prints a summary. The same validator
runs server-side on every upload — uploading a zip with an invalid
SKILL.md returns 400 with a specific error code (`invalid_manifest`).

[manifest.go]: https://github.com/VMware-AI/agent-marketplace-packages/blob/main/internal/skills/manifest.go