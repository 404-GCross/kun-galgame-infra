# 下游服务接入 OAuth 用户系统

> 给 kungal / moyu / galgame_wiki 后端开发看的接入指南。**核心原则**：迁移完毕后，所有用户展示字段（name、avatar、bio）都从 OAuth 拉取，本站不再持久化这些字段。

## 1. 背景与决策

迁移之前：每个站点有自己的 `user` 表，包含 name / avatar / bio 等字段。

迁移之后：

- OAuth `users` 表是**唯一的**身份字段持有者
- 站点的 user 表保留，但**只剩站点特有字段**（daily_check_in、moemoepoint、role、last_login_time 等）
- name / avatar / bio / status / email 这些都不在站点本地存

为什么这么设计：详见 [01-architecture.md](./01-architecture.md) 第 5 节"替代方案对比"。

## 2. 渲染管线的改造模式

### 改造前（站点本地有完整 user 表）

```sql
-- 渲染评论列表
SELECT
  c.id, c.content, c.created_at,
  u.id, u.name, u.avatar, u.bio
FROM galgame_comment c
JOIN "user" u ON u.id = c.user_id
WHERE c.galgame_id = ?;
```

### 改造后（OAuth 是唯一名 / 头像源）

```sql
-- 步骤 1: SELECT 只取 user_id
SELECT
  c.id, c.content, c.created_at, c.user_id
FROM galgame_comment c
WHERE c.galgame_id = ?;
```

```go
// 步骤 2: 收集所有 user_id（去重）
ids := []uint{}
for _, c := range comments {
    ids = append(ids, c.UserID)
}

// 步骤 3: 一次批量回拉
users, err := userClient.Users(ctx, ids)
// users[uid] → *UserBrief{ID, UUID, Name, Avatar, Bio, Status, Roles, ...}

// 步骤 4: 在响应 DTO 里拼装
type CommentResponse struct {
    ID        uint        `json:"id"`
    Content   string      `json:"content"`
    CreatedAt time.Time   `json:"created_at"`
    User      *UserBrief  `json:"user"`
}

resp := make([]CommentResponse, 0, len(comments))
for _, c := range comments {
    resp = append(resp, CommentResponse{
        ID:        c.ID,
        Content:   c.Content,
        CreatedAt: c.CreatedAt,
        User:      users[c.UserID],
    })
}
```

**关键**：DB 查询不再 JOIN user 表（也根本没法 JOIN，那些字段不存在了）。展示字段全部走 SDK。

## 3. 三个 OAuth API + 一个 SDK

### 3.1 `GET /users/batch` —— 批量拉用户 brief

适用场景：渲染列表、需要把一组 user_id 解析成展示字段。

```http
GET /api/v1/users/batch?ids=1,2,3
Authorization: Basic base64(client_id:client_secret)
```

响应：

```json
{
  "code": 0,
  "data": {
    "users": [
      { "id": 1, "uuid": "...", "name": "kun", "avatar": "...", "bio": "...", "status": 0, "roles": ["admin"] }
    ],
    "not_found": [99]
  }
}
```

- 单次最多 100 个 ID
- 鉴权是 OAuth Client Basic Auth（client_id + client_secret），不是终端用户 JWT —— 这是服务到服务调用
- 响应**不含** email / moemoepoint / created_at 等隐私 / 非展示字段

### 3.2 `GET /users/search` —— 按名字搜索

适用场景：@ 提及自动补全、用户搜索框、管理后台用户查找。

```http
GET /api/v1/users/search?q=kun&limit=10
Authorization: Basic base64(client_id:client_secret)
```

响应（按相关度排序：精确 > 前缀 > 子串，每档内字母序）：

```json
{
  "code": 0,
  "data": {
    "users": [
      { "id": 1, "uuid": "...", "name": "kun", ... },
      { "id": 5894, "uuid": "...", "name": "kun123", ... }
    ]
  }
}
```

