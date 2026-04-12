# Phase 5: 基础设施收尾

> 前置: Phase 2-4 核心端点完成

## 1. 定时任务 (robfig/cron)

### 每日重置 (0 0 * * *)
```go
// 重置所有用户的每日计数器
UPDATE "user" SET
    daily_check_in = 0,
    daily_image_count = 0,
    daily_toolset_upload_count = 0
```

### 每小时清理 (0 * * * *)
```go
// 清理过期的 toolset 资源 (S3 对象)
// 1. 查询 status=expired 的 galgame_toolset_resource
// 2. 删除 S3 对象
// 3. 删除数据库记录
```

**实现位置:** `internal/infrastructure/cron/cron.go`

**在 app.go 中启动:**
```go
cronScheduler := cron.New(cron.WithSeconds())
cronScheduler.AddFunc("0 0 0 * * *", resetDailyTask(db))
cronScheduler.AddFunc("0 0 * * * *", cleanupToolsetTask(db, s3))
cronScheduler.Start()
// graceful shutdown 时 cronScheduler.Stop()
```

---

## 2. Markdown 渲染 (goldmark)

### 当前 Nitro 管线

```
remark-parse → GFM → frontmatter → math
→ rehype → sanitize → slug → prism → katex
→ kunLazyImage → kunCodeBlockWrapper → kunH1ToH2
→ kunTableWrapper → kunInsertWbr → kunVideo
→ stringify
→ 后处理: spoiler (||text||), video (kv:<url>)
```

### Go 目标管线

```go
import (
    "github.com/yuin/goldmark"
    "github.com/yuin/goldmark/extension"  // GFM
    mathjax "github.com/nicholasgasior/goldmark-mathjax"
    highlighting "github.com/yuin/goldmark-highlighting/v2"
)

md := goldmark.New(
    goldmark.WithExtensions(
        extension.GFM,             // 表格/删除线/自动链接
        mathjax.MathJax,           // 数学公式
        highlighting.Highlighting, // 代码高亮
    ),
)
```

### 自定义 AST Walker

需实现的 goldmark 扩展:

| 插件 | 功能 | 对应 Nitro |
|------|------|-----------|
| LazyImage | img 标签添加 loading="lazy" | rehypeKunLazyImage |
| CodeBlockWrapper | 代码块外包一层 div | rehypeKunCodeBlockWrapper |
| H1ToH2 | h1 → h2 (防止多个 h1) | rehypeKunH1ToH2 |
| TableWrapper | table 外包 div (横向滚动) | rehypeKunTableWrapper |
| InsertWbr | 长单词插入 `<wbr>` | rehypeKunInsertWbr |
| VideoEmbed | `kv:<url>` → video 标签 | rehypeKunVideo |

### 后处理 (regex)

```go
// Spoiler: ||text|| → <span class="spoiler">text</span>
re := regexp.MustCompile(`\|\|(.+?)\|\|`)
html = re.ReplaceAllString(html, `<span class="spoiler">$1</span>`)
```

### 使用位置

- Topic 创建/编辑: content → html
- Reply 创建/编辑: content → html
- 文本预览: slice(0, 233) (去掉 HTML 标签)

**风险:** 渲染结果可能与 remark/rehype 有差异。建议对 100+ 篇真实文章做对比测试。

---

## 3. Meilisearch 搜索集成

### 索引结构

**galgame 索引:**
```json
{
    "id": 1,
    "vndb_id": "v1234",
    "name_en_us": "...",
    "name_ja_jp": "...",
    "name_zh_cn": "...",
    "name_zh_tw": "...",
    "aliases": ["alias1", "alias2"],
    "tags": ["tag1", "tag2"],
    "banner": "...",
    "created": 1700000000
}
```

**topic 索引:**
```json
{
    "id": 1,
    "title": "...",
    "content": "...",  // 纯文本, 去 HTML
    "tags": ["tag1"],
    "user_name": "...",
    "created": 1700000000
}
```

**user 索引:**
```json
{
    "id": 1,
    "name": "...",
    "avatar": "..."
}
```

### 同步策略

