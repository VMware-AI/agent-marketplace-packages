# Skills catalog reference

`SKILL.md` frontmatter (schema v2.0) draws values from three closed
catalogs, declared in [`internal/skills/catalogs.go`][catalogs].

[catalogs]: https://github.com/VMware-AI/agent-marketplace-packages/blob/main/internal/skills/catalogs.go

## Categories

Skill **categories** describe *what the skill does* (its functional
domain). Distinct from the agent-side [category catalog][agent-cat] which
describes the *type of agent*. The two vocabularies don't collapse — a
"dev" skill can ship inside an "automation"-typed agent.

| ID | Label (zh) | Description |
|----|-----------|-------------|
| `ops` | 运维 | 运维 / SRE / 监控 |
| `dev` | 开发辅助 | 代码生成、调试、code review |
| `data` | 数据分析 | 数据处理 / 报表 / ETL |
| `search` | 检索 | Web 搜索 / RAG / 知识库 |
| `media` | 多媒体 | 图像 / 视频 / 音频处理 |
| `content` | 内容创作 | 写作、文案 |
| `integration` | 集成 | 第三方集成（GitHub / Notion / Slack 等） |
| `productivity` | 效率 | 通用工具 / 效率提升 |
| `other` | 其他 | 兜底 |

**Required.** Must be one of these IDs.

## Agents

The set of runtimes that recognize this skill payload. The
`agentpkg skills install` command materializes the artifact on the
target machine **once per declared agent** (zip-extract only — system-wide
methods collapse to one install target).

| ID | Description |
|----|-------------|
| `opencode` | [opencode.ai](https://opencode.ai/) CLI |
| `openclaw` | openclaw agent (our internal vendor agent) |
| `hermes` | hermes-agent (Python) |
| `all` | Sentinel — no agent-specific install path; uses the central fallback. Combine with concrete agents is rejected. |

**Required.** At least one entry.

## Install methods

How `agentpkg skills install` should materialize the artifact.

| Method | Description |
|--------|-------------|
| `zip-extract` | Default. Extract the zip verbatim into the resolved path. |
| `pip-wheel` | Extract zip to staging, then `uv pip install --no-index --find-links <staging>/wheels`. Requires the zip to contain a `wheels/` subdir. |
| `npm-pack` | Run `npm install -g <install_config.package>`. The zip payload is ignored. |
| `tarball` | Extract zip to staging, locate the single `.tar.gz` payload inside, then extract that into the resolved path. Honors `install_config.strip_components`. |

**Optional.** Defaults to `zip-extract` for legacy zips.

## Default install paths

The marketplace uses these built-in per-agent install dirs. Each
template uses `$HOME` and `$NAME` placeholders, expanded at install
time. Override per-skill via `metadata.install_paths.<agent>`.

| Agent | Default path | Source |
|-------|--------------|--------|
| `opencode` | `$HOME/.config/opencode/skills/$NAME` | opencode.ai/docs/skills (global location) |
| `openclaw` | `$HOME/.openclaw/skills/$NAME` | openclaw docs/tools/skills.md (managed tier) |
| `hermes` | `$HOME/.hermes/optional-skills/$NAME` | `hermes_constants.py:get_optional_skills_dir` (`HERMES_HOME/optional-skills`) |
| `all` (fallback) | `$HOME/.local/share/agentpkg/skills/$NAME` | Central stash; also where `state.json` lives. |

Override example:

```yaml
metadata:
  install_paths:
    opencode: "$HOME/work/skills/$NAME"   # custom path for this skill
```

The CLI flag `--target-dir <dir>` overrides **all** paths (the historical
behavior). Per-agent paths are only used when `--target-dir` is empty.

## Adding new entries

Don't invent new IDs out-of-band — open a PR against
[`internal/skills/catalogs.go`][catalogs] and update this table.
IDs are kebab-case-lowercase.