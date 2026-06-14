# Galgame 用户投稿与审核流程设计

> **更正（2026-06，以代码为准）**：本设计稿的本地 `galgame_stats.wiki_status_snapshot` 列**最终未采用**（forum/patch 均未建此列）。真实下游同步见 moyu `internal/infrastructure/cron/wiki_sync.go`：游标 `cron_state.last_id` + `since_id`、幂等 `wiki_message_processed` 表、`approved` 经 OAuth s2s 发 +3、其余仅发通知。下文 `wiki_status_snapshot` 的 `ALTER` / `UPDATE` 为历史设计，**勿执行**。

> 2026-05-12 — 让 kungal / moyu 的普通用户也能"创建新 galgame"，但走轻量审核闸口；
> 同时把 VNDB 已同步过来的草稿暴露成可直接"认领发布"的库存。

## 1. 设计目标

1. **kungal / moyu 不再依赖 VNDB**：发布 galgame 唯一可信源是 wiki，VNDB 仅作为一次性 ETL 来源（每天 cron 同步成 status=2 草稿）。
2. **普通用户可投稿**：没有 VNDB 编号的国产 / 独立作品也能进库，走"提交-审核-发布"。
3. **审核期不阻塞 UX**：用户提交后立即能在 kungal / moyu 自己的发布页看到（"我"维度可见），审核通过后跨站全局可见。
4. **跨站事件可消费**：wiki 沉淀消息表，kungal / moyu 拉 API 即可得到 "xxx 提交了 / xxx 通过了" 等事件，不需要 webhook 基础设施。
5. **审核通过的 galgame 跨站可见**：approved (`status=0`) 在 wiki 全局可见，kungal、moyu 都能调用同一个 galgame_id。

## 2. 非目标

- 不做 webhook（kungal / moyu 主动拉取就够）
- 不做 admin 队列的工作流引擎（claim / assign / SLA 等先不考虑，单一 pending 列表就行）
- wiki 不持久化"已读"状态，每个消费端各管各
- 不删现有 PR 流程（PR 是"已发布条目的修订请求"，submission 是"新建条目的申请"，两条独立路径）

## 3. status 状态机扩展

现状 3 个 status；本设计扩到 5 个：

| status | 含义 | 谁能列表看到 | 谁能详情看到 | 谁能 batch 拉到 |
|---|---|---|---|---|
| 0 | 已发布 | 所有人 | 所有人 | 所有人 |
| 1 | 封禁 | admin | admin | admin |
| 2 | VNDB 草稿（系统同步） | 仅 admin / 草稿搜索接口 | 任何人（拿到 ID 后） | admin / "认领"前端 |
| **3** | **用户提交，待审核** | admin / 提交者 | admin / 提交者 | admin / 提交者 |
| **4** | **审核拒绝** | admin / 提交者 | admin / 提交者 | admin / 提交者 |

### 状态转换图

```
        ┌─────────────────────────────────────────┐
        │                                         │
   ┌────▼────┐  sync-vndb     ┌──────────┐  claim │
   │ (none)  ├───────────────▶│  2 草稿  ├────────┤
   └─────────┘                └──────────┘        │
        │                                         │
        │ POST /galgame/submit                    │
        │ (普通用户)                              ▼
        │                              ┌──────────────────┐
        │                              │ 0 已发布          │
        │                              │ (跨站全局可见)    │
        │                              └────────▲─────────┘
        │                                       │
        ▼                            approve    │
   ┌──────────┐    edit    ┌──────────┐         │
   │ 3 待审核 │◀───────────┤ 4 已拒绝  │         │
   └────┬─────┘            └──────────┘         │
        │                       ▲                │
        │    decline            │                │
        ├──────────────────────▶┘                │
        │                                        │
        │    approve                             │
        └────────────────────────────────────────┘
        │
        │    admin 直接发布（POST /galgame，admin/moderator only）
        │    旁路：admin/moderator 创建直接 → 0
```

### 转换规则

