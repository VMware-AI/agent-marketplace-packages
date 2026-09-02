# marketplace-api · systemd unit

这一份是**裸机 / VM** 部署 marketplace-api 的最小 systemd 配置。docker 用户可以忽略（[deploy/start_marketplace_docker.sh](../start_marketplace_docker.sh) 是首选）。

## 三步启用

### 1. 装二进制 + 配置

```bash
# 1.1 编译
make build

# 1.2 复制二进制
sudo install -m 0755 bin/marketplace-api /usr/local/bin/marketplace-api

# 1.3 复制配置模板
sudo install -d -m 0755 /etc/agent-marketplace
sudo install -m 0644 deploy/config/marketplace-api.example.yaml \
                    /etc/agent-marketplace/config.yaml

# 1.4 复制 TLS 证书（生产请用 CA 签证书，不要用示例模板里留的）
sudo install -d -m 0700 /etc/agent-marketplace/tls
sudo cp YOUR_CERT.crt /etc/agent-marketplace/tls/tls.crt   && sudo chmod 0644
sudo cp YOUR_CERT.key /etc/agent-marketplace/tls/tls.key   && sudo chmod 0600

# 1.5 准备 dist/ 目录（mount / 复制 / 软链都可）
sudo install -d -m 0755 /var/lib/agent-marketplace/dist
# 假设 dist/ 在 /srv/agent-marketplace/dist
sudo mount --bind /srv/agent-marketplace/dist /var/lib/agent-marketplace/dist
# 或者直接复制过去、或者做个 ln -s

# 1.6 写密码到 EnvironmentFile
sudo tee /etc/agent-marketplace/env >/dev/null <<EOF
MARKETPLACE_API_PASSWORD=换成你的强密码
EOF
sudo chmod 0600 /etc/agent-marketplace/env
```

### 2. 装 unit

```bash
sudo install -m 0644 deploy/systemd/marketplace-api.service \
                /etc/systemd/system/marketplace-api.service
sudo systemctl daemon-reload
sudo systemctl enable --now marketplace-api
```

### 3. 验证

```bash
systemctl status marketplace-api
journalctl -u marketplace-api -n 50 -f
curl --cacert /etc/agent-marketplace/tls/tls.crt -u ":$MARKETPLACE_API_PASSWORD" \
  https://localhost:8443/api/v1/index
curl https://localhost:8443/api/v1/health   # 匿名探针
```

## 日志

默认全走 stdout/stderr，由 **journald** 收集：

```bash
journalctl -u marketplace-api -f
journalctl -u marketplace-api --since "1 hour ago"
```

如果想同时落盘一份便于 `grep`，在 `/etc/agent-marketplace/config.yaml` 里：

```yaml
logging:
  level:  "info"
  format: "text"
  file:   "/var/log/agent-marketplace/api.log"
```

并配 `logrotate`：

```
/var/log/agent-marketplace/api.log {
    daily
    rotate 14
    compress
    missingok
    notifempty
    postrotate
        systemctl reload marketplace-api || true
    endscript
}
```

## dist/ 更新

marketplace-api 运行时**只读** `dist/`。`agentpkg reindex` 把新的 tarball + `index.json` 写到 dist/ 之后，**无需重启服务**——server 内部每 10s 自动 stat `dist/index.json`，发现 mtime 或 size 变化就重新加载。也支持手动 reload：

```bash
# 1. 拷新 tarball 进 /srv/agent-marketplace/dist/
cp /path/to/new.tar.gz /srv/agent-marketplace/dist/
# 2. 重生 index
agentpkg package reindex --out /srv/agent-marketplace/dist
# 3. 立即生效（可选——poll loop 默认 10s 也会自动捡起来）
sudo systemctl reload marketplace-api
# 或不用 systemd：
#   kill -HUP $(pgrep -f marketplace-api)
```

reload 失败（旧 index 坏了）服务继续返回旧数据并在 journald 打 WARN，绝不 crash。轮询间隔可通过 `MARKETPLACE_API_POLL_INTERVAL=0` 关闭（只保留 SIGHUP）。

## 与 docker 路径的取舍

| 维度 | systemd | docker run / compose |
|---|---|---|
| 依赖 | 主机已有 systemd | 主机有 docker |
| TLS | 你提供证书 | 脚本可自签 |
| 隔离 | 主机进程（最小 sandbox 见 unit 里的 Protect* 设置） | 完整容器隔离 |
| dist/ 更新 | 重启 + 自管 mount | 重启 compose / docker run 自动重挂卷 |
| 适合场景 | 单台内网服务器长期跑 | 多服务 / CI / 临时演示 |

如果你只在 CI 或本地测试用，**用 docker**；生产跑长期服务，systemd 更轻量。

## 常见问题

**Q: `systemctl start marketplace-api` 报 `status=203/EXEC`？**

`ExecStart` 路径不存在或没执行权限。检查：
```bash
ls -la /usr/local/bin/marketplace-api
/usr/local/bin/marketplace-api --version
```

**Q: 服务起来了但 401？**

`MARKETPLACE_API_PASSWORD` 没读到。检查：
```bash
systemctl show marketplace-api -p Environment
# 应输出 MARKETPLACE_API_PASSWORD=...
journalctl -u marketplace-api -e | grep -i password
```

**Q: 怎么 reload dist 而不重启？**

支持，详见上文 "## dist/ 更新"。两种触发：

- **自动**：server 每 10s 一次 stat `dist/index.json`，mtime 或 size 变化即 reload。`agentpkg reindex` 之后最多等 10s 即可。
- **手动**：`sudo systemctl reload marketplace-api`（unit 里有 `ExecReload=/bin/kill -HUP $MAINPID`）或 `kill -HUP <pid>`。

reload 失败不影响在线服务，旧数据继续返回。