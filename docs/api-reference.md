# marketplace-api — HTTP 接口参考

marketplace-api 是一个**只读**的 HTTP 服务，把 `dist/` 目录暴露成 JSON + tarball API。所有接口（除 `/api/v1/health`）都通过 HTTP Basic Auth 鉴权，密码由 `auth.password_env` 配置指定的环境变量提供（默认 `MARKETPLACE_API_PASSWORD`）。

完整的机器可读规范见 [docs/api/openapi.json](./api/openapi.json)，浏览器可访问 `https://<host>:8443/swagger` 查看 Swagger UI（CDN 加载）。

---

## 接口一览

| 方法 | 路径 | 是否需要认证 | 说明 |
|---|---|---|---|
| `GET` | `/api/v1/health` | ❌ 匿名 | 服务存活探针 |
| `GET` | `/api/v1/index` | ✅ | 列出全部 agent 与版本（stripped，不含完整 manifest） |
| `GET` | `/api/v1/agents/{name}` | ✅ | 查单个 agent 的全部版本与完整 manifest |
| `GET` | `/api/v1/agents/{name}/{source}/{version}/manifest` | ✅ | 查单个 (agent, source, version) 的完整 manifest.json |
| `GET` | `/api/v1/agents/{name}/{source}/{version}/tarball` | ✅ | 下载 tarball 字节流（application/gzip） |
| `GET` | `/api/v1/agents/{name}/{source}/{version}/sha256` | ✅ | 取 sha256 侧车文件（sha256sum 格式） |
| `GET` | `/api/v1/agents/{name}/{source}/{version}/config-schema` | ✅ | 取 schema 1.1+ 的 admin 配置 schema |
| `GET` | `/api/v1/index.json` | ✅ | 原始 dist/index.json（含完整 manifest，用于调试） |
| `GET` | `/swagger` | ❌ 匿名 | Swagger UI HTML |
| `GET` | `/swagger/openapi.json` | ❌ 匿名 | 原始 OpenAPI 3.0 spec |

> **路由顺序保证匿名**：`/api/v1/health`（和两个 swagger 路径）注册在 chi 的 auth group **之前**。改路由顺序会立即被 `TestHealth_NoAuthRequired` 抓住。

---

## 通用约定

### 鉴权

- HTTP Basic Auth，**用户名**仅作展示用途（任何非空字符串），**仅校验密码**。
- 401 响应附带 `WWW-Authenticate: Basic realm="marketplace-api"` 头，符合 RFC 7235。
- 密码常量时间比较（`crypto/subtle.ConstantTimeCompare`），不响应时序旁路。

### 错误信封

所有 4xx/5xx 响应使用统一信封：

```json
{
  "error": {
    "code": "not_found",
    "message": "agent opencode not found",
    "details": { }
  }
}
```

`details` 字段可选。常见 `code`：

| code | HTTP | 含义 |
|---|---|---|
| `unauthorized` | 401 | Basic Auth 缺失或密码错误 |
| `not_found` | 404 | 资源不存在 |
| `bad_request` | 400 | 请求参数非法 |
| `no_index` | 503 | dist/index.json 尚未加载 |
| `internal_error` | 500 | 服务器内部错误 |
| `already_exists` | 409 | skills 上传时 `(name, source, version)` 已存在 —— 版本不可覆盖 |
| `too_large` | 413 | skills 上传超过 50 MiB 上限 |
| `bad_content_type` | 415 | skills 上传未使用 `multipart/form-data` |
| `bad_skill_zip` / `bad_filename` / `name_mismatch` / `version_mismatch` / `invalid_manifest` / `bad_channel` / `missing_file` / `bad_multipart` | 400 | skills 上传各阶段的语义错误 |

---

## Skills 端点

Skills 存储在 `dist/skills/` 与 `dist/skills-index.json`，独立的命名空间 —— 与 agents 的路由、schema、磁盘布局完全分离。详见 [skills-api.md](skills-api.md) 与 [skill-md-schema.md](skill-md-schema.md)。

| 方法 | 路径 | 说明 |
|---|---|---|
| `GET` | `/api/v1/skills[?source=X&channel=Y]` | 列出所有 skill（精简投影）。可选 `?channel` 把每个 skill 投影到该 channel 内最高 semver 版本；可选 `?source` 限定到单一 source。 |
| `GET` | `/api/v1/skills/{source}` | 列出指定 source 下的所有 skill |
| `GET` | `/api/v1/skills/{source}/{name}` | 单个 skill（所有版本，完整 body） |
| `GET` | `/api/v1/skills/{source}/{name}/{version}` | 单个版本（完整 detail） |
| `GET` | `/api/v1/skills/{source}/{name}/{version}/download` | 流式返回 zip（`application/zip`） |
| `GET` | `/api/v1/skills/{source}/{name}/{version}/sha256` | sha256 副文件（`<hex>  <filename>\n`） |
| `GET` | `/api/v1/skills/{source}/{name}/{version}/SKILL.md` | 从 zip 读出的原始 SKILL.md 字节（frontmatter + body，与发布时字节一致） |
| `GET` | `/api/v1/skills-index.json` | 原始 `dist/skills-index.json`（调试用） |
| `POST` | `/api/v1/skills` | 上传一个 skill zip（multipart，`file` 字段） |
| `DELETE` | `/api/v1/skills/{source}/{name}` | 删除指定 source 下的所有版本 |
| `DELETE` | `/api/v1/skills/{source}/{name}/{version}` | 删除指定版本 |