- `q`：1..50 字符；`%` `_` `\` 等 LIKE 通配符按字面匹配（已转义）
- `limit`：默认 20，封顶 50
- 同样 Basic Auth
- 同样不含隐私字段

### 3.3 `GET /oauth/userinfo` —— 当前登录用户信息

适用场景：OAuth callback 中拿用户身份；前端 /me 端点。

```http
GET /api/v1/oauth/userinfo
Authorization: Bearer <access_token>
```

响应：

```json
{
  "code": 0,
  "data": {
    "id": 12345,
    "sub": "550e8400-e29b-41d4-a716-446655440000",
    "name": "KUN",
    "email": "kun@kungal.com",
    "picture": "...",
    "roles": ["user", "admin"],
    "updated_at": 1234567890
  }
}
```

- `id`：integer，与 OAuth `users.id` 一致 —— **本地 user 表的 PK 应该用这个**
- `sub`：UUID，OIDC 标准的 subject
- `id` 和 `sub` 都标识同一用户，业务后端任选其一
- `roles` 与 JWT roles claim 一致
- name / email / picture 受 OIDC scope 控制；`id` / `sub` / `roles` 始终返回

### 3.4 Go SDK：`pkg/userclient`

apps/api 里有现成的 Go SDK（`pkg/userclient`），封装了：

- TTL 缓存（默认 10 分钟）+ 负缓存（默认 1 分钟，避免反复查不存在的 ID）
- `singleflight` 合并并发请求
- 自动分片（>100 ID 自动拆分）
- 输入去重

使用示例：

```go
import "api/pkg/userclient"

// 服务启动时初始化一次（建议放进 DI 容器）
cli := userclient.New(userclient.Config{
    BaseURL:      "https://oauth.kungal.com/api/v1",
    ClientID:     "kungal-backend",
    ClientSecret: os.Getenv("OAUTH_CLIENT_SECRET"),
    CacheTTL:     10 * time.Minute,
})

// 批量取
users, err := cli.Users(ctx, []uint{1, 2, 3, 4})
// users[1].Name, users[1].Avatar...

// 单个（返回 nil 表示不存在）
u, err := cli.User(ctx, 1)

// 按名搜索（不缓存；前端要做实时补全请 debounce 200-300ms）
matches, err := cli.Search(ctx, "kun", 10)

// 用户改名/换头像后，主动失效缓存
cli.Invalidate(uid)
```

## 4. 站点 user 表瘦身建议

迁移完毕后，站点 user 表可以删掉这些列（逐步即可，不必一蹴而就）：

| 列 | 是否可删 |
|----|---------|
| name | ✓ 可以删（OAuth 提供） |
| email | ✓ 可以删（OAuth 提供） |
| password | ✓ 必须删（OAuth 是身份源；本地保留是安全风险） |
| avatar | ✓ 可以删（OAuth 提供） |
| bio | ✓ 可以删（OAuth 提供） |
| role | ✓ 可以删，但要先确认所有权限判断都改走 OAuth roles claim 或 `/users/batch` 返回的 roles 字段 |
| status | ✓ 可以删（OAuth 提供） |
| moemoepoint | ✗ **保留**（OAuth 也有但被认为是 OAuth-端总积分；站点的 moemoepoint 是站点行为分） |
| daily_check_in / daily_image_count | ✗ **保留**（站点功能特有） |
| daily_toolset_upload_count（kungal）/ daily_upload_size（moyu） | ✗ **保留** |
| last_login_time | ✗ **保留** |
| ip | ✗ **保留**（站点最近会话指纹） |
| follower_count / following_count（moyu）| 由你定 —— 反归一计数，可删可留 |

**建议路径**：

1. 改代码：渲染层全部用 SDK 拿 name/avatar/bio
2. 上线，观察一个迭代（确保没有遗漏的引用）
3. 一次性 ALTER TABLE DROP 那些列

## 5. 缓存与一致性

### 5.1 默认行为

`userclient.Users()` 用 10 分钟 TTL 缓存。意味着：

- 用户改了 name → 下游服务最多滞后 10 分钟才能看到
- 头像换了 → 同上
- 封号了 → 同上

对**展示字段**（name / avatar / bio）—— 这种延迟一般可以接受。

对**强一致字段**（status / roles）—— 你可能需要更短的 TTL。两种做法：

```go
// 方法 A：全局短 TTL
cli := userclient.New(userclient.Config{
    CacheTTL: 30 * time.Second,
    ...
})

