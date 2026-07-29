# hermes-agent 0.19.0 (upstream)

## About

Offline-installable bundle for [`hermes-agent`](https://github.com/NousResearch/hermes-agent) `0.19.0`, sourced from the upstream PyPI package. hermes-agent is described upstream as "the self-improving AI agent — creates skills from experience, improves them during use, and runs anywhere."

## What is bundled

| Path | Contents | Size |
|---|---|---|
| `runtime/python/` | Python 3.12.13 (`cpython-3.12.13+20260623-x86_64-unknown-linux-gnu-install_only_stripped`) | ~102 MB |
| `runtime/uv/` | uv 0.11.28 (`uv-x86_64-unknown-linux-gnu`) | ~63 MB |
| `payload/hermes_agent-0.19.0-py3-none-any.whl` | The upstream Python wheel itself | ~10.1 MB |

## What is NOT bundled (pulled at install time)

`hermes-agent` has 30+ transitive dependencies (openai, pydantic, fastapi, mcp, pillow, cryptography, etc.). Rather than bundle ~150 MB of wheels that change frequently, `install.sh` resolves them at install time using `uv pip install` against a configured PyPI mirror.

Default mirror: `https://mirrors.aliyun.com/pypi/simple/` (chosen because it was the only reliable mirror reachable from the build host). Override at install time:

```bash
AGENT_MARKETPLACE_PYPI_MIRROR=https://pypi.tuna.tsinghua.edu.cn/simple/ \
    ./install.sh
```

For **fully-offline** installs (no PyPI access at install time), pre-populate `payload/wheels/` with all transitive deps:

```bash
# On a build host with network access:
tools/fetch.sh hermes-agent upstream 0.19.0   # downloads hermes-agent + transitive wheels
# Then repack and ship the tarball
```

## System prerequisites

In addition to the [universal prerequisites](../../../docs/prerequisites.md), install:

```bash
sudo apt-get install -y ca-certificates libssl3 zlib1g libffi8 libsqlite3-0
```

Python's TLS, sqlite, and zlib are dynamically linked against these libraries.

## Install

```bash
cd agents/hermes-agent/upstream/0.19.0
./install.sh
```

The install will:
1. Verify the tarball's checksums
2. Detect any prior install and run a migration if needed
3. Deploy Python 3.12 + uv to `$HOME/.local/hermes-agent/runtime/`
4. Create a venv at `$HOME/.local/hermes-agent/0.19.0/venv/`
5. Install `hermes-agent` + deps via `uv pip` from the configured mirror
6. Symlink `$HOME/.local/bin/hermes` to the venv's CLI
7. Run `hermes --version` to verify
8. Write `$HOME/.local/state/hermes-agent.state.json`

## Verify after install

```bash
$HOME/.local/bin/hermes --version
$HOME/.local/hermes-agent/runtime/python/bin/python3.12 --version
$HOME/.local/hermes-agent/runtime/uv/uv --version
```

## Known limitations

- **First install needs network** for `uv pip` to resolve transitive deps. Run `tools/fetch.sh` to fully pre-populate if the target machine is offline.
- **Linux x86_64 only**. macOS / arm64 not packaged yet — the bundled Python and uv binaries are Linux ELF.
- **Not validated end-to-end**: this bundle has not been installed on a real Linux container as of the initial commit. The Python + uv binaries are pulled from their official CI releases (python-build-standalone 20260623, uv 0.11.28) and their SHA256s are pinned, but the install procedure should be smoke-tested in your environment before deploying.
- **`hermes-agent` requires Python 3.11–3.13** — 3.12.13 is in range. Earlier versions of Python (e.g. system 3.10) will fail.