| 来源 → 目标 | 触发者 | 接口 | 副作用 |
|---|---|---|---|
| (none) → 2 | system | sync-vndb cron | 写 revision (action='created', user=系统) |
| (none) → 0 | admin/moderator | `POST /galgame` | 直接发布（旁路审核） + revision |
| (none) → 3 | 普通用户 | `POST /galgame/submit` | revision + message(type=submitted) |
| 2 → 0 | 任何登录用户 | `POST /galgame/:gid/claim` | user_id 改为认领者 + 加 contributor + revision(action='claimed') |
| 3 → 0 | admin/moderator | `PUT /admin/galgame/:gid/status` | revision(action='approved') + message(type=approved) |
| 3 → 4 | admin/moderator | `PUT /admin/galgame/:gid/status` | revision(action='declined', payload.reason) + message(type=declined) |
| 4 → 3 | 提交者 | `PATCH /galgame/:gid` | revision(action='edited_pending') + message(type=edited_pending) |
| 3 → 3 (data 改) | 提交者 | `PATCH /galgame/:gid` | 同上 |
| 任意 → 1 | admin/moderator | `PUT /admin/galgame/:gid/status` | revision(action='banned') |
| 1 → 0 | admin/moderator | 同上 | revision(action='unbanned') |

## 4. Schema 变更

### 4.1 `galgame.status` 取值扩展

无 DDL 改动，仍是 `INT DEFAULT 0`。代码层面增加 3、4 作为合法值。

### 4.2 `galgame.vndb_id` 改成可选

当前：

```sql
vndb_id VARCHAR(10) UNIQUE NOT NULL
```

改成：

```sql
vndb_id VARCHAR(10) NOT NULL DEFAULT ''
-- 把 UNIQUE 改成 partial unique：空字符串可以重复（多个无 vndb_id 的原创作品）
DROP INDEX uq_galgame_vndb_id;  -- 假设原 index 名
CREATE UNIQUE INDEX uq_galgame_vndb_id_nonempty
    ON galgame(vndb_id) WHERE vndb_id <> '';
```

GORM 模型对应改动：去掉 `uniqueIndex` tag，在 `migrate-galgame` 里 `Exec` 一次 `CREATE UNIQUE INDEX ... WHERE ...`（AutoMigrate 不支持 partial unique）。

### 4.3 `galgame_message` 新表

```sql
CREATE TABLE galgame_message (
    id              BIGSERIAL PRIMARY KEY,
    type            VARCHAR(20) NOT NULL,
        -- 'submitted' | 'approved' | 'declined' | 'edited_pending'
        -- 'banned' | 'unbanned' | 'claimed'
    galgame_id      INT NOT NULL,
    actor_user_id   INT,            -- 触发动作的用户（submitter / admin / claimer）
    target_user_id  INT,            -- 应该被通知的用户（NULL = 仅 admin 队列可见）
    payload         JSONB,          -- decline reason / merged_into 等附加数据
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- 用户视角：拉给当前用户的通知
CREATE INDEX idx_galgame_message_target
    ON galgame_message(target_user_id, id DESC)
    WHERE target_user_id IS NOT NULL;

-- admin 视角：未处理的提交队列。partial index 配合 service 层 join
-- galgame.status 过滤掉已处理项
CREATE INDEX idx_galgame_message_admin_queue
    ON galgame_message(type, id DESC)
    WHERE type = 'submitted';

-- cron feed：按 id 单调递增分页
CREATE INDEX idx_galgame_message_id
    ON galgame_message(id);
```

### 4.4 各类型 payload 约定 + target 归属

| type | actor | target_user_id | payload |
|---|---|---|---|
| `submitted` | submitter | NULL（admin 队列才看） | `{ "vndb_id": "v17 or empty" }` |
| `claimed` | claimer | NULL | `{ "from_status": 2 }` |
| `edited_pending` | submitter | NULL | `{ "field_count": 3 }` |
| `approved` | admin | 当前 owner | `{ "approved_by": <uid>, "note": "" }` |
| `declined` | admin | 当前 owner | `{ "declined_by": <uid>, "reason": "..." }` |
| `banned` | admin | 当前 owner | `{ "banned_by": <uid>, "reason": "..." }` |
| `unbanned` | admin | 当前 owner | `{ "unbanned_by": <uid>, "note": "" }` |

**为什么 banned / unbanned 也带 target**：
1. 用户应该被通知"你的作品被封 / 解封"
2. `/messages/feed` 按 `target_user_id IS NOT NULL` 过滤，让 kungal/moyu cron 拿到这些事件给作者发本地通知，target 必须非空（早期设计想据此同步本地 `wiki_status_snapshot` 列，**已不采用**——实际见 moyu `wiki_sync.go`）

