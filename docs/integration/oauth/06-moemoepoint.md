# 06 — 萌萌点（moemoepoint）全站统一货币

返回 [README](./README.md)

> 🚧 **状态：设计规范（待实现）**。本文描述的库表、端点、错误码**尚未落地**，是供实现 + 下游接入对齐的最佳实践设计。落地后本横幅删除、补"已上线"标记。

## 0. 决策

**moemoepoint 是全站统一的一种货币**：一个用户在 kungal、moyu、以及未来所有接入 OAuth 的站点**共享同一个余额**。

- **单一真源（single source of truth）= OAuth**（共享身份库 `kun_oauth_admin`）。
- 余额的**任何**增减都必须经过 OAuth 的统一入口，留下**不可篡改的流水**。
- 下游站点**不再各自维护本地 moemoepoint**，改为调用 OAuth 的 service-to-service API 发放 / 扣除，并按需回读余额。

## 1. 为什么这样设计

### 1.1 现状是"脑裂 + 零审计"（要被本方案取代）

| 问题 | 现状 |
|---|---|
| OAuth 的 `users.moemoepoint` | **迁移时的冻结快照**（`migrate-users` 把 kungal+moyu 的值求和写入），之后从不更新 → `/auth/me`、`/profile` 显示的是旧值 |
| 真实经济 | 在**各下游本地** `user.moemoepoint` 原地 `+delta`（moyu 审核通过 +3 等），各站一份、互不相通 |
| 审计 | **完全没有**：原地改列，改完即丢历史，无法回答"这 3 点是怎么来的 / 谁扣的" |

### 1.2 最佳实践：append-only 账本 + 缓存余额

> **余额不是一个可变的整型列，而是一条只追加（append-only）流水的派生值。**

- 每一次增减写一条**不可变**的 ledger 记录（事件溯源）。
- `users.moemoepoint` 降级为**缓存余额**，在**同一事务**内随流水原子更新（既快读又可随时用 `SUM(delta)` 校验）。
- **永不 UPDATE / DELETE 流水**。发错了 → 写一条**反向补偿**记录（`reason=refund`），历史完整。
- 所有写入走**唯一入口** + **幂等键**，天然审计、天然防重放。

---

## 2. 数据模型

### 2.1 流水表 `moemoepoint_ledger`（不可变、只追加）

```sql
CREATE TABLE moemoepoint_ledger (
  id              BIGSERIAL PRIMARY KEY,
  user_id         INTEGER     NOT NULL,              -- OAuth users.id
  delta           INTEGER     NOT NULL,              -- 有符号：+3 / -10（禁止 0）
  balance_after   INTEGER     NOT NULL,              -- 写入后的余额快照（快读 + 自检）
  category        VARCHAR(20) NOT NULL,              -- 闭集枚举，OAuth 拥有，分类查看的轴（见 §3）
  reason          VARCHAR(60) NOT NULL,              -- 细分事件，下游拥有，命名空间 <app>.<event>（见 §3）
  source_app      VARCHAR(32) NOT NULL,              -- 触发方：oauth / kungal / moyu / ...
  ref_type        VARCHAR(40),                       -- 触发实体类型：galgame / patch / comment / checkin / admin ...
  ref_id          VARCHAR(64),                       -- 触发实体 id（可空）
  actor_user_id   INTEGER     NOT NULL DEFAULT 0,    -- 谁导致的：0=系统 / 管理员 id / 用户自己
  idempotency_key VARCHAR(128) NOT NULL,             -- 防重放，全局唯一
  note            VARCHAR(255),                       -- 人类可读备注（管理员操作时填）
  created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX idx_mp_ledger_idem ON moemoepoint_ledger (idempotency_key);
CREATE INDEX        idx_mp_ledger_user ON moemoepoint_ledger (user_id, id DESC);
-- 分类查看：按 category 过滤 / 聚合（用户明细页、运营报表）
CREATE INDEX        idx_mp_ledger_user_cat ON moemoepoint_ledger (user_id, category, id DESC);
```

要点：
- **`idempotency_key` 唯一**是整套设计的地基（见 §5）。
- `balance_after` 不是冗余——它让"第 N 条流水当时的余额"可追溯，也用于一致性自检（任意行 `balance_after` 必须等于该用户截至该行的 `SUM(delta)`）。
- 流水**只追加**：纠错用补偿记录，不改旧行。
- **`category`（闭集）+ `reason`（开放）是两级分类**——这是回答"分类查看"与"下游要不要自报全套枚举"的关键，见 §3。

