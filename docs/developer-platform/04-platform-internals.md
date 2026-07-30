# 平台内部实现

> 本文承载 §5 数据模型、§6 请求生命周期(中间件链)、§8 缓存、§12 可观测 / 计量。设计与命名约定见 [01-design.md](./01-design.md);认证/分层见 [03](./03-auth-and-tiers.md);迁移与运维见 [07](./07-migration-and-ops.md)。

---

## 5. 数据模型(均在主库 `kun_galgame_infra`)

### 5.1 扩展 `oauth_clients`(沿用 Image*/Artifact* 扩展字段范式)

应用(无论 API-key-only 还是 OAuth2)都是一行 `oauth_clients`,新增。**字段是平台级**(tier/配额对整个开放 API 生效,不按媒介拆——per-面权限走 scope):

```go
// --- Developer platform / NextMoe open API extension fields ---
// OwnerUserID: 第三方开发者应用的拥有者(生态账号)。一方站点 client 为 NULL
// (它们靠 SiteID 归属)。门户的 "我的应用" 按此过滤,也用于管理鉴权。
OwnerUserID    *uint  `gorm:"index" json:"owner_user_id,omitempty"`

DevEnabled     bool   `gorm:"not null;default:false" json:"dev_enabled"`      // 准入 NextMoe 开放 API
DevTier        string `gorm:"size:20;not null;default:'free'" json:"dev_tier"` // free|trusted|internal(D2:tier 授予由平台内部完成;身份/角色沿 IdP 五全局角色,不铸新全局角色)
DevNSFWAllowed bool   `gorm:"not null;default:false" json:"dev_nsfw_allowed"`
// 限流/配额(0 = 用 tier 默认值,见 03-auth-and-tiers.md §7)
DevRatePerMin  int    `gorm:"not null;default:0" json:"dev_rate_per_min"`
DevQuotaDaily  int    `gorm:"not null;default:0" json:"dev_quota_daily"`
```
> scope 直接复用既有 `AllowedScopes` + `CheckScope`,不另起字段。

### 5.2 新表 `developer_api_keys`

```go
type DeveloperAPIKey struct {
    ID          uint       `gorm:"primaryKey" json:"id"`
    ClientID    string     `gorm:"size:50;not null;index" json:"client_id"` // FK oauth_clients.id(= 应用)
    Name        string     `gorm:"size:100;not null" json:"name"`           // 开发者起的标签
    KeyHash     string     `gorm:"size:80;not null;uniqueIndex" json:"-"`   // "sha256:<hex>"
    KeyPrefix   string     `gorm:"size:24;not null;index" json:"key_prefix"`// nm_live_a1b2
    Last4       string     `gorm:"size:4;not null" json:"last4"`
    Scopes      datatypes.JSON `gorm:"type:jsonb" json:"scopes"`            // ⊆ 应用 AllowedScopes
    NSFWAllowed bool       `gorm:"not null;default:false" json:"nsfw_allowed"`
    ExpiresAt   *time.Time `json:"expires_at,omitempty"`  // 轮换宽限/有效期;NULL=不过期
    RevokedAt   *time.Time `json:"revoked_at,omitempty"`  // 吊销即拒
    LastUsedAt  *time.Time `json:"last_used_at,omitempty"`
    CreatedByUserID uint   `gorm:"not null" json:"created_by_user_id"`
    CreatedAt   time.Time  `json:"created_at"`
}
func (DeveloperAPIKey) TableName() string { return "developer_api_keys" }
```
有效性:`active = RevokedAt IS NULL AND (ExpiresAt IS NULL OR ExpiresAt > now())`。

### 5.3 新表 `developer_api_usage`(用量落盘,按日聚合)

```go
type DeveloperAPIUsage struct {
    ID        uint      `gorm:"primaryKey"`
    ClientID  string    `gorm:"size:50;not null;uniqueIndex:idx_usage_day,priority:1"`
    KeyID     uint      `gorm:"not null;uniqueIndex:idx_usage_day,priority:2"` // 0=应用级汇总哨兵
    Face      string    `gorm:"size:40;not null;uniqueIndex:idx_usage_day,priority:3"` // catalog/galgame/galgame_internal[_write|_propose]
    Day       string    `gorm:"size:10;not null;uniqueIndex:idx_usage_day,priority:4"` // YYYY-MM-DD(UTC)
    Count     int64     `gorm:"not null"`
    Status4xx int64     `gorm:"column:status_4xx;not null"`
    Status5xx int64     `gorm:"column:status_5xx;not null"`
    UpdatedAt time.Time
}
```
> 实时计数在 **Redis**(限流/配额计数器),周期 flush 到此表供门户出历史图;`last_used_at` 同理异步回写(每 key 每分钟至多一次)。
> `face` 已是**一等列**(粒度 = (client, key, face, day)),门户能出"按面"曲线;`key_id 0` 是应用级汇总哨兵(不用可空 key_id,避开唯一索引的 NULLs-distinct 语义)。`status_4xx/5xx` 显式列名——GORM 命名策略把 `Status4xx` 蛇形化成 `status4xx`(数字前不加下划线),读写两侧都用 `status_4xx`。

> **留存**:本表只增不减,`prune-developer-usage` 每日 job 删除 `day < 今天−400 天` 的行(400 为拍板值,常量 `DeveloperUsageRetentionDays`)。跨副本单飞由 jobs runner 的按 job 名 advisory lock 提供。