不要 schema validate jsonb，先约定俗成。

## 5. 接口设计

所有路径前缀 `/api`（galgame service `:9280`）。

### 5.1 用户提交（新）

```
POST /api/galgame/submit         认证：Bearer (任意登录用户)
Body: {
  "vndb_id": "v17 或留空",
  "name_zh_cn": "...",
  "name_ja_jp": "...",
  ...其他字段同 CreateGalgameRequest...
  "tag_ids": [...], "official_ids": [...], "engine_ids": [...]
}

Behaviour:
  - vndb_id 非空时，全局唯一性检查（任意 status 已存在 → 20004）
  - 创建 status=3 galgame
  - user_id = JWT.uid
  - 创建 revision 1 (action='created', snapshot)
  - 添加 contributor = submitter
  - 如果有 vndb_id，挂一条 link "VNDB"
  - 写一条 message (type='submitted', actor=submitter, target=NULL)
  - 配额：每用户每日 5 条 (Redis: image:quota 风格)，超出 → 7

Response: { "code": 0, "data": { "id": 10000, "status": 3, ...完整 galgame } }
```

### 5.2 认领草稿（新）

```
POST /api/galgame/:gid/claim     认证：Bearer (任意登录用户)
Body: {} (空)

Behaviour:
  - SELECT galgame WHERE id=? FOR UPDATE
  - 必须 status=2，否则 → 20006 (新错误码：草稿不可认领)
  - UPDATE status=0, user_id=claimer, updated=NOW()
  - INSERT galgame_contributor(galgame_id, user_id=claimer)
  - INSERT galgame_revision(action='claimed', snapshot, payload.from_status=2)
  - INSERT galgame_message(type='claimed', actor=claimer, target=NULL)

Response: 同 5.1，data 是 status=0 后的 galgame
```

### 5.3 编辑自己的待审稿（新）

```
PATCH /api/galgame/:gid          认证：Bearer (限提交者本人)
Body: { ...UpdateGalgameRequest 子集... }

Behaviour:
  - SELECT galgame
  - 必须 status IN (3, 4) AND user_id = JWT.uid，否则 → 20005
  - UPDATE 字段，status 若是 4 → 翻回 3（重入审核队列）
  - 新 revision(action='edited_pending', snapshot)
  - 新 message(type='edited_pending', actor=submitter, target=NULL)

Response: { "code": 0, "data": <更新后 galgame> }
```

> 注意 PUT /galgame/:gid 已有路由（详见 `01-galgame.md`），它是"已发布条目的直接编辑"。
> PATCH 是新增的"草稿编辑"路径。两者语义不同，前者写 action='updated'，后者写 'edited_pending'。

### 5.4 撤回 / 删除自己的待审稿（新）

```
DELETE /api/galgame/:gid          认证：Bearer (限提交者本人)

Behaviour:
  - 必须 status IN (3, 4) AND user_id = JWT.uid
  - 软删（设 status=1 + 加个 'withdrawn' flag）或硬删？
  - 选 **硬删**：DELETE FROM galgame WHERE id=?（CASCADE 清掉关联）
    审核未通过的内容删干净是合理的；revision 记录跟着 CASCADE 走，message
    单独保留（target_user_id 是用户的"我撤回了 X"流水，galgame_id 改为 FK
    on delete SET NULL）。

Response: { "code": 0 }
```

### 5.5 列表/搜索可见性变更

#### `GET /galgame/search`

加可选 query `include_pending=true`：

- 默认（兼容现有）：`status = 0`
- `include_pending=true` 且带 JWT：返 `status = 0 OR (status IN (3,4) AND user_id = JWT.uid)`
- `include_pending=true` 但无 JWT：忽略（按默认走）

Meilisearch 实现：动态构造 filter
```
filter: "status = 0 OR (status IN [3,4] AND user_id = <uid>)"
```

需要把 `user_id` 加进 `galgame_doc.filterableAttributes`（settings.go）。

#### `GET /galgame/batch`

加可选 `viewer_uid` query 或读 Bearer。两者择一，本设计选 **Bearer 优先**：