### 2.2 余额：`users.moemoepoint`（缓存）

- 含义从"权威值"变为"**最新一条流水的 `balance_after` 缓存**"。
- 在**写流水的同一事务**里更新（见 §5 伪代码），永不被流水之外的路径改写。
- 可随时重算校验：`SELECT COALESCE(SUM(delta),0) FROM moemoepoint_ledger WHERE user_id=?` 必须等于 `users.moemoepoint`。

---

## 3. 分类体系：两级（category 闭集 + reason 开放）

直接回答两个问题：

- **"现在已经明确有哪些枚举了吗"** —— **粗粒度的 `category` 现在就定死**（见 §3.1，闭集、少而稳定、OAuth 拥有）；**细粒度的 `reason` 不可能现在穷举**（随业务演进），也**不应该**塞进 OAuth。
- **"是否需要 kungal/moyu 自行列举一套完全的枚举"** —— **不需要在 OAuth 注册完整枚举**（那会让"每加一个 +N 的玩法都要改 OAuth 并发版"，强耦合）。下游**自己拥有并文档化**它的细粒度 `reason`，但必须落进 OAuth 拥有的某个 `category` 桶里。

> 设计参照 Stripe balance transaction：平台拥有一小撮稳定的 `type`（= 我们的 `category`，用于分类/聚合/报表），具体语义放在自由的 `description`/`metadata`（= 我们的 `reason` + `ref`）。

### 3.1 `category`（闭集，OAuth 拥有，"分类查看"的轴）

实现侧用常量校验，未知 → 400。新增 category **极少**发生，且需要 OAuth 改动 + 本文档同步——这正是我们希望它稳定的原因。

| category | 含义 | 典型方向 | 例子 |
|---|---|---|---|
| `contribution` | 产出内容获得的奖励 | + | Wiki 投稿通过、发布补丁、被采纳的编辑 |
| `engagement` | 轻量活跃 | + | 每日签到、评论被赞、连续登录 |
| `penalty` | 违规 / 内容下架的扣除 | − | 内容被删回收积分、违规处罚 |
| `admin` | 人工操作 | ± | 管理员发放 / 扣除 / 手动冲正 |
| `system` | 自动 / 非业务调整 | ± | 迁移起始值、对账冲正、自动补偿 |

**"分类查看"= 按 `category` 过滤 / group-by**（用户积分明细页、运营报表都走这个轴，索引见 §2.1）。因为 category 是闭集，过滤项稳定、UI 不会因下游加玩法而碎裂。

### 3.2 `reason`（开放，下游拥有，细分事件）

- 格式约定：`<source_app>.<event>`，小写 + 下划线，如 `moyu.wiki_galgame_approved`、`kungal.daily_checkin`、`oauth.admin_grant`、`oauth.migration`。
- OAuth **不**维护 reason 的闭集枚举；只校验：① 非空、长度/字符集；② 命名空间前缀 == 已认证的 `source_app`（防止 moyu 伪造 `kungal.*`）。`oauth.*` 仅 OAuth 自身（管理员 / 迁移）可用。
- 下游**新增一个积分玩法 = 自己定一个新的 `reason` 字符串 + 选一个已有 `category`**，无需改 OAuth、无需发版协调。
- 每个下游应在**自己的接入文档**里维护一张"本站 reason 清单"（reason → category、典型 delta、ref 规则），这是该站的产品定义，不是 OAuth 的职责。

### 3.3 起步示例（非闭集，仅示范映射）

| category | reason（示例） | delta | source_app | ref_type / ref_id |
|---|---|---|---|---|
| `contribution` | `moyu.wiki_galgame_approved` | +3 | moyu | galgame / `<gid>` |
| `contribution` | `moyu.patch_published` | +N | moyu | patch / `<patchId>` |
| `engagement` | `kungal.daily_checkin` | +N | kungal | checkin / `<yyyy-mm-dd>` |
| `engagement` | `kungal.comment_liked` | +1 | kungal | comment / `<commentId>` |
| `penalty` | `moyu.content_removed` | −N | moyu | galgame / `<gid>`（与发放同 ref）|
| `admin` | `oauth.admin_grant` / `oauth.admin_deduct` | ±N | oauth | admin / `<adminUserId>` |
| `system` | `oauth.migration` | 任意 | oauth | — |

