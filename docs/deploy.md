# 部署指南

marketplace-api 提供三种部署方式：

1. **裸机 / 虚拟机 systemd** —— 直接跑二进制，适合小规模
2. **`docker run` 单容器** —— 用 `deploy/start_marketplace_docker.sh` 一键
3. **docker compose** —— `deploy/compose/docker-compose.yml`

三者都加载同一份 YAML 配置（`deploy/config/marketplace-api.example.yaml` 是模板）。

---

## 三种方式对照

| 维度 | systemd | docker run | docker compose |
|---|---|---|---|
| 启动命令 | `systemctl start marketplace-api` | `./deploy/start_marketplace_docker.sh up` | `docker compose up -d` |
| TLS 证书 | 你提供 | 脚本自签（需 EXTERNAL_IP） | 脚本自签 |
| 日志位置 | journald (按配置可加 `--log-file`) | `docker logs`（也是 stdout/stderr） | `docker compose logs` |
| `dist/` 更新 | `systemctl restart marketplace-api` | `docker restart marketplace-api` | `docker compose restart` |
| 适合场景 | 内网、生产长期运行 | 本地开发、临时演示 | 多服务协同、未来加 nginx/caddy |

---

## 1. systemd（裸机 / 虚拟机）

参见 [deploy/systemd/README.md](../deploy/systemd/README.md)。三步：

1. 复制 `deploy/systemd/marketplace-api.service` 到 `/etc/systemd/system/`
2. 创建 `EnvironmentFile=/etc/agent-marketplace/env` 并写入 `MARKETPLACE_API_PASSWORD=...`
3. `systemctl daemon-reload && systemctl enable --now marketplace-api`

`dist/` 通过 bind-mount 注入（把 `dist/` 拷或软链到 `/var/lib/agent-marketplace/dist`）。

---

## 2. docker run 一键脚本

脚本：[deploy/start_marketplace_docker.sh](../deploy/start_marketplace_docker.sh)

```bash
# 1) 必需：设置 EXTERNAL_IP（自签名证书 SAN 的目标）
export EXTERNAL_IP=192.168.1.42

# 2) 必需：设置密码（或者放到 deploy/config/.env）
export MARKETPLACE_API_PASSWORD='强密码'

# 3) 拉镜像并运行
./deploy/start_marketplace_docker.sh up
```

子命令：

| 子命令 | 行为 |
|---|---|
| `up`（默认） | 校验 + TLS 生成 + `docker run`（自动 `--rm`，重启会自动重拉镜像） |
| `configure` | 仅生成 `deploy/config/config.yaml` 和 `deploy/tls/`，不启动容器 |
| `down` | `docker stop`（保留容器） |
| `clean` | `docker rm -f`（删除容器） |
| `status` | 打印容器存在/状态 |
| `logs` | `docker logs -f` 跟踪日志 |

约束：

- 脚本拒绝 loopback（`localhost` / `127.0.0.1` / `0.0.0.0` / `::1`）作为 EXTERNAL_IP —— 这种值生成的证书 SAN 没什么能验证
- 必须有现成的 `dist/`（含 `index.json`）
- 不会写任何东西到 `dist/`

容器**始终**在容器内 8443 上监听，宿主端口由 `HOST_PORT=8443` 控制。

---

## 3. docker compose

参见 [deploy/compose/docker-compose.yml](../deploy/compose/docker-compose.yml)。

```bash
cd deploy/compose

# 生成自签名证书（一次性；EXTERNAL_IP 改变后重新生成）
EXTERNAL_IP=192.168.1.42 ./gen-tls.sh

# 准备 .env
cp ../config/.env.example ./.env
$EDITOR ./.env   # 设置 MARKETPLACE_API_PASSWORD

# 起栈
docker compose up -d

# dist/ 增加新 tarball 后
docker compose restart
```

compose 健康检查直接 ping `/api/v1/health`（见 docker-compose.yml:33-37）。

---

## 配置：`config/config.yaml`

所有方式共用同一份 YAML。模板：[deploy/config/marketplace-api.example.yaml](../deploy/config/marketplace-api.example.yaml)

