# Nitro 代码反模式 & Go 重写注意事项

> **重要**: 本文档列举了原 Nitro 后端中不应被带到 Go Fiber 后端的具体问题。
> 每一条都附有原始文件路径和代码，以及 Go 中应该如何正确处理。

---

## 一、严重 BUG

### 1.1 Galgame 创建 — 错误消息与逻辑反转

**文件:** `api/galgame/index.post.ts:15-20`

```typescript
const galgame = await prisma.galgame.count({
  where: { vndb_id: input.vndbId }
})
if (galgame) {
  return kunError(event, '未找到这��� Galgame')  // ← 消息反了
}
```

`count()` 返回数字，`count > 0` 意味着**已存在**，但错误消息却说"未找到"。这是错误消息和语义的双重错误。

**Go 中应该:**
```go
exists, _ := repo.ExistsByVndbID(vndbID)
if exists {
    return errors.ErrBadRequest("该 VNDB ID 的 Galgame 已存在")
}
```

### 1.2 Reply 错误码不一致

**文件:** `api/topic/[tid]/reply/index.post.ts:12`

```typescript
return kunError(event, '用户登录失效', 401)  // ← 用了 401
```

而项目约定认证失效用 `205`，其他所有端点都用 `205`。这个端点用了 `401`，前端的 `responseHandler` 不会触发跳转登录页的逻辑。

**Go 中应该:** 统一使用 `errors.ErrAuthExpired()` (code=205)，不要有例外。

---

## 二、竞态条件 (TOCTOU)

### 2.1 萌萌点 / 发帖限制在事务外检查

**文件:** `api/topic/index.post.ts:19-49` 和 `api/galgame/index.post.ts:27-45`

```typescript
// 事务外: 读取用户和今日发帖数
const user = await prisma.user.findUnique({
  where: { id: userInfo.uid },
  include: { topic: { where: { created: { gte: subDays(new Date(), 1) } } } }
})
if (user.moemoepoint / 10 + 1 < user.topic.length) {
  return kunError(event, '您今日发布的话题已经达到上限')
}
// ...
// 事务内: 扣萌萌点
return prisma.$transaction(async (prisma) => {
  await prisma.user.update({ data: { moemoepoint: { increment: -10 } } })
})
```

**问题:** 在 `findUnique` 和 `$transaction` 之间，用户可能同时发起多个请求。所有请求都通过了限制检查，然后都成功扣萌萌点。

**Go 中应该:**
```go
err := db.Transaction(func(tx *gorm.DB) error {
    var user model.User
    // SELECT ... FOR UPDATE 加行锁
    tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&user, uid)

    todayCount := countTodayTopics(tx, uid)
    if user.Moemoepoint/10+1 < todayCount {
        return fmt.Errorf("今日发帖已达上限")
    }

    // 在事务内扣萌萌点
    tx.Model(&user).Update("moemoepoint", gorm.Expr("moemoepoint - ?", 10))
    // ...创建话题...
    return nil
})
```

### 2.2 Like Toggle 的 check-then-act

**文件:** `api/galgame/[gid]/like.put.ts:17-31`

```typescript
const galgame = await prisma.galgame.findUnique({
  include: { like: { where: { user_id: uid } } }
})
const isLikedGalgame = galgame.like.length > 0

// 事务内基于 isLikedGalgame 做分支
return prisma.$transaction(async (prisma) => {
  if (isLikedGalgame) {
    await prisma.galgame_like.delete({...})
  } else {
    await prisma.galgame_like.create({...})
  }
})
```

**问题:** 快速双击时，两个请求都看到 `isLikedGalgame = false`，都执行 `create`，导致唯一约束违反或重复点赞。

**Go 中应该:** 在事务内查询当前状态：
```go
err := db.Transaction(func(tx *gorm.DB) error {
    var existing model.GalgameLike
    result := tx.Where("user_id = ? AND galgame_id = ?", uid, gid).First(&existing)
    if result.Error == gorm.ErrRecordNotFound {
        tx.Create(&model.GalgameLike{...})
        tx.Model(&model.Galgame{}).Where("id = ?", gid).
            Update("like_count", gorm.Expr("like_count + 1"))
    } else {
        tx.Delete(&existing)
        tx.Model(&model.Galgame{}).Where("id = ?", gid).
            Update("like_count", gorm.Expr("like_count - 1"))
    }
    return nil
})
```