- 无 Authorization → 现有逻辑（status=0 only）
- 有 Bearer → 解 JWT，按 `status = 0 OR (status IN (3,4) AND user_id = uid)` 过滤

为什么不加新端点：kungal / moyu 的渲染管线已经全走 `/galgame/batch`，加 viewer-aware
能力是兼容增量；调用方不传 Authorization 行为不变。

#### `GET /galgame` (公开列表)

不动，永远只显示 status=0。pending/declined 走 search / mine endpoint。

#### `GET /galgame/mine` （新）

```
GET /api/galgame/mine?status=3,4&page=1&limit=20    认证：Bearer

Behaviour:
  - status query 默认 "3,4"
  - WHERE user_id = JWT.uid AND status IN (...)
  - 给"我的提交"列表用

Response: { "code": 0, "data": { "items": [...], "total": N } }
```

### 5.6 Admin 审核接口扩展

现有 `PUT /admin/galgame/:gid/status`（详见 `06-admin.md`）保持，但行为按目标 status 分支：

```
PUT /api/admin/galgame/:gid/status    认证：Bearer + role admin/moderator
Body: { "status": 0 | 1 | 3 | 4, "reason": "可选" }

注意去掉了 "status=2" — admin 不应该手动把条目改回 VNDB 草稿态，那是
sync-vndb 的专属域。如果要"撤回发布"，应该走 status=1 (ban) 或硬删。

新增的 3 -> 0 / 3 -> 4 转换会自动：
  - 写 revision (action='approved'/'declined', payload.reason)
  - 写 message (type='approved'/'declined', actor=admin, target=submitter)
```

### 5.7 消息接口（新）

```
GET /api/galgame/messages/mine?since_id=0&limit=20    认证：Bearer
  → WHERE target_user_id = JWT.uid AND id > since_id ORDER BY id DESC LIMIT n

GET /api/galgame/messages/feed?since_id=0&limit=1000  认证：OAuth Client Basic Auth
  → WHERE target_user_id IS NOT NULL AND id > since_id ORDER BY id ASC LIMIT n
  → 给 kungal/moyu cron 用，单调递增分页

GET /api/admin/galgame/messages?type=submitted&page=1 认证：Bearer + admin/moderator
  → 默认 type=submitted；可传 csv 'submitted,edited_pending'
  → JOIN galgame ON galgame.id = message.galgame_id WHERE galgame.status IN (3)
  → 这样查询已处理的提交不会污染队列
```

响应统一：

```json
{
  "code": 0,
  "data": {
    "items": [
      {
        "id": 42,
        "type": "approved",
        "galgame_id": 10000,
        "galgame": {
          "id": 10000,
          "name_zh_cn": "...",
          "banner_image_hash": "...",
          "status": 0
        },
        "actor_user_id": 1,
        "target_user_id": 5,
        "payload": { "approved_by": 1 },
        "created_at": "2026-05-12T07:00:00Z"
      }
    ],
    "total": 1
  }
}
```

**注意** message 响应里**内嵌一个 galgame brief**（id + name + banner + status），让消费端
渲染消息卡片时一次调用就够，不必再 batch 拉 galgame。

## 6. 可见性矩阵

| 调用方 | 端点 | 默认行为 | 与 status=3/4 的交互 |
|---|---|---|---|
| 任何客户端 | `GET /galgame/` 列表 | status=0 | 看不到 |
| 任何客户端 | `GET /galgame/:gid` 详情 | 任意 status | 能看到（但 client 自己判断要不要展示） |
| 任何客户端 | `GET /galgame/batch` (无 Bearer) | status=0 | 看不到 |
| 带 JWT | `GET /galgame/batch` (有 Bearer) | status=0 + 自己的 3/4 | 看到自己的 |
| 任何客户端 | `GET /galgame/search` | status=0 | 看不到 |
| 带 JWT | `GET /galgame/search?include_pending=true` | status=0 + 自己的 3/4 | 看到自己的 |
| 带 JWT | `GET /galgame/mine` | 自己的 3/4 | 看到 |
| admin | `GET /admin/galgame?status=3` | 全部 status=3 | 看到 |
| admin | `GET /admin/galgame/messages?type=submitted` | 未处理队列 | 看到 |

