# Go 后端架构模式参考

> 本文档供 Claude Code 在实现新模块时参考，确保与已有代码风格一致。

## 1. 模块目录结构

每个业务模块遵循 5 层结构：

```
internal/{module}/
├── model/        # GORM 模型 (TableName + 字段标签)
├── dto/          # 请求/响应 DTO (validate 标签)
├── repository/   # 数据库操作 (纯 GORM, 不含业务逻辑)
├── service/      # 业务逻辑 (调用 repo, 事务, 萌萌点)
└── handler/      # HTTP 处理 (解析请求, 调用 service, 返回响应)
```

## 2. Handler 模式

```go
func (h *XxxHandler) DoSomething(c *fiber.Ctx) error {
    // 1. 认证 (如需)
    user, appErr := middleware.MustGetUser(c)
    if appErr != nil {
        return response.Error(c, appErr)
    }

    // 2. 路径参数
    id, err := strconv.Atoi(c.Params("id"))
    if err != nil {
        return response.Error(c, errors.ErrBadRequest("无效的 ID"))
    }

    // 3. 请求体/查询参数验证
    var req dto.XxxRequest
    if appErr := utils.ParseAndValidate(c, &req); appErr != nil {
        return response.Error(c, appErr)
    }
    // 或 GET 请求: utils.ParseQueryAndValidate(c, &req)

    // 4. 调用 service
    result, appErr := h.xxxService.DoSomething(c.Context(), user.UID, &req)
    if appErr != nil {
        return response.Error(c, appErr)
    }

    // 5. 返回
    return response.OK(c, result)
    // 或分页: response.Paginated(c, items, total)
    // 或消息: response.OKMessage(c, "操作成功")
}
```

## 3. Service 模式

```go
type XxxService struct {
    xxxRepo  *repository.XxxRepository
    rdb      *redis.Client
    // 按需注入其他依赖
}

func (s *XxxService) DoSomething(ctx context.Context, uid int, req *dto.XxxRequest) (*dto.XxxResponse, *errors.AppError) {
    // 业务逻辑
    // 返回 *errors.AppError 而非 Go 原生 error
}
```

### 事务模式

```go
func (s *XxxService) CreateWithSideEffects(ctx context.Context, uid int, req *dto.CreateRequest) (*dto.Response, *errors.AppError) {
    var result *dto.Response
    err := s.xxxRepo.DB().Transaction(func(tx *gorm.DB) error {
        // 1. 创建主记录
        item := &model.Xxx{...}
        if err := tx.Create(item).Error; err != nil {
            return err
        }

        // 2. 创建关联记录 (如标签、贡献者等)
        for _, tagID := range req.TagIDs {
            if err := tx.Create(&model.XxxTagRelation{
                XxxID: item.ID,
                TagID: tagID,
            }).Error; err != nil {
                return err
            }
        }

        // 3. 更新萌萌点
        if err := tx.Model(&userModel.User{}).
            Where("id = ?", uid).
            Update("moemoepoint", gorm.Expr("moemoepoint + ?", 3)).
            Error; err != nil {
            return err
        }

        result = &dto.Response{ID: item.ID, ...}
        return nil
    })

    if err != nil {
        return nil, errors.ErrInternal("创建失败")
    }
    return result, nil
}
```

## 4. Repository 模式

```go
type XxxRepository struct {
    db *gorm.DB
}

func NewXxxRepository(db *gorm.DB) *XxxRepository {
    return &XxxRepository{db: db}
}

// DB 暴露底层连接供 service 层使用事务
func (r *XxxRepository) DB() *gorm.DB {
    return r.db
}

func (r *XxxRepository) FindByID(id int) (*model.Xxx, error) {
    var item model.Xxx
    err := r.db.First(&item, id).Error
    return &item, err
}

func (r *XxxRepository) FindList(page, limit int, sortField string) ([]model.Xxx, int64, error) {
    var items []model.Xxx
    var total int64

    query := r.db.Model(&model.Xxx{})
    query.Count(&total)

    err := query.
        Order(sortField + " DESC").
        Offset((page - 1) * limit).
        Limit(limit).
        Find(&items).Error

    return items, total, err
}
```

## 5. DTO 模式

```go
// 请求 DTO — 用 validate 标签
type CreateXxxRequest struct {
    Name    string `json:"name" validate:"required,min=1,max=100"`
    Content string `json:"content" validate:"required,max=10000"`
    TagIDs  []int  `json:"tag_ids" validate:"max=20"`
}

// 查询 DTO — 用 query 标签
type ListXxxRequest struct {
    Page    int    `query:"page" validate:"min=1"`
    Limit   int    `query:"limit" validate:"min=1,max=50"`
    Sort    string `query:"sort" validate:"omitempty,oneof=created updated like_count"`
}

// 响应 DTO — 只包含需要返回的字段
type XxxCard struct {
    ID        int       `json:"id"`
    Name      string    `json:"name"`
    LikeCount int       `json:"like_count"`
    CreatedAt time.Time `json:"created"`
}
```