### 2.3 消息去重的 check-then-act

**文件:** `utils/message.ts` (createDedupMessage)

```typescript
const duplicatedMessage = await prisma.message.findFirst({
  where: { sender_id, receiver_id, type, content, link }
})
if (duplicatedMessage) return
const newMessage = await prisma.message.create({...})
```

**Go 中应该:** 使用数据库 UNIQUE 约束 + `ON CONFLICT DO NOTHING`，或在同一事务内使用 `FOR UPDATE`。

---

## 三、事务中包含外部调用 (严禁)

### 3.1 事务内调用 VNDB API + S3 上传

**文件:** `api/galgame/index.post.ts:57-131`

```typescript
return prisma.$transaction(async (prisma) => {
  const newGalgame = await prisma.galgame.create({...})

  await syncVndbData(prisma, {...})           // ← 外部 HTTP 调用 VNDB API
  // ...
  await uploadGalgameBanner(Buffer.from(...)) // ← S3 上传
  // ...
}, { timeout: 60000 })                        // ← 60 秒超时！
```

**问题:**
- VNDB API 慢或超时 → 事务挂起 → 数据库连接被占用 → 其他请求被阻塞
- S3 上传失败 → 整个 galgame 创建回滚，但如果 VNDB 已写入关联数据则不一致
- 60 秒的事务锁在高并发下是灾难

**Go 中应该:** 分阶段执行，不在事务中做外部调用：

```go
// Phase 1: 事务 — 只做数据库操作
var galgameID int
err := db.Transaction(func(tx *gorm.DB) error {
    galgame := &model.Galgame{...}
    tx.Create(galgame)
    tx.Create(&model.GalgameContributor{...})
    tx.Model(&model.User{}).Update("moemoepoint", gorm.Expr("moemoepoint + 3"))
    galgameID = galgame.ID
    return nil
})

// Phase 2: 异步 — 外部调用，失败不回滚主记录
go func() {
    syncVndbData(galgameID, vndbID)      // VNDB API
    uploadBanner(galgameID, bannerData)   // S3
}()
```

### 3.2 PR Merge 事务内也调用了 VNDB

**文件:** `api/galgame/[gid]/pr/merge.put.ts:50-57`

```typescript
return prisma.$transaction(async (prisma) => {
  if (galgamePR.galgame.vndb_id !== prJSONObject.vndbId) {
    await resyncVndbData(prisma, {...})  // ← 又在事务内调 VNDB
  }
  // ...
})
```

同样的问题。Go 中不要在事务内发 HTTP 请���。

---

## 四、N+1 查询

### 4.1 Reply 中对每个 target 用户单独更新萌萌点

**文件:** `api/topic/[tid]/reply/index.post.ts:84-98`

```typescript
for (const [, { user, content }] of targetUsersMap) {
  await prisma.user.update({                    // ← 循环中的单条 UPDATE
    where: { id: user.id },
    data: { moemoepoint: { increment: 1 } }
  })
  await createMessage(prisma, ...)              // ← 循环中的 INSERT
}
```

如果回复 @ 了 5 个人，就是 5 次 UPDATE + 5 次 INSERT = 10 次 DB 调用。

**Go 中应该:** 批量操作：
```go
// 批量更新萌萌点
userIDs := collectTargetUserIDs(targets)
tx.Model(&model.User{}).
    Where("id IN ?", userIDs).
    Update("moemoepoint", gorm.Expr("moemoepoint + 1"))

// 批量创建消息
messages := buildMessages(targets)
tx.Create(&messages)
```

### 4.2 事务内每创建一条 Markdown 都调用 markdownToHtml

**文件:** `api/topic/[tid]/reply/index.post.ts:116-132`

```typescript
const formattedTargets = await Promise.all(
  newReply.target.map(async (targetRelation) => {
    return {
      replyContentHtml: await markdownToHtml(targetRelation.content) // ← 每个 target 一次
    }
  })
)
// 还有主回复本身:
contentHtml: await markdownToHtml(newReply.content)
```

