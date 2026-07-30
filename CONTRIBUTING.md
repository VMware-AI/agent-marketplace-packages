# 贡献指南 — 新增 (agent, version) 包

本 SOP 面向**新增一个现有 agent 的新版本**。要新增全新的 agent，请看末尾 [§ 新增 agent](#新增-agent)。

## 准备

打包主机需要：

- `bash ≥ 4`、`coreutils`、`findutils`、`grep`、`sed`、`awk`、`tar`、`gzip`、`sha256sum`、`curl`
- `node`（仅当打包 openclaw 时需要 `npm pack`）
- `python3 ≥ 3.11` + `pip`（仅当打包 hermes-agent 时）
- 联网访问上游 registry（npm / PyPI / GitHub Releases）
- 已编译的 `agentpkg`：`make build-agentpkg`，产物在 `bin/agentpkg`

---

## 步骤 1 — 选 source 子树

- `upstream/`：1:1 镜像上游公开发布
- `ours/`：内部 fork / patch / 自定义构建

```bash
mkdir -p agents/<agent>/upstream/<version>
cd agents/<agent>/upstream/<version>
mkdir -p payload runtime files migrate
```

这个目录**就是**未来 tarball 的根——一切从这里打包。

---

## 步骤 2 — 拉上游 artifact

```bash
# opencode — GitHub Releases 上的 static binary
./tools/fetch.sh opencode upstream 1.18.9

# openclaw — npm tarball
./tools/fetch.sh openclaw upstream 2026.7.2

# hermes-agent — pip wheels (含所有传递依赖)
./tools/fetch.sh hermes-agent upstream 0.19.0
```

每个 `fetch.sh`：

1. 解析上游规范 URL（并 pin 上游 artifact 的 SHA256）
2. 下载到 `payload/`
3. 写一份带 `agent`/`source`/`version`/`upstream.sha256` 的 stub `manifest.json`

重复运行是安全的——会先比 SHA256，已分叉的 payload 拒绝覆盖。

---

## 步骤 3 — 离线预解析依赖

target 端装的时候**不能联网**。所以 payload 必须把所有依赖都打包好：

```bash
# openclaw：解析整棵 npm 树到 payload/openclaw/（global prefix 布局）
NPM_REGISTRY=https://registry.npmmirror.com/ \
  npm install --prefix agents/openclaw/upstream/<v>/payload/openclaw \
    --global --no-audit --no-fund \
    openclaw@<v>

# hermes-agent：下载全部传递 wheel（linux manylinux）
python3 -m pip download \
  --dest agents/hermes-agent/upstream/<v>/payload/wheels \
  --index-url https://mirrors.aliyun.com/pypi/simple/ \
  --python-version 3.12 \
  --platform manylinux2014_x86_64 \
  --only-binary=:all: \
  "hermes-agent==<v>"
```

`agentpkg package build` 把 `payload/` 整目录打进去。装的时候 install.sh 用 vendored tree：

- npm：`cp -a payload/openclaw/. $DEPLOY_ROOT/`
- Python：`uv pip install --no-index --find-links payload/wheels/`

**Runtime（Node / Python / uv）不打进 tarball**。这些在 target 机上用 `./tools/install-runtime.sh` 装，版本约束写在 `manifest.runtime_constraints` 里。

---

## 步骤 4 — 写 manifest.json

参照 [docs/manifest-schema.md](docs/manifest-schema.md)。最少要填：

- `agent` / `source` / `version` / `channel`
- `upstream.sha256`（fetch.sh 已经填好）
- `runtime_constraints[]` —— target 自带的 runtime 列表，例如：
  ```json
  { "name": "node", "min_version": ">=22.22.3 <23", ... }
  ```
- `payload[]` —— 拷到 `$HOME/.local/<agent>/...` 的文件
- `requires.system_packages` / `requires.system_tools`
- `upgrade.compatible_from` / `upgrade.migrations`（参考 [docs/upgrade-protocol.md](docs/upgrade-protocol.md)）
- `services[]` / `configs[]`（schema 1.1+）—— [docs/manifest-schema.md#services](docs/manifest-schema.md) 有完整字段说明

`checksums` **不用手写** —— `agentpkg package build` 会算好。

---

## 步骤 5 — 写 install.sh

按 [docs/install-protocol.md](docs/install-protocol.md) 的契约。最低要求：

1. 重新校验 tarball SHA256 对 `manifest.tarball.sha256`；不匹配立即 fail
2. 重算 `payload/` `runtime/` `files/` 下每个文件 SHA256 对 `manifest.checksums`
3. 检查 `requires.system_tools` 在 `PATH` 上；缺失 exit 40
4. **校验 runtime 要求**：检查 `manifest.runtime_constraints` 中声明的 runtime 是否在 target 上就位；缺失或版本不对，exit 50 并打印 `install_hint`
5. 读 `$HOME/.local/state/<agent>.state.json`（如存在），决定 fresh install 还是 upgrade
6. 部署 runtime + payload + files 到 `$HOME/.local/<agent>/...`（只走用户可写路径）
7. 写新的 `state.json`
8. 跑 `<agent> --version` 验证；成功时打印 `INSTALLED_VERSION=<output>`
9. exit 0

参考模板：`agents/opencode/upstream/1.18.9/install.sh`。

---

## 步骤 6 — 写 uninstall.sh

跟 install.sh 反过来：

- 读 `state.json`
- 按 `installed_files` 逐个 `rm -f`
- `rm -rf "$DEPLOY_ROOT"`
- 沿 `$TARGET_ROOT` 向上 rmdir 空目录，到 `$TARGET_ROOT` 为止
- `rm -f "$STATE_FILE"`

state 已不存在时 exit 0（幂等）。

---

## 步骤 7 — migrate/

**重大版本号变更**时，往 `migrate/` 里加迁移脚本：

- `migrate/from-<previous-major-minor>.x.sh` —— 同 major 同 minor 的所有老版本都会跑
- `migrate/from-<exact>.sh` —— 只有那个具体老版本跑

匹配顺序（[docs/upgrade-protocol.md § 优先级匹配](docs/upgrade-protocol.md)）：

1. 精确：`from-<installed-version>.sh`
2. major.minor 通配：`from-<major>.<minor>.x.sh`
3. major 通配：`from-<major>.x.x.sh`
4. 没匹配：跳过迁移（默认假设兼容）

迁移脚本**必须**幂等、可重跑、失败时只 WARN 不 abort。

---

## 步骤 8 — 校验

```bash
agentpkg package verify agents/<agent>/upstream/<version>
```

会做：

1. `meta.yaml` 字段校验
2. `manifest.json` schema 合规
3. 重算每个文件的 SHA256 对 `manifest.checksums`

不通过就 exit 非零。

---

## 步骤 9 — 打 tarball

```bash
agentpkg package build agents/<agent>/upstream/<version> --out dist
```

产物：

- `dist/<agent>-<source>-<version>.tar.gz`
- `dist/<agent>-<source>-<version>.tar.gz.sha256`

并且**自动** `agentpkg package reindex` 一次。

---

## 步骤 10 — 签名（强烈建议）

```bash
agentpkg package sign dist/<agent>-<source>-<version>.tar.gz --key <GPG_KEY_ID>
# 产物：dist/<agent>-<source>-<version>.tar.gz.asc
```

详见 [docs/publishing-model.md](docs/publishing-model.md)。

---

## 步骤 11 — 发布

```bash
# 自己写到 CDN / S3 / OSS
./tools/publish.sh dist/<agent>-<source>-<version>.tar.gz --target s3://my-cdn
./tools/publish.sh dist/<agent>-<source>-<version>.tar.gz.asc --target s3://my-cdn
./tools/publish.sh dist/index.json                                  --target s3://my-cdn

# 让市场 API 读到新 dist（bind-mount 是只读的，重启即可）
docker compose restart            # 或 docker restart / systemctl restart
```

---

## 新增 agent

加一个全新 agent：

1. 创建 `agents/<new-agent>/upstream/1.0.0/`（或合适的起始版本号）
2. 跑 `agentpkg package init <new-agent> --version 1.0.0` 一次性 scaffold
3. 在 `agents/<new-agent>/README.md` 里写 agent-specific 注意事项
4. 更新根 `README.md` 的「当前已打包的 agent」表
5. 在 manifest 里写明 runtime 约束；消费者用 `./tools/install-runtime.sh install --from-manifest <path>`（Ubuntu 24.04 only）预装

---

## 版本号约定

| Agent | Scheme | 例子 |
|---|---|---|
| opencode | `MAJOR.MINOR.PATCH` | `1.18.9` |
| openclaw | CalVer (`YYYY.M.P`) | `2026.7.2` |
| hermes-agent | `MAJOR.MINOR.PATCH` | `0.19.0` |

**不要**自创版本号 scheme——用上游项目的，以便 `compatible_from` 匹配工作。