约定：
- **同一业务事件的发放与回收用相同 `ref` + 对称方向**，便于对账（如 `contribution/moyu.wiki_galgame_approved +3` ↔ galgame 被 ban 时 `penalty/moyu.content_removed -3`，ref 同为 `galgame/<gid>`）。
- `delta` 禁止为 0。

---

## 4. 服务到服务 API

**鉴权**：与 [`/users/batch`](./03-cross-service.md) 相同——**OAuth Client Basic Auth**（`Authorization: Basic base64(client_id:client_secret)`），不是终端用户 JWT。`source_app` 由服务端从认证的 client 推导（不信任请求体里的自报值）。

| 端点 | 方法 | 用途 |
|------|------|------|
| `/users/:id/moemoepoint` | POST | **调整**余额（发放 / 扣除），幂等 |
| `/users/:id/moemoepoint` | GET | 读当前余额 |
| `/users/:id/moemoepoint/ledger` | GET | 分页拉流水（对账 / 用户"积分明细"页）|

> `/users/batch` 刻意**不返回** moemoepoint（隐私 + 易过期）——余额一律走这里的专用 RPC。

### 4.1 POST /users/:id/moemoepoint — 调整余额

**请求体**：

```json
{
  "delta": 3,
  "category": "contribution",
  "reason": "moyu.wiki_galgame_approved",
  "ref_type": "galgame",
  "ref_id": "1207",
  "actor_user_id": 0,
  "idempotency_key": "moyu:wiki_msg:88231",
  "note": ""
}
```

| 字段 | 必填 | 说明 |
|------|------|------|
| delta | 是 | 有符号整数，非 0。正=发放，负=扣除 |
| category | 是 | §3.1 闭集之一（`contribution`/`engagement`/`penalty`/`admin`/`system`）。**分类查看的轴** |
| reason | 是 | 细分事件，`<source_app>.<event>`（§3.2）。前缀须 == 认证的 source_app |
| ref_type / ref_id | 否 | 触发实体（强烈建议填，用于对账 + 幂等键派生）|
| actor_user_id | 否 | 谁导致的，默认 0（系统）。管理员操作填管理员 id |
| idempotency_key | 是 | 全局唯一键，见 §5。**调用方负责生成稳定键** |
| note | 否 | 人类可读备注 |

**成功响应**（首次执行 与 幂等重放 返回一致）：

```json
{
  "code": 0,
  "message": "成功",
  "data": {
    "user_id": 1207,
    "balance": 42,
    "applied": true,
    "ledger_id": 90871
  }
}
```

| 字段 | 说明 |
|------|------|
| balance | 调整后的最新余额 |
| applied | `true`=本次真正写入；`false`=幂等键命中、未重复执行（见 §5）|
| ledger_id | 对应流水行 id（幂等命中时为原流水 id）|

**错误响应**：

| HTTP | code | 触发条件 |
|------|------|----------|
| 400 | 16002 | delta 为 0 / 超出单次上限 |
| 400 | 16003 | category 不在闭集内 |
| 400 | 16005 | reason 格式非法 / 命名空间前缀 ≠ 认证的 source_app |
| 400 | 9 | idempotency_key 缺失 / 超长 |
| 409 | 16004 | idempotency_key 已存在但请求体与原记录不一致（见 §5）|
| 409 | 16001 | 扣除会使余额为负（若启用非负约束，见 §5.3）|
| 404 | 10005 | user 不存在 |
| 401 | 10001/15001/15009 | Basic Auth 缺失 / client 无效 |

### 4.2 GET /users/:id/moemoepoint — 读余额

```json
{ "code": 0, "message": "成功", "data": { "user_id": 1207, "balance": 42 } }
```

### 4.3 GET /users/:id/moemoepoint/ledger — 流水

**查询参数**：

| 参数 | 必填 | 说明 |
|------|------|------|
| limit | 否 | 默认 20，封顶 100 |
| before_id | 否 | 游标，拉更旧的 |
| category | 否 | **分类查看**：只看某个 category（`contribution`/`engagement`/`penalty`/`admin`/`system`）。可逗号分隔多选 |
| source_app | 否 | 只看某个来源站点 |