**问题:** Markdown → HTML 是 CPU 密集操作，在事务内串行执行拖长了事务时间。

**Go 中应该:** 在事务外处理 Markdown，或预渲染后存储 HTML 列。

---

## 五、业务逻辑放错���置

### 5.1 萌萌点计算写在 Handler 层

**文件:** `api/galgame-rating/index.post.ts:24-26`

```typescript
const contentLength = rest.short_summary.length
const moemoepointIncrement =
  contentLength > 100 ? 10 : contentLength > 20 ? 5 : 3
```

**文件:** `api/topic/index.post.ts:40-49`

```typescript
const hasConsumeSection = TOPIC_SECTION_CONSUME_MOEMOEPOINTS.some(...)
if (hasConsumeSection) {
  if (user.moemoepoint < MOEMOEPOINT_COST_FOR_CONSUME_SECTION) {
    // ...
  }
}
```

handler 里夹杂了评分奖励规则和 section 消费规则。

**Go 中应该:** 业务规则属于 Service 层：
```go
// service/rating_service.go
func calculateRatingReward(summaryLen int) int {
    switch {
    case summaryLen >= 666: return 10
    case summaryLen >= 233: return 5
    default:                return 3
    }
}
```

### 5.2 魔法数字散落各处

| 数字 | 含义 | 出现文件 |
|------|------|---------|
| `3` | 创建 galgame/topic/toolset 奖励 | 多处 |
| `-10` | 创建消费类 section 扣除 | topic/index.post.ts |
| `17` | 改用户名消耗 | user/username.put.ts |
| `100`, `20` | 评分奖励阈值 | galgame-rating/index.post.ts |
| `233` | 文本预览截断长度 | 多处 |
| `150` | 回复预览截断 | topic/[tid]/reply/index.post.ts |
| `60000` | 事务超时 ms | galgame/index.post.ts |

**Go 中应该:** 集中到常量文件：
```go
// internal/constants/moemoepoint.go
const (
    RewardCreateGalgame  = 3
    RewardCreateTopic    = 3
    RewardCreateResource = 3
    CostConsumeSection   = 10
    CostChangeUsername   = 17

    RatingRewardTierHigh   = 10  // summary >= 666 chars
    RatingRewardTierMedium = 5   // summary >= 233 chars
    RatingRewardTierLow    = 3   // default

    RatingLenThresholdHigh   = 666
    RatingLenThresholdMedium = 233

    TextPreviewLength = 233
)
```

---

## 六、缺失验证

### 6.1 Like 操作不验证目标是否被封禁

**文件:** `api/galgame/[gid]/like.put.ts:17`

```typescript
const galgame = await prisma.galgame.findUnique({
  where: { id: galgameId, status: { not: 1 } }
})
```

这里检查了 galgame 的 status，但 topic 的 like 端点没有做相同检查。不一致。

**Go 中应该:** 所有互动操作统一检查目标状态。

### 6.2 PR Merge 状态检查在事务外

**文件:** `api/galgame/[gid]/pr/merge.put.ts:35-40`

```typescript
// 事务外检查
if (galgamePR.status !== 0) {
  return kunError(event, '这个更新请求已经被拒绝或合并')
}
// ...
// 事务内直接操作，不再检查 status
return prisma.$transaction(async (prisma) => {
  await prisma.galgame_pr.update({
    where: { id: input.galgamePrId },
    data: { status: 1 }
  })
})
```

**Go 中应该:** 在事务内再次验证 + 使用乐观锁：
```go
result := tx.Model(&model.GalgamePR{}).
    Where("id = ? AND status = 0", prID).  // 原子检查+更新
    Updates(map[string]any{"status": 1, "completed_time": time.Now()})
if result.RowsAffected == 0 {
    return fmt.Errorf("PR 已被处理")
}
```

### 6.3 楼层计算的竞态

**文件:** `api/topic/[tid]/reply/index.post.ts:25-30`

