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

降级是一等公民路径，由 `agentpkg rollback <agent>` 提供：

```bash
# 默认回滚到 state.json.previous 记录的版本
agentpkg rollback opencode

# 显式多步回滚
agentpkg rollback opencode --to-version 1.18.5 --to-source upstream
```

详见下文「回滚（rollback）」段。注意降级和回滚在语义上不等价：降级指任何从高版本往低版本的方向，回滚特指基于 `state.json.previous` 的一键撤销。

---

## 回滚（rollback）

`agentpkg rollback <agent>` 读 `state.json.previous` 拿到前一个版本，然后：

1. 解析 + 下载（或命中 cache）目标 tarball
2. 在目标 tarball 内探测可选的 `migrate/to-<from>.sh`（与 `from-<old>.sh` 对称）
3. 若有，先跑它（best-effort；失败 WARN 继续）
4. 跑目标版本的 `install.sh`
5. 写 systemd `--user` units + 同步 state.json

默认目标版本 = `state.json.previous.version`；`--to-version` / `--to-source` 用于多步回滚。

退出码：

| 退出码 | 含义 |
|---|---|
| 0 | 成功 |
| 74 | state.json 缺失或没有 previous 块 |
| 75 | 目标版本无法解析 |
| 76 | 下载或 sha256 校验失败 |

---

## 反向迁移脚本约定：`migrate/to-<from>.sh`

当目标版本 tarball 提供 `migrate/to-<from>.sh` 时，agentpkg 在 `install.sh` 之前执行它（仅在 rollback 路径）。

文件名约定：

```
<version>/migrate/to-<from-version>.sh     # 精确匹配（tools/pack.sh 默认布局）
migrate/to-<from-version>.sh               # 根布局 fallback
```

`<from-version>` = **当前装在机器上的版本**（即回滚前的版本），不是要回滚到的目标。

环境变量：

| 变量 | 说明 |
|---|---|
| `AGENT_MARKETPLACE_TARGET_ROOT` | 同 from- 脚本 |
| `AGENT_MARKETPLACE_TARGET_VERSION` | 回滚到的版本号（脚本能据此判断细节） |
| `AGENT_MARKETPLACE_DEPLOY_ROOT` | 当前 deploy_root（被替换的那个） |

语义与 `from-<old>.sh` 完全镜像：幂等、失败仅 WARN、不中断 install。

对称性表：

| 场景 | 源 tarball | 目标 tarball | 脚本路径 |
|---|---|---|---|
| 升级 1.18.5 → 1.18.9 | 1.18.5（已在） | 1.18.9（新装） | `1.18.9/migrate/from-1.18.5.sh` |
| 回滚 1.18.9 → 1.18.5 | 1.18.9（已在） | 1.18.5（新装） | `1.18.5/migrate/to-1.18.9.sh` |

---

## 卸载与清理

详见 [install-protocol.md § uninstall](./install-protocol.md#uninstall-1)。要点：

- `agentpkg uninstall` 先于 `uninstall.sh` 禁用并删除 systemd units
- `uninstall.sh` 按 `installed_files` 精确删除，同时向上 rmdir 至 `$TARGET_ROOT`（不会越过）
- `state.json` 一并删除

---

## 失败与回滚

当一次升级 / 部署出错时，使用 `agentpkg rollback <agent>` 回滚到 `state.json.previous` 记录的版本。详见上文「回滚」段。

agentpkg 不读取 stdin、不阻塞；编排器在收到非零退出码时把 stderr 推给前端即可。