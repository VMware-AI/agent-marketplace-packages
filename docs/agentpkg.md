# agentpkg — CLI 使用手册

agentpkg 是一个 Go 写的双用途 CLI：

- **虚拟机侧（VM-side）**：登录、查 index、下载、生成配置、安装/升级/卸载 agent
- **打包方（author-side）**：scaffold、校验、打 tarball、重新生成 index、GPG 签名

源码位于 [internal/cli](../internal/cli)，挂载在 [cmd/agentpkg/main.go](../cmd/agentpkg/main.go)。所有子命令通过 `cobra` 注册。

---

## 全局选项

```text
agentpkg [--config <path>] [--credentials <path>] [--version] <subcommand> [...]
```

| 参数 | 默认值 | 说明 |
|---|---|---|
| `--config` | `~/.config/agentpkg/config.yaml` | 配置文件路径（保存 server URL） |
| `--credentials` | `~/.config/agentpkg/credentials` | 凭证文件路径（保存 Basic Auth 密码） |
| `--version` | — | 打印 `agentpkg 0.1.0` 并退出 |

未指定 `--config` / `--credentials` 时，使用 `~/` 下的默认文件（首次 `agentpkg login` 时自动创建，权限 `0600`/`0700`）。

---

## 子命令一览

| 命令 | 用途 | 形态 |
|---|---|---|
| `login` | 配置 server + 写入密码 | VM-side |
| `logout` | 删除凭证 | VM-side |
| `whoami` | 打印当前 server + 健康状态 | VM-side |
| `index` | 列出全部 agent | VM-side |
| `show` | 看单个 agent 的详情 | VM-side |
| `download` | 下载 tarball + sha256 侧车 | VM-side |
| `config generate` | 调 `render-config.sh` 渲染配置 | VM-side |
| `install` | 安装一个 agent（含 systemd 服务） | VM-side |
| `upgrade` | 升级已安装 agent | VM-side |
| `rollback` | 回滚到 state.json.previous 记录的版本 | VM-side |
| `uninstall` | 卸载 agent | VM-side |
| `package init` | scaffold 新 agent 目录 | author-side |
| `package verify` | 校验 manifest + 重新比对 checksum | author-side |
| `package build` | 打 tarball + sha256 侧车 | author-side |
| `package reindex` | 重扫 dist/ 写 index.json | author-side |
| `package sign` | gpg --detach-sign 签名 | author-side |

---

## VM-side 命令

### `login` —— 配置 server 与密码

```bash
agentpkg login --server https://marketplace.example.com:8443 \
               --password-file <(echo "$MARKETPLACE_API_PASSWORD")
# 或
agentpkg login --server https://...:8443 --password-stdin   <<< "$PW"
# 或（agentpkg daemon 驱动）
agentpkg login --server https://...:8443 --password "$MARKETPLACE_API_PASSWORD"

# 自签名证书 / 私有 CA 场景：
agentpkg login --server https://self-signed.example:8443 \
               --password-file <(echo "$PW") --skip-cert-verify
agentpkg login --server https://internal-ca.example:8443 \
               --password-file <(echo "$PW") --ca-cert /etc/ssl/enterprise-ca.pem
```

`--server` 写入配置；密码来源**互斥**（四选一）：

- `--password <value>` — 直接传（密码会出现在 `ps` / `/proc/<pid>/cmdline`，适合 daemon/托管调用方）
- `--password-stdin` — 从 stdin 读
- `--password-file <path>` — 从文件读（mode `0600`）
- 都不传 — 交互提示（`/dev/tty`）

成功后立即探测 `/api/v1/health` 验证连通。

**TLS 选项**（持久化到 `config.yaml`，所有后续命令自动继承，无需再次传）：

- `--skip-cert-verify` — 跳过 marketplace-api 的 TLS 证书校验。**不安全**：连接无法验证身份。仅用于一次性调试或自签名测试环境。启用时打印 `WARN: TLS certificate verification is disabled...` 到 stderr。
- `--ca-cert <path>` — 自定义 PEM CA bundle。私有 PKI / 企业 CA 部署时使用；CA 路径会被存为绝对路径（避免 cd 改变工作目录后失效）。文件不存在或不是合法 PEM 时报错并退出，**不会写入 config.yaml**（避免留一个之后每次命令都失败的配置）。

再次 `agentpkg login` 时若未显式传这些 flag，对应的 config 字段会被清掉——"最后一次 login 决定"的语义，方便操作员回滚到默认安全配置。

### `logout` —— 清除凭证

```bash
agentpkg logout
```

幂等：没有凭证文件时不报错。

### `whoami` —— 查当前连接

```bash
agentpkg whoami
# server:  https://...
# status:  ok (200)
```

### `index` —— 列出全部 agent

```bash
agentpkg index                            # 人类可读表格
agentpkg index --json                     # JSON 输出
agentpkg index --filter opencode          # 名字过滤
agentpkg index --channel beta             # 只看 beta 通道
agentpkg index --source ours              # 只看内部版本
```