```typescript
// 事务外计算楼层
const lastReply = await prisma.topic_reply.findFirst({
  where: { topic_id: topicId },
  orderBy: { floor: 'desc' }
})
const newFloor = (lastReply?.floor || 0) + 1

// 事务内使用
return prisma.$transaction(async (prisma) => {
  await prisma.topic_reply.create({
    data: { floor: newFloor, ... }
  })
})
```

两个并发回复会得到相同的 `newFloor`。

**Go 中应该:**
```go
err := db.Transaction(func(tx *gorm.DB) error {
    // 在事务内获取当前最大楼层（自带行锁）
    var maxFloor int
    tx.Model(&model.TopicReply{}).
        Where("topic_id = ?", topicID).
        Select("COALESCE(MAX(floor), 0)").
        Scan(&maxFloor)

    reply := &model.TopicReply{
        Floor: maxFloor + 1,
        // ...
    }
    return tx.Create(reply).Error
})
```

---

## 七、错误处理问题

### 7.1 静默吞掉错误

**文件:** `api/galgame/index.post.ts:52-54`

```typescript
const res = await readGalgameData(event)
if (!res) {
  return  // ← 如果 readGalgameData 内部已经 return kunError，
          //    外部又 return undefined → 客户端收到空响应
}
```

**文件:** `utils/message.ts` (createDedupMessage)
```typescript
const duplicatedMessage = await prisma.message.findFirst({...})
if (duplicatedMessage) {
  return  // ← 去重后静默返回，调用者不知道消息未创建
}
```

**Go 中应该:** 始终返回明确的成功/失败状态。

### 7.2 事务内不区分错误类型

所有 Nitro 端点在事务中如果 Prisma 报错，统一回滚但不区分是唯一约束冲突、外键违反还是连接错误。

**Go 中应该:**
```go
if errors.Is(err, gorm.ErrDuplicatedKey) {
    return errors.ErrBadRequest("记录已存在")
}
if errors.Is(err, gorm.ErrForeignKeyViolated) {
    return errors.ErrBadRequest("关联记录不存在")
}
return errors.ErrInternal("操作失败")
```

---

## 八、性能问题

### 8.1 每次请求实时渲染 Markdown

`api/galgame/[gid]/index.get.ts` 详情页每次请求对 4 个语言的 intro 都做 `markdownToHtml()`。

`api/topic/[tid]/reply/index.post.ts` 创建回复时对内容和所有 target 都做 `markdownToHtml()`。

**Go 中应该:**
- 创建/更新时渲染一次，存储 HTML 到专门的列 (如 `content_html`)
- 读取时直接返回预渲染的 HTML
- galgame intro 可以用 Redis 缓存渲染结果

### 8.2 搜索使用 ILIKE 全表扫描

**文件:** `api/search/_search.ts`

```typescript
title: { contains: kw, mode: 'insensitive' },
content: { contains: kw, mode: 'insensitive' }
```

**Go 中:** 使用 Meilisearch，不要在 GORM 中用 `ILIKE '%keyword%'`。

### 8.3 详情页查询过多关联

**文件:** `api/galgame/[gid]/index.get.ts` — 单次查询 include 了 10+ 个关联。

**Go 中应该:** 使用 `errgroup` 并行查询，或者只 `Preload` 必要关联：
```go
g, ctx := errgroup.WithContext(c.Context())

var galgame model.Galgame
g.Go(func() error { return db.First(&galgame, gid).Error })

var tags []model.GalgameTagRelation
g.Go(func() error { return db.Where("galgame_id = ?", gid).Preload("Tag").Find(&tags).Error })

// ... 并行查询其他关联
g.Wait()
```

---

## 九、安全问题

### 9.1 没有萌萌点余额下限保护

多处 `moemoepoint: { increment: -10 }` 操作没有检查余额是否足够（仅在事务外检查过，见 2.1）。

**Go 中应该:** 数据库层面添加 CHECK 约束：
```sql
ALTER TABLE "user" ADD CONSTRAINT user_moemoepoint_non_negative
    CHECK (moemoepoint >= 0);
```

### 9.2 Like 操作扣自己的萌萌点 (而非被赞者)

**文件:** `api/galgame/[gid]/like.put.ts:52-60`

