# Category catalog

`meta.yaml`'s `category` field must be one of these IDs. The marketplace-api and `agentpkg package build` both validate against this list.

| ID | Label (zh) | Description |
|----|-----------|-------------|
| `developer` | 开发者工具 | 代码生成、调试、版本控制等 |
| `chat` | 对话助手 | 通用聊天 / 对话 agent |
| `data` | 数据分析 | 数据处理、报表、可视化 |
| `content` | 内容创作 | 写作、图像、视频生成 |
| `search` | 搜索检索 | Web 搜索、知识库检索 |
| `automation` | 自动化 | 工作流编排、定时任务 |
| `media` | 多媒体 | 图像 / 视频 / 音频处理 |
| `utility` | 通用工具 | 其他辅助类 agent |

## Adding new categories

1. Add the ID to `validCategories` in `internal/manifest/catalogs.go`.
2. Add a row to the table above (label + description).
3. Update the frontend's category filter UI to include the new category.

Don't invent a new category system — extend the list, keep IDs lowercase + kebab-case.