```json
{
  "code": 0,
  "message": "成功",
  "data": {
    "items": [
      {
        "id": 90871, "delta": 3, "balance_after": 42,
        "category": "contribution", "reason": "moyu.wiki_galgame_approved",
        "source_app": "moyu", "ref_type": "galgame", "ref_id": "1207",
        "actor_user_id": 0, "note": "", "created_at": "2026-05-29T08:00:00Z"
      }
    ],
    "has_more": true
  }
}
```

> "分类查看"在两个层面都成立：用户积分明细页可按 `category` 切 tab；运营报表可 `GROUP BY category`（或 `category, source_app`）做收支结构分析。因为 `category` 是闭集，这些维度永远稳定、不会因下游加玩法而碎裂。

---

## 5. 幂等 · 并发 · 一致性（核心）

### 5.1 幂等键（防重放）

下游的发放往往由**会重试 / 会重放的路径**触发——典型是 moyu 的 cron「Wiki 消息 → +3」。没有幂等就会重复发放。

- 调用方为**每个业务事件**生成**稳定**的 `idempotency_key`，推荐格式 `<app>:<event>:<事件唯一id>`，如 `moyu:wiki_msg:88231`、`kungal:checkin:1207:2026-05-29`。
- 服务端：`idempotency_key` 唯一索引。已存在则**不重复执行**，直接回原结果（`applied:false` + 原 `ledger_id`/最新 `balance`）。
- 已存在但**请求体不一致**（同键不同 delta/reason）→ `409 / 16004`（调用方 bug，必须暴露而非静默）。

### 5.2 并发（同一用户并行调整）

写入在**单事务**内、对该用户**行级加锁**，避免余额竞态：

```text
BEGIN
  -- 幂等短路
  IF EXISTS(ledger WHERE idempotency_key=$k) THEN return 原记录; COMMIT; END
  SELECT moemoepoint FROM users WHERE id=$uid FOR UPDATE   -- 行锁
  newBalance := balance + delta
  IF newBalance < 0 AND 非负约束开启 THEN ROLLBACK; return 16001; END
  INSERT moemoepoint_ledger(..., balance_after=newBalance, idempotency_key=$k)
  UPDATE users SET moemoepoint=newBalance WHERE id=$uid
COMMIT
```

唯一索引 + 行锁双保险：即使两个请求同键并发，唯一索引也会让其中一个 `INSERT` 失败 → 重试走幂等短路。

### 5.3 非负约束（策略，二选一）

- **允许为负**：不限制，扣除即写负 delta（适合"先消费后结算"场景）。
- **禁止为负**（推荐默认）：扣除后余额 < 0 → `409 / 16001`，不写流水。
- 选择写进实现常量，本文档同步标注当前策略。

### 5.4 一致性自检（运维）

定时任务/巡检：对每个用户断言 `users.moemoepoint == SUM(ledger.delta)`，不一致即告警。`balance_after` 链也可做逐行校验。

---

## 6. 管理端（发放 / 扣除 / 查流水）

- 管理员操作**走同一个 Adjust 入口**：`category=admin`、`reason=oauth.admin_grant`/`oauth.admin_deduct`、`actor_user_id=<管理员id>`、`note` 填理由、`idempotency_key` 用 `oauth:admin:<管理员id>:<时间戳或表单token>`。→ **天然进同一审计流水**。
- OAuth admin（`apps/web` 用户管理）增加：① 给用户发放/扣除的弹窗（带理由）；② 用户流水查看（调 §4.3）。
- **不要**给管理员开"直接编辑 moemoepoint 整型"的口子——那会绕过流水，破坏审计闭环。

---

## 7. 迁移：从脑裂到统一（一次性）

目标：把"OAuth 冻结值 + 各 app 本地值"收敛成"OAuth 单一真源 + 完整起始流水"。