### `show` —— 看单个 agent 详情

```bash
agentpkg show opencode                    # 默认 stable 通道所有版本
agentpkg show opencode --json             # JSON 输出
agentpkg show opencode --channel beta     # 只看 beta 通道
agentpkg show opencode --version 1.18.9   # 只看具体版本
```

### `download` —— 下载 tarball

```bash
agentpkg download opencode -o ./opencode.tar.gz
# 默认：自动选最新 stable 版本，写到当前目录
agentpkg download opencode --version 1.18.9 --source upstream -o /tmp/x.tar.gz
```

下载后立即校验 `.sha256` 侧车；不匹配则删除 tarball 并退出非零。

### `config generate` —— 渲染配置文件

调 tarball 里的 `render-config.sh`，产物写到 manifest 声明的 `configs[].render_to`。

```bash
agentpkg config generate opencode \
  --config-input ./my-config.json \
  --target-root ~/.local \
  --cache-dir /var/cache/agent-marketplace
```

`--config-input` 是 daemon 写好的 JSON 输入（schema 由 manifest `required_inputs` 声明）。

退出码：

- `0` 成功
- `70` 必填字段缺失
- `71` 渲染脚本错误

### `install` —— 安装一个 agent

```bash
agentpkg install opencode                                    # 默认 stable 最新版
agentpkg install opencode --version 1.18.9 --channel stable
agentpkg install opencode --target-root ~/.local --no-services   # 不起 systemd 服务
agentpkg install opencode --download-only                       # 只下 tarball，不装
agentpkg install opencode --cache-dir /var/cache/agent-marketplace
```

执行步骤：

1. 下载 tarball + sha256 侧车并校验
2. 解压到 `cache`，跑 `install.sh`
3. 解析 manifest，照 `services[]` 写 `~/.config/systemd/user/<agent>-<name>.service`
4. `systemctl --user daemon-reload` + `enable --now`
5. 把 `services / configs / config_dir` 追加到 `state.json`

`--no-services` 跳过步骤 3-5（适合 CI / 镜像构建）。

退出码：继承 `install.sh` 的退出码（0/10/20/30/40/50/60）+ 72（systemd --user 不可用，soft fail）。

### `upgrade` —— 升级已安装 agent

```bash
agentpkg upgrade opencode --version 1.19.0
agentpkg upgrade opencode --version 1.19.0 --dry-run   # 打印计划，不下 tarball、不跑 install.sh
```

同 install 流程，但 install.sh 走迁移路径（`migrate/from-<old>.sh`）。`state.json.previous` 写入老版本号。

`--dry-run` 在解析完目标版本 / 缓存路径 / 期望 sha256 后打印一份人类可读的升级计划（当前版本、目标版本、目标 root、缓存路径、即将执行的步骤），随后 exit 0。它**不会**下载 tarball、**不会**扫描 tarball 看有没有迁移脚本、**不会**触碰 state.json 或 systemd。编排器拿这份输出给用户展示 → 用户确认后再调一次去掉 `--dry-run` 真正执行。

### `uninstall` —— 卸载

```bash
agentpkg uninstall opencode --version 1.18.9
```

执行步骤：

1. 读 `state.json`
2. 禁用并删除 systemd `--user` units
3. 删除 `state.json.configs[].render_to` 文件
4. 跑 `uninstall.sh`（其内部进一步用 `installed_files` 清空 `$DEPLOY_ROOT`，并向上 rmdir 空目录直到 `$TARGET_ROOT`）

幂等：状态文件缺失时直接退出 0。

### `rollback` —— 回滚

```bash
agentpkg rollback opencode                          # 默认回滚到 state.json.previous.version
agentpkg rollback opencode --to-version 1.18.5      # 多步回滚（连续升级后想直接回到 1.18.5）
agentpkg rollback opencode --to-source ours         # 显式指定 source tree
```

执行步骤：

1. 读 `$TARGET_ROOT/state/<agent>.state.json` → 取 `.previous.version` / `.previous.source`（`--to-version` / `--to-source` 显式覆盖时优先）
2. 解析目标 tarball + 校验 sha256（命中 cache 则跳过下载）
3. 探测目标 tarball 里的 `migrate/to-<fromVersion>.sh`（可选反向迁移脚本）；若存在，先于 install.sh 执行。**失败仅 WARN**，install.sh 仍跑
4. 跑目标版本的 `install.sh`（它会把新 deploy_root 写出来，覆盖式替换文件；state.json 也会用新值覆盖，并写新的 `previous`）
5. 同 install 末尾：写/更新 systemd `--user` units + 把 services / configs / config_dir 追加进 state.json

`--no-services` 跳过第 5 步（CI / smoke-test 场景）。