> **迁移**:以上列 + 表都在 `kun_galgame_infra` → `go run ./cmd/migrate`(部署不自动跑,见 [07 §14](./07-migration-and-ops.md))。

---

## 6. 请求生命周期(API 中间件链)

```
Cloudflare(TLS,可能命中边缘缓存→直接返回)
  → Traefik(按 /v1/<face>/ 路由到面服务)
    → 面服务(catalog 或 galgame,同一套中间件):
       1. resolveCredential:  Bearer key/JWT → app + scopes + tier + nsfw_allowed
                              (JWT 本地验;API key 经 introspection + Redis 缓存)
                              失败 → 401
       2. rateLimit(Redis,滑动窗口,key=app/key,跨面共享计数) → 超 → 429 + Retry-After + X-RateLimit-*
       3. quota(Redis 当日计数器,跨面共享) → 超 → 429 + X-Quota-*
       4. requireScope(端点所需 scope ⊆ 凭证 scope) → 缺 → 403
       5. content_limit 闸:默认 sfw;请求 nsfw 需 galgame:nsfw scope + nsfw_allowed,否则降级为 sfw 或 403
       6. handler(命中 ETag → 304;否则查询 + 设缓存头)
       7. async:Redis incr 用量 + last_used_at;(周期 flush 落库)
```

伪代码(中间件):
```go
func OpenAPIAuth(c fiber.Ctx) error {
    cred, err := resolveCredential(c)            // API key(introspect+cache) 或 JWT
    if err != nil { return resp401(c) }
    if !allowRate(cred) { return resp429(c, retryAfter) }   // Redis 滑窗
    if !allowQuota(cred) { return resp429Quota(c) }         // Redis 日计数
    c.Locals("cred", cred)
    return c.Next()
}
// handler 内:requireScope("galgame:read"); content_limit = gate(c.Query("content_limit"), cred)
```

---

## 8. 缓存(公开读的承重墙)

**关键设计:把"鉴权"与"响应内容"解耦,让公开读对 Cloudflare 可缓存。**

- 同一 `content_limit` + 同一版本下,公开目录读的**响应内容对所有调用者相同**(与是哪把 key 无关)。鉴权只用于**限流/计量**,不改变响应体。
- 因此缓存键 = `(path, query, content_limit, /v1)`,**不含 key**;响应可带 `Cache-Control: public, s-maxage=…`,被 Cloudflare 边缘共享缓存。
- 把 calendar 已验证的模式铺到两个面的热路径(galgame list/detail/batch/官方成员;catalog works/persons/labels 详情):
  - 弱 **ETag**(嵌 `max(updated)` 或资源指纹)→ `If-None-Match` 命中回 304。
  - `Cache-Control`:历史/稳定数据 `s-maxage` 长(如 1 天),易变的短(如 5 分钟);`max-age=0` 让浏览器每次回源校验。
  - `Cache-Tag`(Cloudflare Cache Rules / 按内容键)便于精准失效。
- 鉴权失败 / 配额头等**不可缓存**部分,仍在回源层处理(CF 仅缓存 2xx 公开读)。
- catalog 面的 **301 redirect 响应同样可缓存**(旧 ID → canonical 是永久事实)。
- **备注(吸收自 API 设计 skill,用在对的地方)**:`GET /v1/galgame` 列表在 6 万→10 万+ 目录上,offset 深翻页有性能悬崖 → 公开列表改 **游标分页**(`cursor`/`next_cursor`),既稳又对缓存友好。(该面已于 2026-07-30 摘牌,但结论**原样适用**于接棒的 `GET /v1/catalog/works`,后者本就是 keyset 分页。)

> 这一节是"开放 API 代价可控"的核心:做好缓存 + CF,绝大多数公开读在边缘命中,回源服务不被打爆。

---

## 12. 可观测 / 计量

- Redis 实时计数(限流/配额)→ 周期 flush `developer_api_usage` → 门户曲线 + 配额执行 + 告警。
- 每请求:`last_used_at` 异步回写;按 (client, key, face, day) 聚合 count/4xx/5xx。
- **账户级 `GET /dev/usage?days=N`**(user-JWT,owner-guarded;`OwnerUsageSummary`)一次返回:
  - `daily[]`——窗口内稠密日序列(缺口补 0,老→新),供柱状图;`total_count/4xx/5xx` 为窗口合计。
  - `by_app[]`——每应用合计(按量降序);`by_face[]`——每 face 合计 `{ face, count, status_4xx, status_5xx }`(按量降序)。以上皆读 `developer_api_usage` rollup。
  - `live[]`——**实时剩余**(章程 05 §9 的账户级兑现):owner 每把 active key 一行 `{ app_name, key_id, rate_limit, quota_limit, quota_used, quota_remaining, quota_reset }`。**直接读 Redis 执法计数器**(`quota:{key_id}:{UTC日}`,与限流/配额同源,不从 rollup 估算);`quota_reset` = 下个 UTC 零点的 epoch 秒。Redis 不可达时 `live` 为空数组并加 `live_unavailable: true`(读面降级,绝不 5xx)。
- **留存**:`prune-developer-usage` 每日 job 修剪 `developer_api_usage`(见 [§5.3](#53-新表-developer_api_usage用量落盘按日聚合))。
