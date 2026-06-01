# OAuth 审查反馈 & 行动项

> 2026-04-12 — OAuth 团队对 plans/ 文档的审查意见及处理

## 反馈 1：Phase 2 端点归属

**状态：已处理**

`02-phase2-galgame.md` 已标记为过时，最终架构以 `08-galgame-service-architecture.md` 为准。

拆分规则：
- **galgame service (OAuth repo)**：Step 1/5/6/8/9（CRUD/PR/link/series/元数据）
- **kungal 后端 (apps/api)**：Step 2/3/4/7（like/comment/resource/rating）

---

## 反馈 2：错误码体系不兼容

**状态：待实施**

### 问题

| 系统 | 错误码 | 含义 |
|------|--------|------|
| OAuth/galgame service | 0, 15001-15007, 10001-10005 | 精确到每个错误类型 |
| kungal 后端 | 0, 205, 233 | 三档：成功/认证失效/业务错误 |

galgame service 返回 `code: 15003` 时，kungal 后端转发给前端，前端不认识。

### 方案

**前端 `responseHandler.ts` 改为更通用的判断：**

```typescript
// 认证失效判断：205 (kungal) 或 10001-10099 (OAuth 认证错误范围)
const isAuthError = code === 205 || (code >= 10001 && code <= 10099)

// 业务错误判断：所有 code !== 0 且不是认证错误
const isBizError = code !== 0 && !isAuthError
```

**kungal 后端透传 galgame service 错误时不做映射：**

```go
// GalgameClient 调用失败时，直接返回 galgame service 的 code + message
func (c *GalgameClient) handleError(resp *http.Response) *errors.AppError {
    var errResp struct {
        Code    int    `json:"code"`
        Message string `json:"message"`
    }
    json.NewDecoder(resp.Body).Decode(&errResp)

    // 透传 code 和 message
    return errors.New(errResp.Code, errResp.Message, resp.StatusCode)
}
```

**kungal 后端自身的错误码保持 205/233 不变**（避免全面改造），只有转发 galgame service 的错误时透传原始码。

### 行动项

- [ ] 前端 `responseHandler.ts` 更新认证判断逻辑
- [ ] kungal 后端 `GalgameClient` 实现错误透传
- [ ] 文档: 在 `01-architecture-patterns.md` 补充错误码兼容说明

---

## 反馈 3：JWT claims 加 integer user_id

**状态：待 OAuth 侧实施**

### 问题

OAuth JWT 当前 claims：
```json
{
  "sub": "550e8400-...",  // UUID
  "email": "...",
  "name": "...",
  "roles": ["user"],
  "site_id": 0
}
```

缺少 integer `user_id`。galgame service 每次写操作都要查 `kun_galgame_infra.users` 做 UUID → integer 转换。

### 方案

在 OAuth 的 `GenerateAccessToken` 中增加 `uid` claim：

```json
{
  "sub": "550e8400-...",
  "uid": 12345,            // ← 新增
  "email": "...",
  "name": "...",
  "roles": ["user"],
  "site_id": 0
}
```

galgame service 的认证中间件直接从 JWT 提取 `uid`，无需查库。

### 行动项

- [ ] OAuth repo: `GenerateAccessToken` 增加 `uid` (integer) claim
- [ ] galgame service: 认证中间件从 JWT 提取 `uid`，不再查库
- [ ] 已有依赖 JWT 的系统（如 kungal 后端的 session refresh）确认兼容新 claim

---

## 反馈 4：萌萌点跨服务

**状态：当前设计正确，无需改动**

调用顺序：先 galgame service（创建元数据），成功后 kungal 后端本地事务（加萌萌点）。

失败方向正确：
- galgame service 失败 → 不加萌萌点 ✅
- galgame service 成功 + 萌萌点失败 → 数据已创建但无奖励（可接受）✅
- 不会出现萌萌点加了但数据没创建 ✅

---

## 反馈 5：like 萌萌点归属 — 确认是 BUG

**状态：已确认，Go 重写时修复**

### 证据

**topic like** (`topic/[tid]/like.put.ts:54`)：
```typescript
await prisma.user.update({
    where: { id: topic.user_id },   // ← 给被赞者加萌萌点
    data: { moemoepoint: { increment: isLiked ? -1 : 1 } }
})
```

**galgame like** (`galgame/[gid]/like.put.ts:53-60`)：
```typescript
await prisma.user.update({
    where: { id: uid },              // ← 给操作者自己加萌萌点
    data: { moemoepoint: { increment: isLikedGalgame ? -1 : 1 } }
})
```

### 差异对比

| 行为 | topic like | galgame like |
|------|-----------|-------------|
| 萌萌点给谁 | 被赞者 (topic.user_id) | 操作者自己 (uid) |
| 能否赞自己 | 不能（返回错误） | 能（跳过萌萌点） |

### 结论

galgame like 的行为是 bug。Go 重写时统一为：
- 萌萌点给**被赞者**（galgame 创建者）
- 不允许给自己点赞（返回错误）

### 行动项

- [ ] Go 重写 galgame like 时修正为给被赞者加萌萌点
- [ ] 在 `01-architecture-patterns.md` 的互动 Toggle 模式中明确注释萌萌点归属
- [ ] 常量文件注释：`// like 操作给被赞者(内容创建者) +1/-1 萌萌点, 非操作者自己`