`--json` 打印 rollback 计划为单个 JSON 对象到 stdout 后 exit 0；不下载、不执行任何脚本。计划包含 `current` / `target` 块、channel 解析结果、`inverse_migration.expected_script_basename`、`steps`、`note`。**注意**：计划是静态的——脚本是否实际存在需要解压 tarball 才能知道，所以 `inverse_migration.will_run` 永远是 `"unknown (requires tarball inspection)"`。编排器先 `agentpkg rollback --json` 拿计划 → 展示给用户 → 用户确认 → 编排器再去掉 `--json` 真跑。失败时 exit code 与上面表格一致。

退出码：

- `0` 成功
- `74` state.json 缺失或没有 `previous` 块（无法定位回滚目标）
- `75` 目标版本在 index 找不到 / tarball 无法解析
- `76` 下载或 sha256 校验失败
- inverse migration 失败 → WARN，agentpkg 仍然返回 0

rollback **永不阻塞**，**不读 stdin**，**不调任何交互 prompt**；编排器只需要看 exit code + stderr。

---

## author-side 命令（`package` 子组）

### `package init`

scaffold 新 agent 目录：

```bash
agentpkg package init openclaw --source upstream --version 2026.7.2 --channel stable
```

生成：

```
agents/openclaw/upstream/2026.7.2/
├── meta.yaml
├── manifest.json
├── install.sh
├── uninstall.sh
├── README.md
├── payload/bin/.gitkeep
└── migrate/.gitkeep
```

### `package verify`

```bash
agentpkg package verify agents/opencode/upstream/1.18.9
```

校验：

- `meta.yaml` 字段
- `manifest.json` schema 合规（[docs/manifest-schema.md](./manifest-schema.md)）
- 重算 `payload/` `files/` `runtime/` 下每个文件 sha256，与 `manifest.checksums` 对比

不匹配则退出非零。

### `package build`

```bash
agentpkg package build agents/opencode/upstream/1.18.9 --out dist
# 产物：
# dist/opencode-upstream-1.18.9.tar.gz
# dist/opencode-upstream-1.18.9.tar.gz.sha256
```

打 tarball 后**自动 `package reindex`** 一次。`--dry-run` 只校验不打包。

### `package reindex`

```bash
agentpkg package reindex --out dist
```

重扫 `dist/*.tar.gz` + `*.tar.gz.sha256`，写 `dist/index.json`。

### `package sign`

```bash
agentpkg package sign dist/opencode-upstream-1.18.9.tar.gz --key <GPG_KEY_ID>
# 产物：
# dist/opencode-upstream-1.18.9.tar.gz.asc   (ASCII-armored detached signature)
```

透传 `gpg --detach-sign --armor`。未提供 `--key` 时使用 GPG 默认 key。

---

## 配置文件 / 凭证文件

`~/.config/agentpkg/config.yaml`：

```yaml
server: "https://marketplace.example.com:8443"
# 可选 —— 由 `agentpkg login --skip-cert-verify` 写入：
skip_cert_verify: true
# 可选 —— 由 `agentpkg login --ca-cert <path>` 写入（绝对路径）：
ca_cert: "/etc/ssl/enterprise-ca.pem"
```

`~/.config/agentpkg/credentials`：

```
password: <your-marketplace-api-password>
```

二者在 `login` 时自动创建；前者 mode `0700`、后者 mode `0600`。

`skip_cert_verify` / `ca_cert` 是可选的 TLS 行为配置，由 `login` 子命令设置并落盘。再次 `login` 时不传对应 flag 会清掉相应字段。`whoami` / `index` / `show` / `download` / `install` / `upgrade` / `rollback` 等所有需要走 marketplace-api 的命令都会自动使用这些设置，**无需在每个子命令上重复传 flag**。

---

## 退出码

| 代码 | 含义 |
|---|---|
| 0 | 成功 |
| 1 | 普通错误 |
| 10/20/30/40/50/60 | 来自 install.sh 的语义退出码（见 [install-protocol.md](./install-protocol.md)） |
| 70 | render-config.sh: 必填输入缺失 |
| 71 | render-config.sh: 脚本错误 |
| 72 | systemd --user 不可用（install 继续但服务没启动） |
| 74 | rollback: state.json 没有 previous 块 |
| 75 | rollback: 目标版本无法解析 |
| 76 | rollback: 下载或 sha256 校验失败 |

---

## 常见工作流

### 一次性登录一台 VM

```bash
agentpkg login --server https://marketplace.example.com:8443 --password-file <(echo "$PW")
agentpkg index
agentpkg show opencode --version 1.18.9
agentpkg install opencode --version 1.18.9
agentpkg whoami    # 确认连通
```

### 打包新版本

```bash
# 1. 准备目录
mkdir -p agents/myagent/upstream/1.0.0
# 2. fetch + 写 manifest + install.sh
./tools/fetch.sh myagent upstream 1.0.0
$EDITOR agents/myagent/upstream/1.0.0/manifest.json
# 3. 校验
agentpkg package verify agents/myagent/upstream/1.0.0
# 4. 打 tarball + 重生 index
agentpkg package build agents/myagent/upstream/1.0.0 --out dist
# 5. 推到镜像
make release-images TAG=v0.1.0-$(date -u +%Y%m%d)
```