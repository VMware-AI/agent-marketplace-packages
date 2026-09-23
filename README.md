# Agent Marketplace Packages

面向 `opencode`、`openclaw`、`hermes-agent` 的**离线分发 + HTTP 服务**包仓库。每个 `(agent, version)` 都打成一个自包含 tarball，由 `marketplace-api` 通过 HTTP 暴露，由 `agentpkg` 拉取并安装到目标机器。

---

## 仓库包含什么

| 组件 | 路径 | 用途 |
|---|---|---|
| `marketplace-api` (Go) | [cmd/marketplace-api](cmd/marketplace-api/) | 只读 HTTP 服务：暴露 `dist/` 为 JSON + tarball API（agents）以及 `dist/skills/` 为 JSON + zip API（skills） |
| `agentpkg` (Go) | [cmd/agentpkg](cmd/agentpkg/) | 双用途 CLI：agents 的 VM 侧安装 / 打包方 authoring；skills 的 author-side 构建 + repo-side 上传/下载 + consumer-side install |
| 已打包的 agent | [agents/](agents/) | 三类 agent × 每个版本目录（含 manifest + install.sh + payload） |
| 已打包的 skill | `dist/skills/` | ZIP 形式的 + skill zip + `.sha256` 副文件；SKILL.md frontmatter 兼容 Anthropic Skills |
| 部署脚本与配置 | [deploy/](deploy/) | docker / docker-compose / systemd / 模板 config |
| API + 协议文档 | [docs/](docs/) | API 参考、部署、CLI 手册、manifest schema 等 |

`tools/*.sh` 是 shell 版本的辅助脚本，**保留过渡用**；新代码请用 `agentpkg package ...` / `agentpkg skills ...`。

> Skills 注册中心与 agents 注册中心**完全独立**：不同的包格式（zip + SKILL.md vs tar.gz + meta.yaml）、不同的 HTTP 路由、不同的 CLI 子命令、不共享类型。详见 [docs/skill-md-schema.md](docs/skill-md-schema.md) 与 [docs/skills-api.md](docs/skills-api.md)。

---

## 架构一览

```
打包方                            运营方
────────                          ──────────
agents/<n>/upstream/<v>/          marketplace-api (TLS+auth)
   manifest.json                      ↓ HTTP Basic Auth
   install.sh                         ↓
   payload/                       dist/*.tar.gz              (agents)
                                   dist/skills/*.zip         (skills)
   ↑                                   ↑   ↑
   │                                   │   │
agentpkg package build ──→ dist/ ←───  bind-mount (agents)
agentpkg skills build    ──→ dist/skills/ ←─── bind-mount (skills)
                                      │
                                      │
                              agentpkg CLI
                                      ↓
                              target VM:
                                  bin/<agent>
                                  systemd --user unit
                                  ~/.local/state/<agent>.state.json
                                  ~/.local/share/agentpkg/skills/<name>/<version>/
                                  ~/.local/share/agentpkg/skills/<name>/latest → <version>
                                  ~/.local/share/agentpkg/skills/<name>/state.json
```

---

## 快速开始（消费方）

假设市场 API 已经在 `https://marketplace.example.com:8443` 跑起来，密码是 `$MARKETPLACE_API_PASSWORD`。

```bash
# 1. 登录并把密码写到 ~/.config/agentpkg/credentials
agentpkg login --server https://marketplace.example.com:8443 \
               --password-file <(echo "$MARKETPLACE_API_PASSWORD")

# 2. 看有什么可以装
agentpkg index

# 3. 安装一个 agent（默认 stable 最新版；会自动写 systemd --user unit）
agentpkg install opencode --version 1.18.9

# 4. 验证
$HOME/.local/bin/opencode --version
systemctl --user status opencode-web.service   # 如果 manifest 声明了服务
```

升级：

```bash
agentpkg upgrade opencode --version 1.19.0
```

卸载：

```bash
agentpkg uninstall opencode
```

详见 [docs/agentpkg.md](docs/agentpkg.md)。

---

## 快速开始（运营方）

起一个 marketplace-api 服务：

```bash
# 本地编译
make build

# 单容器一键（自签 TLS，要求 EXTERNAL_IP）
EXTERNAL_IP=$(curl -s https://ifconfig.me)
export MARKETPLACE_API_PASSWORD='强密码'
./deploy/start_marketplace_docker.sh up
```

或者直接跑二进制：

```bash
MARKETPLACE_API_PASSWORD='强密码' \
  ./bin/marketplace-api \
  --config deploy/config/marketplace-api.example.yaml \
  --log-format json
```

或者用 docker compose：

```bash
cd deploy/compose
EXTERNAL_IP=$YOUR_IP ./gen-tls.sh
cp ../config/.env.example ./.env && $EDITOR ./.env
docker compose up -d
```

详情见 [docs/deploy.md](docs/deploy.md)。

---

## 快速开始（打包方）

加一个新版本：

