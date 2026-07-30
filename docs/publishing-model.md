# 发布模型与信任级别（publishing-model）

本文说明打包、签名、分发、消费的整套信任模型。

---

## 信任的四个环节

```
   author          marketplace-api          agentpkg
   ─────           ────────────────         ────────
 1. pack  ──→ dist/<n>-<src>-<v>.tar.gz ──→ index
 2. sign  ──→ dist/<n>-<src>-<v>.tar.gz.sig (GPG detached)
 3. publish ──→ CDN / 镜像
 4. consume ──→ sha256 + GPG verify
```

每一环节都对应一种信任强度，下面从弱到强列。

---

## 等级 0：仅 sha256（最低信任）

- 不签名；只发布 `.tar.gz` + `.tar.gz.sha256`
- 消费者跑 `sha256sum -c <sidecar>` 来防意外损坏 / 字节级篡改
- **不能** 抵御：恶意 CDN / 中间人替换整个 tarball
- 适用：内网 / 测试 / 一次性试用

```bash
# consumer side
curl -fSLO https://cdn/x.tar.gz
curl -fSLO https://cdn/x.tar.gz.sha256
sha256sum -c x.tar.gz.sha256
```

---

## 等级 1：sha256 + GPG detached signature

- 多一步 `agentpkg package sign dist/<x>.tar.gz --key <KEY_ID>`，生成 `*.tar.gz.asc`
- 消费者用作者公钥验证：

```bash
gpg --verify x.tar.gz.asc x.tar.gz
```

- **能** 抵御：CDN 替换整个 tarball（攻击者没法伪造 GPG 签名）
- **不能** 抵御：作者本身的 key 不可信 / 公钥分发链被攻破
- 适用：对外发布的稳定版本

`package sign` 在源码里只是 `gpg --detach-sign --armor` 的封装，作者自行管 key。

---

## 等级 2：TLS + 隔离的 CDN

- 整个 CDN 服务端跑 TLS（市场 API 默认 8443 HTTPS；自签或 CA 签）
- 把证书 / 私钥放在能控制的主机上（自有 CDN、或 marketplace-api 自带 HTTPS）
- 在公司内网：可以直接用 self-signed + 客户端 trust 内部 CA

注意：`marketplace-api` 的 TLS 是**可选**的。如果你在前面放了 nginx / caddy 反代，可以把 `tls_cert`/`tls_key` 留空，反代负责 HTTPS。

---

## 等级 3：私钥签 + 受控 CDN + 内部可信源

最高强度：上述三段都做，且：

- CDN 只对公司网络开放（VPN / 专线）
- author key 在硬件 token（如 YubiKey）上，离线保管
- `index.json` 也由 maintainer 签（确保 listing 不被中间人改）

`marketplace-api` 没有提供 index 的签名验证。如果需要，建议在 `agentpkg login` / `agentpkg index` 之间加一个 `agentpkg verify-index` 步骤（手动 `gpg --verify dist/index.json.sig dist/index.json`），目前尚未自动。

---

## dist/ 与 index.json 的一致性

`marketplace-api` 启动时校验：

- 每个 manifest 引用的 tarball 文件确实存在
- 每个 tarball 的 `.sha256` 侧车存在
- 侧车 hex 与 manifest 中的 `tarball.sha256` 匹配

校验失败**立即退出**，不会启动。这是等级 0/1 的服务端兜底。

---

## 发布流程（推荐）

```bash
# 1. 打包 + 自动 reindex
agentpkg package build agents/<n>/upstream/<v> --out dist

# 2. 签名（可选但强烈建议）
agentpkg package sign dist/<n>-<src>-<v>.tar.gz --key release-key@example.com

# 3. 推到 CDN
./tools/publish.sh dist/<n>-<src>-<v>.tar.gz --target s3://my-cdn/agents/
./tools/publish.sh dist/<n>-<src>-<v>.tar.gz.asc --target s3://my-cdn/agents/
./tools/publish.sh dist/index.json                --target s3://my-cdn/agents/

# 4. 让市场 API 读到新 dist
#   - docker: docker compose restart
#   - systemd: systemctl restart marketplace-api
```

---

## 工具脚本

| 脚本 | 用途 | 状态 |
|---|---|---|
| `tools/pack.sh` | shell 版本的 pack | 维护中（等价 `agentpkg package build`） |
| `tools/fetch.sh` | 拉上游 artifact 到 `payload/` | 维护中 |
| `tools/verify.sh` | shell 版本的 verify | 维护中（等价 `agentpkg package verify`） |
| `tools/sign.sh` | shell 版本的 sign | 维护中（等价 `agentpkg package sign`） |
| `tools/publish.sh` | 上传到 CDN | **用户自行实现**（按你用的 S3 / OSS / 自建 HTTP 决定） |

新代码应使用 Go 版本（`agentpkg package ...`）；shell 脚本保留仅作过渡。