1. **冻结写入**：下游各 app 临时停止本地 `moemoepoint` 写入（或进入双写过渡，见下）。
2. **合并**：对每个用户，取各 app 本地值之和作为统一起始余额（与当年 `migrate-users` 的口径一致；若各 app 当前本地值已漂移，以"各 app 现值求和"为准，并在迁移说明里记录口径）。
3. **写起始流水**：为每个用户写**一条** `category=system` + `reason=oauth.migration` 的 ledger（`delta=合并值`、`balance_after=合并值`、`idempotency_key=oauth:migration:v1:<userId>`），并设 `users.moemoepoint=合并值`。幂等键保证迁移脚本可重复跑。
4. **下游切换**：各 app 删除本地 `moemoepoint` 列的写逻辑，改为：
   - 发放/扣除 → 调 §4.1（带稳定幂等键）；
   - 显示余额 → 回读 §4.2，或直接用 `/auth/me` / userinfo 里 OAuth 返回的实时余额（OAuth 端改为返回缓存余额，不再是旧快照）。
5. **过渡期（可选双写）**：切换不能一刀切时，下游可"本地写 + 同时调 OAuth Adjust"，以 OAuth 为准对账；过渡完成后撤掉本地写与本地列。

> 迁移本身就是一条 `migration` 流水——起始值也可追溯，不存在"凭空出现的余额"。

---

## 8. 下游接入指南（kungal / moyu / 未来站点）

| 你现在做的 | 改成 |
|---|---|
| 本地 `UPDATE user SET moemoepoint = moemoepoint + N` | 调 `POST /users/:id/moemoepoint`（Basic Auth + `category` + `reason` + 稳定 idempotency_key）|
| cron 重放发放（如 wiki 审核 +3）| 同上；幂等键用业务事件唯一 id（如 `moyu:wiki_msg:<id>`），**重放安全** |
| 渲染用户余额读本地列 | 读 `/auth/me` / userinfo 的实时余额，或 §4.2；**不要本地缓存权威值**（要缓存就设短 TTL 且明确是缓存）|
| 内容被删时手动扣分 | 用 `penalty` + `<app>.content_removed` + 相同 ref 调 Adjust，便于对账 |

**你需要自己定义并文档化的**：本站的 `reason` 清单（每个积分玩法一条 `<app>.<event>` + 归到哪个 `category` + 典型 delta + ref 规则）。**不需要**在 OAuth 注册——新增玩法只是多一个 `reason` 字符串，挑一个已有 `category`，无需 OAuth 改动 / 发版。你**只**依赖 OAuth 拥有的两样东西：闭集 `category`（§3.1）和命名空间规则（reason 前缀 == 你的 source_app）。

客户端实现：OAuth **不发布 SDK**，每个 consumer 自己写薄客户端（与 `/users/batch` 同样的 Basic Auth；参考 [03-cross-service.md](./03-cross-service.md) 与 migration 接入指南）。

---

## 9. 错误码（16xxx，紧接 OAuth 15xxx）

| code | 常量（建议）| 含义 |
|---|---|---|
| 16001 | `ErrMoemoepointInsufficient` | 扣除会使余额为负（非负约束开启时）|
| 16002 | `ErrMoemoepointInvalidDelta` | delta 为 0 或超出单次上限 |
| 16003 | `ErrMoemoepointInvalidCategory` | category 不在闭集内 |
| 16004 | `ErrMoemoepointIdemConflict` | idempotency_key 已存在但请求体不一致 |
| 16005 | `ErrMoemoepointInvalidReason` | reason 格式非法 / 命名空间前缀 ≠ 认证的 source_app |

完整错误码表见 [04-tokens-and-errors.md](./04-tokens-and-errors.md#错误码速查)。

---

## 10. 安全 / 防刷要点

- **服务端推导 `source_app`**（从认证 client），不信任请求体自报，防止伪造来源。
- **reason 命名空间锁定**：reason 前缀必须 == 认证的 `source_app`（`oauth.*` 仅 OAuth 自身），下游无法伪造别站的 reason、也无法冒充 `oauth.admin_grant` 自行发放。
- **发放权限**（可选收紧）：限定某 client 只能用某些 `category`（如下游 client 不允许提交 `category=admin`/`system` —— 那是 OAuth 自身保留的）。
- **单次 delta 上限** + （可选）**单用户单日累计上限**，防下游 bug 或刷分。
- 所有调整有 `category` + `reason` + `actor_user_id` + `source_app` + `idempotency_key` → 任何一笔都能回答"哪类、什么事、谁、从哪、是不是重复"。