```typescript
if (uid !== galgame.user_id) {
  await prisma.user.update({
    where: { id: uid },           // ← 扣的是点赞者自己
    data: { moemoepoint: { increment: isLikedGalgame ? -1 : 1 } }
  })
}
```

取消赞时扣自己 1 萌萌点，点赞时给自己 1 萌萌点。**是否是预期行为？** 通常应该是给**被赞者**加减萌萌点，而非操作者。

**Go 中应该:** 确认业务需求后再实现。如果确实是给自己加减，需要在文档中明确说明。

### 9.3 文件上传无扩展名白名单

**文件:** `api/toolset/[id]/upload/large.post.ts` — 直接使用用户提供的文件名扩展名作为 S3 key。

**Go 中应该:** 白名单校验扩展名：
```go
allowedExts := map[string]bool{
    ".zip": true, ".7z": true, ".rar": true,
    ".tar": true, ".gz": true, ".xz": true,
}
if !allowedExts[ext] {
    return errors.ErrBadRequest("不支持的文件格式")
}
```

---

## 十、Go Fiber 特有注意事项 (非 Nitro 问题)

这些不是 Nitro 的问题，但是 Node.js → Go 迁移时容易犯的错：

### 10.1 Fiber 的 `c.Context()` 生命周期

Fiber 的 `c.Context()` 返回的是 `*fasthttp.RequestCtx`，请求结束后会被回收。**不要**把它传给 goroutine：

```go
// ❌ 错误
go func() {
    db.WithContext(c.Context()).Create(&record)  // ctx 已被回收
}()

// ✅ 正确
ctx := context.Background()  // 或 copy 出来
go func() {
    db.WithContext(ctx).Create(&record)
}()
```

### 10.2 不要用 Prisma 式的嵌套 include 思维

Prisma 的 `include` 会生成复杂 JOIN 查询。GORM 的 `Preload` 是 N+1 查询 (每个关联一次 SELECT)。对于深度嵌套关联，应该手动写 JOIN 或分步查询。

```go
// ❌ 不要模仿 Prisma 的 10 层 include
db.Preload("Tags.Tag").Preload("Officials.Official").
   Preload("Engines.Engine").Preload("Contributors.User").
   Preload("Series").Preload("Likes").First(&galgame, id)

// ✅ 分步并行查询
g, _ := errgroup.WithContext(ctx)
g.Go(func() error { return db.First(&galgame, id).Error })
g.Go(func() error { return db.Where(...).Find(&tags).Error })
g.Go(func() error { return db.Where(...).Find(&officials).Error })
g.Wait()
```

### 10.3 GORM 的 `Save()` vs `Updates()`

- `Save()` 会更新所有字段（包括零值），可能意外覆盖数据
- 始终用 `Updates()` + 指定字段

```go
// ❌ 危险
db.Save(&user)  // 如果 user.Moemoepoint 是 0，会覆盖为 0

// ✅ 安全
db.Model(&user).Updates(map[string]any{"bio": newBio})
```

### 10.4 不要用 `string(body)` 传 HTTP 请求体

当前 Go 代码中有多处:
```go
strings.NewReader(string(body))  // body 是 []byte
```

这产生了不必要的内存拷贝。应该用 `bytes.NewReader(body)`。

---

## 总结清单

在实现每个 Go 端点前，对照检查：

- [ ] **事务内不含外部调用** (HTTP、S3、邮件)
- [ ] **所有状态检查在事务内** (萌萌点余额、发帖限制、PR 状态)
- [ ] **互动 toggle 在事务内查询当前状态**，不用事务外的快照
- [ ] **批量操作代替循环** (消息、萌萌点更新)
- [ ] **楼层/序号计算在事务内**
- [ ] **业务规则在 Service 层**，Handler 只做请求解析和响应
- [ ] **魔法数字用常量**
- [ ] **错误消息准确** (不要把"已存在"写成"未找到")
- [ ] **错误码统一** (认证失效一律 205)
- [ ] **Markdown 预渲染存储**，不要每次请求实时渲染
- [ ] **搜索用 Meilisearch**，不用 ILIKE
- [ ] **文件上传校验扩展名白名单**
- [ ] **Fiber ctx 不传给 goroutine**
- [ ] **GORM 用 Updates 不用 Save**