- 全量同步: 启动时或 admin 触发
- 增量同步: 创建/更新/删除时同步更新索引
- 实现位置: `internal/infrastructure/search/meilisearch.go`

### 搜索 API

```
GET /api/search?keywords=xxx&type=galgame&page=1&limit=20
```

type: galgame | topic | user | all

---

## 4. WebSocket (Socket.IO)

### 推荐方案: go-socket.io

前端保持不变, 后端用 `github.com/googollee/go-socket.io`

### 事件

| 事件 | 方向 | 说明 |
|------|------|------|
| private:join | C→S | 加入私聊房间 |
| message:sending | C→S | 发送消息 |
| message:recall | C→S | 撤回消息 |
| private:leave | C→S | 离开房间 |
| message:received | S→C | 接收消息推送 |

### 认证

```go
// Socket.IO 中间件: 读取 kun_session cookie, 验证 Redis session
server.OnConnect("/", func(s socketio.Conn) error {
    cookie := s.RemoteHeader().Get("Cookie")
    // 解析 kun_session, 验证 Redis
    // 将 UserInfo 存入 s.SetContext()
})
```

### 数据库操作

- ChatMessage CRUD
- ChatMessageReadBy 标记
- ChatMessageReaction

---

## 5. 邮件验证码

### 发送验证码

```
POST /api/auth/email/send-code
Body: { email, type: "register" | "forgot" | "reset" }
```

逻辑:
1. 频率限制: 30 秒内不可重复发送 (Redis key: `email_cooldown:{email}`)
2. 生成 7 位随机验证码
3. 生成 32 字节 salt
4. 存入 Redis: key=`{salt}:{email}`, value=code, TTL=10min
5. 发送邮件 (net/smtp)
6. 返回 salt 给前端 (前端提交时需携带 salt)

### 验证码校验

```
POST /api/auth/email/verify
Body: { email, code, codeSalt }
```

逻辑:
1. 从 Redis 读取 `{codeSalt}:{email}`
2. 比较 code
3. 匹配则删除 key, 返回成功

---

## 6. CDN 缓存清除

### Cloudflare API

```go
func PurgeURLs(urls []string) error {
    body := map[string][]string{"files": urls}
    req, _ := http.NewRequest("POST",
        "https://api.cloudflare.com/client/v4/zones/{ZONE_ID}/purge_cache",
        ...)
    req.Header.Set("Authorization", "Bearer {CF_API_TOKEN}")
    ...
}
```

### 触发时机

- 用户上传新头像 → purge `/avatar/{uid}/*`
- Galgame 更新 banner → purge `/galgame/{gid}/banner`

---

## 7. 前端全量适配

### 剩余工作

所有使用旧 `useFetch` + `kungalgameResponseHandler` 的页面需迁移到 `kunFetch` / `useKunFetch`。

**迁移清单 (按页面):**
- [ ] galgame 列表/详情/创建/编辑
- [ ] topic 列表/详情/创建/编辑
- [ ] toolset 列表/详情
- [ ] website 列表/详情
- [ ] doc 文章页
- [ ] ranking 页
- [ ] search 页
- [ ] admin 管理页
- [ ] message/chat 页
- [ ] user 资料页 (部分已完成)

### 数据字段适配

旧 Prisma 返回的 `_count.xxx` → 新 Go 返回的 `xxx_count`:
```typescript
// 旧
item._count.like

// 新
item.like_count
```

---

## 8. 部署切换策略

### 方案: Nginx 路由分流 (渐进式迁移)

```nginx
# Phase 1 完成后: user/auth 走 Go
location ~ ^/api/(auth|user|home) {
    proxy_pass http://127.0.0.1:2334;
}

# 其他仍走 Nitro
location /api/ {
    proxy_pass http://127.0.0.1:1007;
}

# Phase 2 完成后: 追加 galgame
location ~ ^/api/(auth|user|home|galgame) {
    proxy_pass http://127.0.0.1:2334;
}

# 全部完成后: 移除 Nitro
location /api/ {
    proxy_pass http://127.0.0.1:2334;
}
```

这允许逐模块上线, 降低风险。