// 方法 B：默认长 TTL，需要强一致时绕过缓存（暂时没暴露 API；可加）
// 或者：在权限决策点直接用 JWT 的 roles claim（解码 access_token 即可拿到，无需 RPC）
```

### 5.2 主动失效

如果是你自己的服务发出的 mutation（很少见，因为身份字段都在 OAuth 端），可以主动失效：

```go
cli.Invalidate(uid)
```

但更常见的失效是 OAuth 端发出 —— 此时下游需要订阅事件流。**当前没有事件 broadcast**；如果需要，要在 OAuth 端加 webhook / message bus。

### 5.3 N+1 防护

永远批量拉。**不要**在循环里 `cli.User(ctx, item.UserID)`：

```go
// ❌ N+1 反例
for _, item := range items {
    user, _ := cli.User(ctx, item.UserID)
    ...
}

// ✓ 批量
ids := []uint{}
for _, item := range items {
    ids = append(ids, item.UserID)
}
users, _ := cli.Users(ctx, ids)
for _, item := range items {
    user := users[item.UserID]
    ...
}
```

虽然 `cli.User` 命中缓存时是 in-memory hit（很快），但 miss 时仍会发 N 次 HTTP 请求。`cli.Users` 一次性发一个请求，命中或 miss 都更省。

## 6. 终端用户登录的接入

这一步在你做用户登录回调时做：

```
用户 → 点击 "用 KUN 账号登录"
  ↓
你的站点 → 重定向到 /oauth/authorize
  ↓
用户在 OAuth 登录（如未登录）
  ↓
OAuth 重定向回你的 redirect_uri，带 code
  ↓
你的服务端 → /oauth/token 用 code 换 access_token
  ↓
你的服务端 → /oauth/userinfo 拿用户 id / sub / name / email / roles
  ↓
你的服务端：
  - 在本站 user 表查 id（应该等于 userinfo.id）
  - 不存在 → INSERT 新行（user.id = userinfo.id）
  - 创建 session、设 cookie
```

**关键点**：

- 本站 user 表的 `id` 列必须接受 OAuth 给的 integer ID（即不要 autoincrement，要从 OAuth 取值）
- 或者：禁用 autoincrement，每次 INSERT 显式给 ID
- 或者：保留 autoincrement 但忽略它，用 OAuth ID 写入 —— 然后定期把 sequence reset 到 max

详见 `docs/integration/oauth/oauth-integration-guide.md`。

## 7. 调试与排错

### 7.1 SDK 日志

`userclient` 当前不主动 log，但 errors 会传播。在调用处 wrap：

```go
users, err := cli.Users(ctx, ids)
if err != nil {
    slog.Error("userclient.Users failed", "ids", ids, "err", err)
    // 决定：返回错误 / 用 fallback 渲染 / 静默
}
```

### 7.2 OAuth 端不可用怎么办

短期：缓存里有的还能用（10 分钟内）。10 分钟之后 cache 过期、OAuth 仍不可用 → SDK 返回 error。

中长期建议：在你的渲染层加 graceful degradation —— 拿不到 user brief 时仍然渲染 user_id（数字），不要 500。

### 7.3 验证 OAuth Client 凭证

```bash
curl -sS -w "\n%{http_code}\n" \
  'https://oauth.kungal.com/api/v1/users/batch?ids=1' \
  -H "Authorization: Basic $(echo -n 'YOUR_CLIENT_ID:YOUR_CLIENT_SECRET' | base64)"
```

期望 200 + 用户 brief。如果 401，检查：

- client_id 是否在 OAuth 端注册了
- client_secret 是否正确
- 该 client 是否启用

## 8. 参考文档

- [api-reference.md](../../integration/oauth/api-reference.md) —— 完整 OAuth API 参考（含 `/users/batch` `/users/search` `/oauth/userinfo`）
- [oauth-integration-guide.md](../../integration/oauth/oauth-integration-guide.md) —— 完整 OAuth 接入指南（含 PKCE、token 轮换、安全注意事项）