> 注意:`GET /galgame/:gid` 详情接口当前**不按 status 过滤**（参见
> `galgame_repository.go:30` 的 FindByID）。pending/declined 的详情能被任何拿到 id
> 的人看到，这是个轻量泄露。本设计选择**保留这个行为**——理由：
>
> 1. id 不可枚举（连续整数但流量低，正常用户不会扫）
> 2. 提交者会把 id 通过 kungal/moyu 的发布页 URL 分享给同伴预览
> 3. 真要避免泄露，应该用单独的 token / 短链方案，不是改详情过滤
>
> 列表 / batch / search 这些"被动列出"的接口必须按 status=0 过滤，因为它们是
> 信息聚合源。详情是"我已经知道 id 我想看"，性质不同。
>
> 如果未来发现这是问题，加一个 detail-level visibility check 不算大改。

## 7. kungal / moyu 接入模式

### 7.1 发布 galgame UI 流程

```
1. 用户输入关键词
2. 前端 → wiki: GET /galgame/search?q=...&include_pending=true (带 access_token)
   返回 { items: [...status=0], pending: [...自己的 3/4] }
3. 前端展示三种结果块：
   - "已发布的相似作品" (items 中 status=0 的)
   - "VNDB 草稿（可一键认领发布）" (items 中 status=2 的)
   - "我已提交的 / 等待审核中" (pending)
   - "都不是？提交新作" (按钮)
4. 用户选择 / 提交：
   a) 选已发布 → kungal 后端直接 INSERT galgame_stats(galgame_id=该 id) + 跳详情
   b) 选 VNDB 草稿 → kungal 后端 POST /galgame/:gid/claim → INSERT stats → 跳详情
   c) 提交新作 → kungal 后端 POST /galgame/submit → 拿到 id → INSERT stats
              （早期设计在本地 galgame_stats 加 wiki_status_snapshot 列缓存状态，
              详见 7.2——**已不采用**，下游不维护本地状态快照列）
```

### 7.2 kungal / moyu 本地 galgame_stats 扩列（早期设计，未采用）

```sql
-- 历史设计，未采用：forum/patch 未建此列（见本文顶部更正），勿执行此 ALTER
ALTER TABLE galgame_stats
    ADD COLUMN wiki_status_snapshot SMALLINT NOT NULL DEFAULT 0;
-- 0=已发布 / 1=banned / 3=pending / 4=declined
-- 5 = 在 wiki 已删除（撤回 / 硬删）—— 本地标记 dead，前端隐藏
```

为什么本地缓存 status：**列表分页时不希望对每条 galgame 都跑一次 wiki batch
看 status 来过滤**。stats 表是 kungal 主键查得很快的本地表，加列缓存最近的 wiki 状态。

> **不缓存** `name` / `banner` / `intro` 等展示字段——那些永远从 `/galgame/batch` 现拉。
> 只有 status 这种"列表过滤需要"的字段才缓存。

### 7.3 cron 同步（早期设计，未采用 — 以代码为准）

> 下方按 `wiki_status_snapshot` 列更新的写法**已不采用**。真实下游同步见 moyu `internal/infrastructure/cron/wiki_sync.go`（另见 [`07-submission.md` 调用方 cron 同步段](../integration/galgame_wiki/07-submission.md)）：游标 `cron_state.last_id` + `since_id`；每条先 `INSERT wiki_message_processed … ON CONFLICT DO NOTHING` 去重；`approved` 经 OAuth s2s 发 +3 并发通知，`declined` / `banned` / `unbanned` 仅发通知；**不写任何本地状态快照列**。

### 7.4 前端通知中心

kungal 前端的消息面板同时拉两个源：

```typescript
const [local, wikiNotif] = await Promise.all([
  $fetch('/api/message'),                    // kungal 本地（reply/like/PR 等）
  $fetch('/api/wiki/messages/mine')          // 透传到 wiki
])
// 按 created_at 合并展示
```

kungal 后端代理 `/api/wiki/messages/mine` → 透传到 `wiki/galgame/messages/mine`。

### 7.5 "已读" 状态

不存在 wiki。kungal / moyu 各自在自己库里维护：

```sql
CREATE TABLE wiki_message_read_state (
    user_id              INT PRIMARY KEY,
    last_read_message_id BIGINT NOT NULL DEFAULT 0,
    updated_at           TIMESTAMPTZ DEFAULT NOW()
);
```

