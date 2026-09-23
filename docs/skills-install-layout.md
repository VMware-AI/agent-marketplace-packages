# Skills install layout

`agentpkg skills install` and `agentpkg skills uninstall` operate on
a local directory tree only — they never talk to the marketplace-api
(unless `--from` is omitted, in which case install pulls the zip
from the registry and then does everything below).

Schema v2.0 adds **per-agent install paths**: each skill declares
which runtimes recognize it (`agents:` in SKILL.md), and the CLI
materializes the artifact on the target machine **once per agent**.
For a single-agent skill with `agents: [opencode]`, only one path
is touched; for multi-agent skills (`agents: [opencode, openclaw, hermes]`),
the same payload is fanned out to each.

## Default location

Two distinct roots are involved at install time:

- **State root** (always `$HOME/.local/share/agentpkg/skills/`): where
  `state.json` lives. This is the central stash that `agentpkg skills
  list-installed` scans.
- **Payload root** (default: per-agent, see table below): where the
  unzipped payload lands. For multi-agent skills this is fan-out; for
  `agents: [all]` it falls back to the central stash.

Set `--target-dir <path>` to switch the payload root to a single
override (legacy / staging / CI mode). When `--target-dir` is set, it
also serves as the state root, so everything lives under one tree.

## Per-agent paths

Each agent runtime looks for skills in its own directory tree.
Schema v2.0's built-in default paths:

| Agent | Default path |
|-------|--------------|
| `opencode` | `~/.config/opencode/skills/<name>/<version>/` |
| `openclaw` | `~/.openclaw/skills/<name>/<version>/` |
| `hermes` | `~/.hermes/optional-skills/<name>/<version>/` |
| `all` (fallback) | `~/.local/share/agentpkg/skills/<name>/<version>/` |

Override any of these in SKILL.md:

```yaml
metadata:
  install_paths:
    opencode: "$HOME/work/skills/$NAME"
```

The full catalog reference is in [skill-catalog.md](skill-catalog.md).

## Per-skill directory

```
skills/
└── <name>/
    ├── <version>/
    │   ├── SKILL.md
    │   ├── scripts/
    │   ├── references/
    │   ├── assets/
    │   └── ...
    ├── latest -> <version>          # symlink to the active version
    └── state.json                   # local install metadata
```

Each `<version>` directory is a complete extract of the skill zip
(minus the trailing `.zip` extension). File modes from the zip are
preserved — `scripts/*` retain their executable bit.

`<name>/latest` is a symlink that points at the version you most
recently installed or set as current. Resolving it (`readlink`) gives
you the active version without reading `state.json`.

Multiple versions can coexist side by side — install v1.0 then v2.0-beta.1
and you get both. Re-installing the same `(source, version)` is a
no-op unless `--force` is set.

For multi-agent installs, this directory tree is replicated under each
agent's resolved path. `state.json` lives only in the central stash
(`$HOME/.local/share/agentpkg/skills/<name>/state.json`) — the per-agent
paths hold only the payload.

## `state.json`

`<target-dir>/<name>/state.json` records the install metadata.
Sample:

```json
{
  "name": "my-skill",
  "current_version": "1.0.0",
  "source": "community",
  "channel": "stable",
  "target_dir": "/home/alice/.local/share/agentpkg/skills/my-skill",
  "install_method": "zip-extract",
  "agents": ["opencode", "hermes"],
  "resolved_paths": {
    "opencode": "/home/alice/.config/opencode/skills/my-skill/1.0.0",
    "hermes": "/home/alice/.hermes/optional-skills/my-skill/1.0.0"
  },
  "installed_versions": {
    "1.0.0": {
      "source": "community",
      "channel": "stable",
      "installed_at": "2026-09-08T10:00:00Z",
      "zip_sha256": "sha256:abcd...",
      "zip_filename": "my-skill-community-1.0.0.zip",
      "released_at": "2026-09-01T00:00:00Z"
    }
  }
}
```

Top-level fields:

| Field | |
|---|---|
| `name` | The skill name (kebab-case). |
| `current_version` | The version `latest` points at. Empty if no versions remain. |
| `source` | Tree the skill was installed from (`community` or `internal`). |
| `channel` | Channel of the current version (`stable`, `beta`, `edge`, `internal`). |
| `target_dir` | Absolute path of `<target-dir>/<name>`. |
| `install_method` | Schema v2.0: which pipeline was used (`zip-extract` / `pip-wheel` / `npm-pack` / `tarball`). |
| `agents` | Schema v2.0: runtimes this skill was installed for. Empty list is normalized to `["all"]`. |
| `resolved_paths` | Schema v2.0: per-agent on-disk path actually written. |
| `installed_versions` | Map keyed by version string. Each entry's fields: |

Per-version fields (under `installed_versions.<version>`):

| Field | |
|---|---|
| `source` | Source this specific version was installed from. |
| `channel` | Channel of this specific version. |
| `installed_at` | RFC3339 timestamp of when install completed. |
| `zip_sha256` | `sha256:<hex>` of the zip that was installed — verified in-flight. |
| `zip_filename` | The original zip filename (e.g. `<name>-<source>-<version>.zip`). |
| `released_at` | RFC3339 timestamp from the registry's `SkillVersion.released_at`. May be empty for offline installs. |

## State atomicity

`state.json` is written via a tmp-file + fsync + rename, so a crash
mid-write never leaves a half-written file on disk. Re-installing
the same `(source, version)` is a no-op when the recorded `zip_sha256`
matches the new bytes.

## Uninstall semantics

`agentpkg skills uninstall <name>` (no `--version`) deletes the entire
`<target-dir>/<name>/` subtree, including `state.json`.

`agentpkg skills uninstall <name> --version <v>` deletes only the
`<version>` directory:

- If `v` is the current version, the `latest` symlink is repointed to
  the next-highest-semver installed version (or removed entirely if
  none remain).
- If `v` is the only remaining version, `state.json` is deleted too.
- Otherwise, the entry is removed from `installed_versions` and
  `state.json` is rewritten.

`--keep-state` keeps `state.json` intact even after the on-disk
directories are removed. Useful for "temporarily park the skill
without losing the recorded install metadata".

## Why versioned (not flat)

Anthropic Skills use a flat `~/.claude/skills/<name>/` layout. We use
the versioned layout above because:

1. **Consistency with agents**: agents use
   `~/.local/share/agentpkg/<name>/<version>/` already. Skills follow
   the same shape so operator muscle memory transfers.
2. **Multi-version coexistence**: you can install both
   `1.0.0` (stable) and `2.0.0-beta.1` (beta) side by side and
   pick the active one via `--version`. The flat layout forces
   re-installs that lose the previous version.
3. **Symlink for the common case**: the `latest` symlink provides
   Anthropic-style single-active-version ergonomics without
   sacrificing multi-version storage.

If you specifically want Anthropic's flat layout, set
`--target-dir ~/.claude/skills/<name>` — the CLI will treat that path
as the per-version directory directly.