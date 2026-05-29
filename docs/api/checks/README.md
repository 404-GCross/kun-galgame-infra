# API 字段对齐审计

> 目的：逐服务记录全部 API 端点 + FE↔BE 字段对齐审计状态。
>
> 当前进度：**全方法 inventory 阶段** —— 已列出全部端点（共 **147**：GET 75 / POST 42 / PUT 12 / DELETE 14 / PATCH 4），状态默认 ⏳（待审计）。逐项字段对齐审计待后续。

## 端点矩阵（按服务 × 方法）

| 服务 | 二进制 | Base URL | GET | POST | PUT | DELETE | PATCH | 小计 |
|---|---|---|---|---|---|---|---|---|
| OAuth | `cmd/oauth` | `/api/v1` | [19](./oauth.get.md) | [21](./oauth.post.md) | [4](./oauth.put.md) | [3](./oauth.delete.md) | [2](./oauth.patch.md) | 49 |
| Image | `cmd/image`（+管理端在 oauth 进程）| `/`、`/api/v1/admin/image` | [6](./image.get.md) | [2](./image.post.md) | — | [2](./image.delete.md) | [1](./image.patch.md) | 11 |
| Galgame Wiki | `cmd/galgame` | `/api` | [42](./galgame.get.md) | [17](./galgame.post.md) | [8](./galgame.put.md) | [8](./galgame.delete.md) | [1](./galgame.patch.md) | 76 |
| Moderation | `cmd/moderation` | `/api/v1` | [4](./moderation.get.md) | [1](./moderation.post.md) | — | — | — | 5 |
| Artifact | `cmd/artifact` | `/api/v1` | [4](./artifact.get.md) | [1](./artifact.post.md) | — | [1](./artifact.delete.md) | — | 6 |
| **合计** | | | **75** | **42** | **12** | **14** | **4** | **147** |

> 注：`/api/v1/admin/image/*`、`/api/v1/admin/jobs/*` 物理上跑在 oauth 进程（admin 鉴权在那边）。image 管理端归到 image.* 审计；jobs 管理端归到 oauth.* 审计。上表 Image 的 DELETE/PATCH 各含 1 个 oauth 进程内的管理端端点。

## 共用图例

**审计状态**：✅ 对齐无问题 · 🔧 已修 · ⏭️ 有意保持 · ⏳ 待审计

**鉴权**：🌐 公开 · 🔐 OptionalJWT · 🔒 登录必需 · 🛡️ admin/moderator · ⚙️ admin · 🔑 OAuth Client Basic Auth（服务到服务）

> 鉴权细节差异：oauth 的 `Auth` 每次查 DB 用户状态（封禁/匿名化即拒）；galgame/moderation/artifact 的 `JWTAuth` 仅验签。各文件图例有标注。
