# 升级协议（upgrade-protocol）

本文说明当已安装一个 agent 的旧版本、想装新版本时整套流程如何衔接。整个流程由 `agentpkg upgrade`（或 `agentpkg install` 命中已有 `state.json` 时）驱动，由 `install.sh` + `migrate/*.sh` 落地。

---

## 总览

```
agentpkg upgrade <agent> --version <new>
   │
   ├── 1. 探测：读 $TARGET_ROOT/state/<agent>.state.json → 拿到 current version/source/channel
   │
   ├── 2. 下载新 tarball + sha256 侧车 → 校验
   │
   ├── 3. 解压 tarball，进入 install.sh 执行
   │
   │      install.sh 看到 state.json → 走「upgrade」分支
   │         │
   │         ├── 3a. 跑 migrate 脚本（按优先级匹配，见下）
   │         ├── 3b. 部署新 payload / runtime / files
   │         ├── 3c. 重新写 state.json（installed_files / installed_version / released_at 等）
   │         └── 3d. 用 <agent> --version 验证
   │
   ├── 4. agentpkg 重新读 manifest → 写/更新 systemd --user units
   │       把 services / configs 同步进 state.json
   │
   └── 5. systemctl --user enable --now（每个 service）；started=true 写回 state
```

如果 `state.json` 不存在或被识别为「不同 deployment」，`install.sh` 直接走 fresh install，不做迁移。

---

## 优先级匹配迁移脚本

`install.sh` 在 `agents/<n>/<src>/<v>/migrate/` 下查找迁移脚本，匹配顺序：

1. **精确匹配** `from-<installed-version>.sh` —— 只对老版本生效
2. **major.minor 通配** `from-<major>.<minor>.x.sh` —— 同 major 同 minor 的所有老版本
3. **major 通配** `from-<major>.x.x.sh` —— 同 major 的所有老版本
4. **fallback**：什么都不跑（假设二进制兼容；只在显式注明时进 main 分支）

脚本环境变量：

| 变量 | 说明 |
|---|---|
| `AGENT_MARKETPLACE_TARGET_ROOT` | 写到这 |
| `AGENT_MARKETPLACE_PREVIOUS_VERSION` | 老版本号，便于脚本判断细节 |
| `AGENT_MARKETPLACE_DEPLOY_ROOT` | 老 deploy 根（脚本可能想读老 config） |

迁移脚本**必须**幂等（重跑结果相同），**必须** exit 0 / 非零退出时**不**中断整个 install——迁移失败仅 WARN，install 继续。

---

## `state.json.previous` 字段

schema 1.1 起，`state.json` 增加 `previous`：

```json
{
  "agent": "opencode",
  "source": "upstream",
  "version": "1.18.9",
  "previous": {
    "version": "1.18.5",
    "source": "upstream"
  },
  ...
}
```

agentpkg 在升级动作发生时设置；install.sh 也可写。`uninstall.sh` 不读这个字段，但 `agentpkg uninstall` 知道 `previous` 后可以选择性保留老配置。

---

## `compatible_from` 声明

manifest 里 `upgrade.compatible_from[]` 列出本版本兼容的老版本号。agentpkg install 在做升级前会做 sanity check：

- 已在的版本不在 `compatible_from` 列表里 → WARN，但不阻止（agent 自己的判断）

示例：

```json
{
  "upgrade": {
    "strategy": "in-place",
    "compatible_from": ["1.18.5", "1.18.6", "1.18.7", "1.18.8"],
    "migrations": [
      { "from": "1.18.5", "script": "from-1.18.5.sh" }
    ]
  }
}
```

---

## 降级（downgrade）

默认**允许**。`install.sh` 不区分新旧，覆盖安装。agentpkg 也不阻止。和升级走同一路径（`state.json` 探测 → migrate 匹配），但因为新版本更低，匹配脚本可能没有。

---

## 卸载与清理

详见 [install-protocol.md § uninstall](./install-protocol.md#uninstall-1)。要点：

- `agentpkg uninstall` 先于 `uninstall.sh` 禁用并删除 systemd units
- `uninstall.sh` 按 `installed_files` 精确删除，同时向上 rmdir 至 `$TARGET_ROOT`（不会越过）
- `state.json` 一并删除

---

## 失败与回滚

marketplace-api 当前不提供自动 rollback。需要手动：

```bash
# 1. 回到旧版本号
agentpkg install <agent> --version <old>

# 2. 或者直接清理 + 重装
agentpkg uninstall <agent>
agentpkg install <agent>
```