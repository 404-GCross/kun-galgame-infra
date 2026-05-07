> [📖 文档索引](./README.md) · 上一节：[06 — 管理统计](./06-admin.md)

## 错误码

### Galgame (20xxx)

| Code | 消息 | 说明 |
|------|------|------|
| 20001 | Galgame 不存在 | ID 不存在或已被封禁 |
| 20002 | Galgame 已存在 | — |
| 20003 | 无效的 VNDB ID | 格式不匹配 `v\d+` |
| 20004 | 该 VNDB ID 的 Galgame 已存在 | VNDB ID 重复 |
| 20005 | 无权操作此 Galgame | 非创建者且非 admin |

### 通用

| Code | 消息 |
|------|------|
| 1 | 请求格式错误 |
| 2 | 无效的 ID |
| 4 | 资源不存在 |
| 5 | 访问被拒绝 |
| 7 | 参数验证失败 |
| 10 | 操作失败 |
| 10001 | 未授权 |
| 10002 | 无效的令牌 |
| 10003 | 令牌已过期 |

---

## 端点总览

| 模块 | 方法 | 路径 | 认证 | 数量 |
|------|------|------|------|------|
| **Galgame** | GET | `/galgame`, `/galgame/search`, `/galgame/batch`, `/galgame/check`, `/galgame/user/:uid/stats`, `/galgame/:gid` | 公开 | 6 |
| | POST/PUT | `/galgame`, `/galgame/:gid` | Bearer | 2 |
| **Revision** | GET | `/galgame/:gid/revisions`, `.../:rev`, `.../:rev/diff` | 公开 | 3 |
| | POST | `/galgame/:gid/revert` | Bearer | 1 |
| **PR** | GET | `/galgame/:gid/prs`, `.../:id` | 公开 | 2 |
| | POST/PUT | `/galgame/:gid/prs`, `.../merge`, `.../decline` | Bearer | 3 |
| **Link** | GET/POST/DELETE | `/galgame/:gid/links` | 读公开，写Bearer | 3 |
| **Alias** | GET/POST/DELETE | `/galgame/:gid/aliases` | 读公开，写Bearer | 3 |
| **Contributor** | GET/DELETE | `/galgame/:gid/contributors` | 读公开，删Bearer | 2 |
| **Tag** | GET | `/tag`, `/tag/search` (MS), `/tag/multi`, `/tag/:name` | 公开 | 4 |
| | PUT | `/tag` | admin/mod | 1 |
| **Official** | GET | `/official`, `/official/search` (MS), `/official/:name` | 公开 | 3 |
| | PUT | `/official` | admin/mod | 1 |
| **Engine** | GET | `/engine`, `/engine/:name` | 公开 | 2 |
| | PUT | `/engine` | admin/mod | 1 |
| **Series** | GET | `/series`, `/series/search`, `/series/:id` | 公开 | 3 |
| | POST/PUT/DELETE | `/series`, `/series/modal`, `/series/:id` | Bearer/admin | 4 |
| **Admin** | GET | `/admin/stats`, `/admin/galgame`, `/admin/galgame/:gid` | Bearer | 3 |
| | PUT | `/admin/galgame/:gid/status` | Bearer | 1 |
| | | | **总计** | **54** |

> **标注 (MS) = Meilisearch 驱动**；其余 search 端点（如 `/series/search`）仍基于 Postgres。

---

## 附录：Meilisearch 运维

- **部署**：生产环境运行一个 Meilisearch 实例，通过 `KUN_MEILISEARCH_HOST` 注入到 wiki 服务
- **Index 前缀**：生产无前缀（`galgames` / `galgame_tags` / `galgame_officials`）；开发/测试可设 `KUN_MEILISEARCH_INDEX_PREFIX=dev_` 避免污染
- **启动自愈**：wiki 服务 `cmd/galgame` 启动时自动 `EnsureIndexes`（创建 index + patch settings），不推送文档
- **写入同步**：创建/编辑 galgame、tag、official 时走 write-through goroutine 更新索引；失败只 log，由下方重建兜底
- **全量重建**：`go run ./cmd/reindex-search [--index=galgames,tags,officials] [--batch=1000]`
  - 首次部署必跑
  - `sync-vndb` / `migrate-*` / 批量脚本后必跑（这些脚本不走 write-through）
  - 建议每周低峰期 cron 跑一次对抗漂移
- **索引 settings 变更**：改 `internal/platform/galgame/search/settings.go` 重启服务即生效；若影响已有文档解析，再跑一次 `reindex-search`