四个段落：

```yaml
server:
  listen: "0.0.0.0:8443"
  tls_cert: "/etc/agent-marketplace/tls/tls.crt"
  tls_key:  "/etc/agent-marketplace/tls/tls.key"

auth:
  password_env: "MARKETPLACE_API_PASSWORD"

repo:
  dist_dir: "/var/lib/agent-marketplace/dist"

logging:
  level:  "info"     # debug | info | warn | error
  format: "text"     # text | json
  file:   ""         # 空 = 只 stdout；指定 = 同时追加到这个文件
```

### 优先级

CLI flag > 环境变量 > YAML。

| 字段 | CLI flag | 环境变量 | YAML |
|---|---|---|---|
| `server.listen` | — | `MARKETPLACE_API_LISTEN` | `server.listen` |
| `server.tls_cert` | — | `MARKETPLACE_API_TLS_CERT` | `server.tls_cert` |
| `server.tls_key` | — | `MARKETPLACE_API_TLS_KEY` | `server.tls_key` |
| `repo.dist_dir` | — | `MARKETPLACE_API_DIST_DIR` | `repo.dist_dir` |
| `logging.level` | — | `MARKETPLACE_API_LOG_LEVEL` | `logging.level` |
| `logging.format` | `--log-format` | `MARKETPLACE_API_LOG_FORMAT` | `logging.format` |
| `logging.file` | `--log-file` | `MARKETPLACE_API_LOG_FILE` | `logging.file` |
| 密码 | — | `cfg.auth.password_env` 指定的变量，**不**进 YAML | 永不进 YAML |

### 日志位置选择

- **stdout**：默认；容器/k8s/systemd 统一收集
- **`--log-file=/var/log/x.log`**：额外追加到此文件，与 stdout 并存
- 容器内：建议留 `--log-file` 为空，由 docker/k8s 收集 stdout
- 裸机 systemd：可以同时开 stdout（journald 收）+ `--log-file`（logrotate 收）

---

## 版本号与构建

`cat VERSION` 拿到当前版本（如 `v0.0.1`）。Tag 形式：`$(VERSION)-$(date -u +%Y%m%d)`。

```bash
# 本地编译
make build              # 编译 marketplace-api + agentpkg
make build-api          # 仅 marketplace-api
make build-agentpkg     # 仅 agentpkg

# 跨平台
make build-linux        # GOOS=linux GOARCH=amd64，仅 marketplace-api

# 多架构镜像（推到 quay.io/vmware-ai/agent-marketplace-api）
make release-images TAG=v0.1.0-20250730
```

支持的平台：`linux/amd64,linux/arm64`。

---

## dist/ 更新流程

marketplace-api 在启动时加载 `dist/index.json`，运行时**只读**。要更新 index，必须重启。

1. 打包新版本：

```bash
agentpkg package build agents/opencode/upstream/1.18.9 --out dist
agentpkg package reindex --out dist
```

2. 让运行中的服务读到新 `dist/`：

| 部署方式 | 操作 |
|---|---|
| systemd | `systemctl restart marketplace-api` |
| docker run | `./deploy/start_marketplace_docker.sh down && ... up`（脚本会重新拉镜像 + 重挂卷） |
| docker compose | `docker compose restart marketplace-api` |

> 因为 `dist/` 是 bind-mount **只读**挂入的，容器内永远不会写它。每次重启读到磁盘最新内容。

---

## 安全建议

- 生产**必须**用 CA 签证书而非自签。把 `deploy/tls/tls.{crt,key}` 替换掉再启动。
- `password_env` 默认是 `MARKETPLACE_API_PASSWORD`；任何能读到该环境变量的进程都能消费 API。把该文件权限设置为 `0600`。
- 若前面放了 nginx / caddy 终结 TLS，可以把 `server.tls_cert` / `server.tls_key` 留空，让 marketplace-api 走 HTTP（容器内通信，反向代理供外部 HTTPS）。
- 日志里**不**会泄漏密码或 tarball 内容。若需要审计访问日志，配 reverse proxy 的 access log 更稳。