前端打开消息面板时：
- 用 `last_read_message_id` 计算未读数（一次 count）
- 用户关闭面板或点"全部已读"时，PUT 一次更新 max id

## 8. 错误码新增

| code | message | 含义 |
|---|---|---|
| 20006 | 草稿不可认领 | claim 时目标 status ≠ 2 |
| 20007 | 仅提交者可编辑 | PATCH 时不是 user_id 本人 |
| 20008 | 草稿仅可在待审/已拒状态编辑 | PATCH 时 status ∉ {3,4} |
| 20009 | 今日投稿配额已用尽 | submit 超出每日 5 条 |

`ErrGalgameForbidden = 20005` 复用为通用"权限不足"。

## 9. 风险与缓解

| 风险 | 缓解 |
|---|---|
| 用户提交大量垃圾 | submit 每日配额 5 条 (Redis day-window)；admin 一键 ban + 封号 |
| 同一新作并发提交两份 | 提交时 search 已经显示其他 pending；admin 在队列里手动 merge 路径见 §10 |
| 下游本地缓存与 wiki 状态不一致 | 不维护本地状态快照列（早期 `wiki_status_snapshot` 方案未采用）；可见性按本地行 + 详情现拉 |
| 提交者编辑 declined 草稿 → 翻回 pending 死循环 | admin 觉得用户故意刷 → status=1 ban，避免无限审核 |
| message feed 太大 | 每日 cron 拉增量，单次 limit=1000；如果一年内累积 > 100w 行再考虑分表 |
| 详情 ID 枚举泄露 pending 草稿 | 见 §6 注释，接受 |

## 10. Admin 合并两份重复提交（手动流程）

不实现自动 dedupe，admin 看 queue 时人工判断：

1. 看到 A、B 两条 status=3 是同一作品
2. 选其中一条（如 A）通过：approve → status=0
3. 对 B 调 `POST /admin/galgame/:gid/merge-into` （新接口，body `{ target_id: A }`）
   - DELETE galgame B
   - 写 message(type='merged', actor=admin, target=B 的 submitter, payload={merged_into: A})
   - kungal cron 看到 → UPDATE galgame_stats SET galgame_id=A WHERE galgame_id=B
     ↑ 但 galgame_stats 主键是 galgame_id，简单 UPDATE 会撞唯一约束，要：
     `INSERT ... ON CONFLICT (galgame_id) DO UPDATE SET like_count = stats.like_count + EXCLUDED.like_count, ...`

第一期不做 merge-into，admin 先用 `PUT /admin/galgame/:gid/status?status=1` 把 B 封禁，
人工通知用户重投。merge-into 留到 V2。

## 11. 实施清单

按依赖顺序：

1. **schema migration** — `galgame_message` 表 + `galgame.vndb_id` partial unique
2. **model 层** — `model/galgame_message.go`
3. **repository 层** — `message_repository.go` + `galgame_repository` 增加 batch-with-viewer / search-with-pending / mine / submit / claim 方法
4. **service 层** — `submission_service.go` + `message_service.go`，galgame_service 改造（admin approve/decline 写 message + revision action 扩展）
5. **DTO** — `submission_dto.go` + `message_dto.go`；vndb_id 改 omitempty
6. **handler** — `submission_handler.go` + `message_handler.go`
7. **路由** — `cmd/galgame/main.go` 注册 5 个新端点 + batch/search 上 `OptionalJWT`
8. **Meilisearch 索引** — search/doc.go 加 user_id 字段，settings.go 加 filterable

测试覆盖（最少）：
- submit → 检 status=3 + revision + message 各一条
- claim → 检 status 翻转 + user_id 改变 + contributor
- patch declined → 检 status 翻回 3 + 新 message
- admin approve → 检 message.target_user_id = submitter

## 12. 关联文档

- 用户提交流程的 consumer-facing API 详见
  [docs/integration/galgame_wiki/07-submission.md](../integration/galgame_wiki/07-submission.md)
- 消息 API 详见
  [docs/integration/galgame_wiki/08-messages.md](../integration/galgame_wiki/08-messages.md)
- revision 系统底层（不变）
  [01-revision-system-design.md](./01-revision-system-design.md)