`source` 合法值为 `community` 与 `internal`。channel 合法值为 `stable` / `beta` / `edge` / `internal`，`latest` 是 `stable` 的别名（npm 约定）。

`POST /api/v1/skills` 强制：zip 文件名必须是 `<name>-<source>-<version>.zip`；服务端用文件名作为 `(name, source, version)` 三元组的权威标识，不读取 SKILL.md 内的 `name` 字段。重复上传同一三元组返回 409（不可变 —— 必须 bump version 才能发布补丁）。

详细 status code 列表见 [skills-api.md](skills-api.md)。

---

## 端点详解

### `GET /api/v1/health`

匿名、不消耗密码。供 Docker / k8s 健康探针使用。

**响应 200**

```json
{ "status": "ok", "version": "0.1.0" }
```

**curl 示例**

```bash
curl https://host:8443/api/v1/health
```

### `GET /api/v1/index`

返回 `dist/index.json` 的精简视图（每个 version 不含完整 manifest）。用于「列表页」。

**响应 200**

```json
{
  "generated_at": "2026-07-30T10:00:00Z",
  "schema_version": "1.0",
  "agents": [
    {
      "name": "opencode",
      "display_name": "OpenCode",
      "description": "...",
      "logo": "https://example.com/opencode.svg",
      "category": "code-assistant",
      "tags": ["cli"],
      "versions": [
        {
          "version": "1.18.9",
          "source": "upstream",
          "channel": "stable",
          "released_at": "2026-07-20",
          "tarball": {
            "filename": "opencode-upstream-1.18.9.tar.gz",
            "size_bytes": 14680000,
            "sha256": "sha256:fc78..."
          }
        }
      ]
    }
  ]
}
```

**curl 示例**

```bash
curl -u :"$MARKETPLACE_API_PASSWORD" https://host:8443/api/v1/index
```

### `GET /api/v1/agents/{name}`

返回单个 agent 的全部版本，**包含**完整 manifest。

**响应 200**：见 [`apitypes.Agent`](../../internal/apitypes/index.go)。
**404**：agent 不存在。

```bash
curl -u ":$PW" https://host:8443/api/v1/agents/opencode
```

### `GET /api/v1/agents/{name}/{source}/{version}/manifest`

返回 manifest.json 原文（即 tarball 内嵌的那份）。

```bash
curl -u ":$PW" https://host:8443/api/v1/agents/opencode/upstream/1.18.9/manifest
```

### `GET /api/v1/agents/{name}/{source}/{version}/tarball`

流式返回 tarball 字节。响应头：

- `Content-Type: application/gzip`
- `Content-Disposition: attachment; filename="<tarball>.tar.gz"`

```bash
curl -u ":$PW" -o opencode.tar.gz \
  https://host:8443/api/v1/agents/opencode/upstream/1.18.9/tarball
sha256sum opencode.tar.gz   # 建议与 .sha256 侧车对一下
```

### `GET /api/v1/agents/{name}/{source}/{version}/sha256`

返回 `sha256sum` 兼容格式的侧车文件：

```
<hex>   opencode-upstream-1.18.9.tar.gz
```

可配合 `sha256sum -c` 直接校验：

```bash
curl -u ":$PW" https://host:8443/api/v1/agents/opencode/upstream/1.18.9/sha256 \
  | sha256sum -c   # 注意：sha256sum -c 期望文件名匹配，此用法需先 cd 到正确目录
```

### `GET /api/v1/agents/{name}/{source}/{version}/config-schema`

返回 schema 1.1+ 的 admin 配置 schema，用于上层 daemon/Control UI 在不解析完整 manifest 的情况下生成配置 UI。

```bash
curl -u ":$PW" \
  https://host:8443/api/v1/agents/opencode/upstream/1.18.9/config-schema
```

### `GET /api/v1/index.json`

原始 `dist/index.json`。带完整 manifest 字段，体积大。仅供调试。

### `GET /swagger` / `GET /swagger/openapi.json`

前者返回带 Swagger UI 的 HTML（CDN 加载）；后者返回 OpenAPI 3.0.3 规范本身。两者均匿名。

---

## 启动与服务发现

服务启动时立即加载并校验 `dist/index.json`，校验失败**快速失败**（退出码 1）。校验内容包括：

- 每个 manifest 引用的 tarball 必须存在
- 每个 tarball 的 `.sha256` 侧车必须存在
- 侧车 hex 必须与 manifest 中的 `tarball.sha256` 一致

启动成功后会向 stdout 打一行 INFO 日志：

```
time=... level=INFO msg="dist loaded" agents=3 versions=3 dist_dir=...
```

更多启动细节见 [deploy.md](./deploy.md)。