## 6. 路由注册模式

在 `internal/app/router.go` 中按模块分组：

```go
// ── Xxx routes (public) ──
api.Get("/xxx", a.XxxHandler.GetList)
api.Get("/xxx/:id", a.XxxHandler.GetDetail)

// ── Xxx routes (authenticated) ──
authed.Post("/xxx", a.XxxHandler.Create)
authed.Put("/xxx/:id", a.XxxHandler.Update)
authed.Delete("/xxx/:id", a.XxxHandler.Delete)
authed.Put("/xxx/:id/like", a.XxxHandler.ToggleLike)
```

## 7. App 注册模式

在 `internal/app/app.go` 中按依赖顺序注册：

```go
// 1. Repository
xxxRepo := xxxRepository.NewXxxRepository(db)

// 2. Service
xxxService := xxxService.NewXxxService(xxxRepo, rdb)

// 3. Handler
xxxHandler := xxxHandler.NewXxxHandler(xxxService)

// 4. 挂到 App 结构体
app := &App{
    ...
    XxxHandler: xxxHandler,
}
```

## 8. 互动 Toggle 模式 (like/favorite/follow)

```go
func (s *XxxService) ToggleLike(ctx context.Context, uid, targetID int) (bool, *errors.AppError) {
    err := s.xxxRepo.DB().Transaction(func(tx *gorm.DB) error {
        var existing model.XxxLike
        result := tx.Where("user_id = ? AND xxx_id = ?", uid, targetID).First(&existing)

        if result.Error == gorm.ErrRecordNotFound {
            // 添加
            if err := tx.Create(&model.XxxLike{UserID: uid, XxxID: targetID}).Error; err != nil {
                return err
            }
            return tx.Model(&model.Xxx{}).Where("id = ?", targetID).
                Update("like_count", gorm.Expr("like_count + 1")).Error
        }
        // 取消
        if err := tx.Delete(&existing).Error; err != nil {
            return err
        }
        return tx.Model(&model.Xxx{}).Where("id = ?", targetID).
            Update("like_count", gorm.Expr("like_count - 1")).Error
    })

    if err != nil {
        return false, errors.ErrInternal("操作失败")
    }
    return true, nil // 返回是否为新增点赞
}
```

## 9. 萌萌点奖励规则

来自 Nitro 代码的业务规则：

| 操作 | 奖励 | 消耗 |
|------|------|------|
| 创建 Galgame | +3 | - |
| 创建 Topic | +3 | -10 (如果创建新 section) |
| 创建 Resource | +3 | - |
| 创建 Toolset | +3 | - |
| 创建 Rating | +3/5/10 | - (按评语长度) |
| 回复/被提及 | +1 | - |
| PR 被合并 | +1 | - |
| 每日签到 | 0~7 (随机) | - |
| 修改用户名 | - | -17 |
| 每日发帖上限 | (moemoepoint / 10) + 1 | - |

## 10. 消息通知模式

操作产生副作用时需创建消息记录：

```go
// 在 service 层的事务中
msg := &messageModel.Message{
    Type:       "liked",
    Content:    "",
    SenderID:   uid,
    ReceiverID: targetUserID,
    TopicID:    &topicID,    // 可选, 关联到话题
    GalgameID:  &galgameID,  // 可选, 关联到 galgame
}
if err := tx.Create(msg).Error; err != nil {
    return err
}
```

消息类型: `replied`, `liked`, `upvoted`, `mentioned`, `requested`, `merged`, `declined`

## 11. 已知 Nitro 特殊逻辑

### Galgame PR 工作流
- galgame 创建者或 admin (role >= 3): 直接更新
- 其他用户: 创建 PR 待审核
- PR merge 时: 更新主记录 + 贡献者列表 + 历史记录 + 萌萌点奖励 + 消息通知

### Topic 发帖限制
- 每日上限: `(user.moemoepoint / 10) + 1` 篇
- 创建新 section 需消耗 10 萌萌点

### Topic Reply 楼层计算
- 新回复的 floor 等于 `topic.reply_count + 1`（事务内递增）

### Markdown → HTML
- 需要: goldmark + GFM + KaTeX + syntax highlighting
- 自定义处理: lazy image, code block wrapper, h1→h2, table wrapper, wbr insertion, video embed (kv:url), spoiler (||text||)

### VNDB 同步
- 创建/更新 galgame 时检查 vndb_id 格式 (v\d+)
- PR merge 时如果 vndb_id 变更需重新同步

## 12. 前端请求约定

Go API 的统一响应格式:

```json
// 成功
{"code": 0, "message": "成功", "data": {...}}

// 分页
{"code": 0, "message": "成功", "data": {"items": [...], "total": 42}}

// 错误
{"code": 205, "message": "用户登录失效"}   // → 前端跳转登录
{"code": 233, "message": "您今天已经签到过了"} // → 前端显示消息
```

前端使用 `kunFetch` (imperative) 和 `useKunFetch` (SSR composable) 两种方式调用。