```bash
# 1. scaffold
agentpkg package init openclaw --source upstream --version 2026.7.2

# 2. fetch 上游 artifact
./tools/fetch.sh openclaw upstream 2026.7.2

# 3. 写 manifest.json + install.sh + ...（详见 docs/manifest-schema.md）

# 4. 校验
agentpkg package verify agents/openclaw/upstream/2026.7.2

# 5. 打 tarball + 重生 dist/index.json
agentpkg package build agents/openclaw/upstream/2026.7.2 --out dist

# 6. 签名（强烈建议）
agentpkg package sign dist/openclaw-upstream-2026.7.2.tar.gz

# 7. 推到 CDN / 让市场 API 读到（dist/ bind-mount 是只读的，重启即可）
make release-images TAG=v0.1.0-$(date -u +%Y%m%d)   # 如果推到 quay
```

SOP 见 [CONTRIBUTING.md](CONTRIBUTING.md)。

---

## 当前已打包的 agent

| Agent | Source | Version | Runtime（目标机自备） | Tarball 大小（约） |
|---|---|---|---|---|
| opencode | upstream | 1.18.9 | 无（Go static binary） | ~14 MB |
| openclaw | upstream | 2026.7.1-2 | Node.js ≥22.22.3 / ≥24.15.0 / ≥25.9 | ~62 MB |
| hermes-agent | upstream | 0.19.0 | Python 3.12 + uv ≥0.11 | ~47 MB |

Runtime **不在 tarball 里**，由目标机用 [tools/install-runtime.sh](tools/install-runtime.sh) 在 Ubuntu 24.04 上预装（详见 [docs/prerequisites.md](docs/prerequisites.md)）。

每个版本的细节见 `agents/<name>/upstream/<version>/README.md`。

---

## 日志 / 鉴权 / API 文档

- **日志**：`marketplace-api` 用 `log/slog`（Go 1.21+ 标准库）。默认输出 **stdout**，可选 `--log-file` 追加写文件。级别由 `logging.level` 或 `MARKETPLACE_API_LOG_LEVEL` 控制（合法值 `debug|info|warn|error`）。详见 [docs/deploy.md § 配置](docs/deploy.md#配置configconfigyaml)。
- **鉴权**：所有接口（除 `/api/v1/health`）走 HTTP Basic Auth，密码来自环境变量（默认 `MARKETPLACE_API_PASSWORD`）。`/api/v1/health` 是匿名探针，由路由顺序保证，被 `TestHealth_NoAuthRequired` 测试锁住。
- **API 文档**：机器可读规范在 [docs/api/openapi.json](docs/api/openapi.json)；浏览器访问 `https://<host>:8443/swagger` 看 Swagger UI（CDN 加载）。手工改了 spec 后跑 `make openapi-embed && make openapi-check`。

---

## 项目结构

```
agent-marketplace-packages/
├── cmd/
│   ├── marketplace-api/         # HTTP server
│   └── agentpkg/               # CLI
├── internal/
│   ├── apitypes/               # API JSON contracts
│   ├── cli/                    # agentpkg 的 VM-side 命令
│   ├── cli/packagecmd/         # agentpkg 的 author-side 命令
│   ├── manifest/               # manifest.json 解析
│   ├── repo/                   # dist/ 加载 + 校验
│   └── server/                 # HTTP handlers + middleware + router
├── agents/                     # 已打包的 agent
│   └── <name>/<source>/<version>/
│       ├── meta.yaml
│       ├── manifest.json
│       ├── install.sh
│       ├── uninstall.sh
│       ├── payload/
│       └── migrate/
├── dist/                       # build 出来的 tarball + index.json
├── deploy/
│   ├── start_marketplace_docker.sh    # docker run 一键
│   ├── compose/                # docker-compose
│   ├── config/                 # YAML 模板
│   ├── docker/                 # Dockerfile
│   ├── systemd/                # systemd unit 模板
│   └── tls/                    # 自签证书
├── docs/                       # API/CLI/部署/协议的文档（中文为主）
├── tools/                      # shell 维护脚本（过渡中）
├── Makefile                    # build / test / openapi-check / release-images
├── go.mod / go.sum
└── VERSION                     # 当前版本号
```

---

## 限制

- **未做端到端实跑测试** —— install.sh 是按「构造正确」设计的，但首次在新环境部署前请自己验一遍
- **Linux x86_64 / arm64** —— 已通过 multi-arch 镜像支持；macOS / Windows 暂未
- **TLS** —— 默认自签（仅适合本地 trust 验证），生产请替换为 CA 签证书
- **Trusted-path publishing** —— sha256 防篡改但防不了恶意 CDN 替换整个 tarball；生产请同时启用 GPG 签名（见 [docs/publishing-model.md](docs/publishing-model.md)）
- **Agent 服务端口硬编码为 `8080`** —— 当前 manifest schema 没有暴露端口配置字段，所有 agent 的 `services[].command` 都必须把 `--port 8080` 直接写在 argv 里；systemd unit 是把 manifest 里的 `command` 数组原样拼成 `ExecStart=`（见 [internal/cli/install.go:449-451](internal/cli/install.go#L449-L451)），没有 env-var 覆盖通道，`agentpkg install` 也没有 `--port` 旗标。**新增或修改 agent 模板时如需调整端口，必须同时修改 `agents/<name>/upstream/<version>/manifest.json` 以及对应的 verify / install 测试断言，重新 `agentpkg package build` 并发布新 tarball 才会生效。**`agentpkg package init` 也会默认按 `--port 8080` 生成 `services[].command`。

---

## 许可

[MIT](LICENSE)