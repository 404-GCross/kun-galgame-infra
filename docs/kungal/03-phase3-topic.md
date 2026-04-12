# Phase 3: Topic 论坛模块实现计划

> 预计端点数: ~28 | 前置: Phase 1 (已完成)

## 模块结构

```
internal/topic/
├── model/topic.go            ✅ 已定义 (276 行, 15+ 模型)
├── dto/
│   ├── topic_dto.go          ❌
│   ├── reply_dto.go          ❌
│   ├── comment_dto.go        ❌
│   └── poll_dto.go           ❌
├── repository/
│   ├── topic_repo.go         ❌
│   ├── reply_repo.go         ❌
│   └── poll_repo.go          ❌
├── service/
│   ├── topic_service.go      ❌
│   ├── reply_service.go      ❌
│   └── poll_service.go       ❌
└── handler/
    ├── topic_handler.go      ❌
    ├── reply_handler.go      ❌
    └── poll_handler.go       ❌
```

## 实现顺序

### Step 1: Topic 基础 CRUD (5 端点)

| HTTP | 路径 | 说明 | Nitro 文件 |
|------|------|------|-----------|
| GET | /api/topic | 列表 | api/topic/index.get.ts |
| GET | /api/topic/:tid | 详情 | api/topic/[tid]/index.get.ts |
| POST | /api/topic | 创建话题 | api/topic/index.post.ts |
| PUT | /api/topic/:tid | 编辑话题 | api/topic/[tid]/index.put.ts |
| DELETE | /api/topic/:tid | 删除话题 | api/topic/[tid]/index.delete.ts |

**列表逻辑:**
- 筛选: section, category, tag, sortField(created/updated/view/like_count)
- 状态过滤: 排除 status=1(被封) 除非管理员
- 分页: page + limit
- 返回: 话题卡片 (标题, 用户, section, tags, 各种 count, 时间)

**详情逻辑:**
- 主记录 + 关联:
  - 作者信息 (name, avatar)
  - Tags (通过 TopicTagRelation)
  - Section
  - 如果已登录: 是否已点赞/踩/收藏/推过
- 浏览量 +1

**创建逻辑 (复杂):**
```
事务:
1. 检查每日发帖上限: (user.moemoepoint / 10) + 1
2. 如果 section 不存在:
   - 检查萌萌点 >= 10
   - 创建新 section
   - 用户萌萌点 -10
3. 否则: 用户萌萌点 +3
4. 创建 topic 记录
5. 创建 tag 关联 (TopicTagRelation)
6. 创建 section 关联 (TopicSectionRelation)
7. Markdown → HTML 转换 (存储 HTML)
```

### Step 2: Topic 互动 (5 端点)

| HTTP | 路径 | 说明 |
|------|------|------|
| PUT | /api/topic/:tid/like | 点赞 (toggle) |
| PUT | /api/topic/:tid/dislike | 踩 (toggle) |
| PUT | /api/topic/:tid/upvote | 推话题 (可重复) |
| PUT | /api/topic/:tid/favorite | 收藏 (toggle) |
| PUT | /api/topic/:tid/hide | 隐藏 (status toggle) |

**注意:**
- like 和 dislike 互斥: 点赞时自动取消踩, 反之亦然
- upvote 不是 toggle, 可以重复推 (每次消耗萌萌点?)
- like/dislike/upvote 都会创建消息通知

### Step 3: Topic Reply (8 端点)

| HTTP | 路径 | 说明 |
|------|------|------|
| GET | /api/topic/:tid/reply | 回复列表 (分页) |
| GET | /api/topic/:tid/reply/detail | 单条回复详情 |
| POST | /api/topic/:tid/reply | 发表回复 |
| PUT | /api/topic/:tid/reply | 编辑回复 |
| DELETE | /api/topic/:tid/reply | 删除回复 |
| PUT | /api/topic/:tid/reply/like | 回复点赞 |
| PUT | /api/topic/:tid/reply/dislike | 回复踩 |
| PUT | /api/topic/:tid/best-answer | 设置最佳回答 |

**发表回复 (复杂):**
```
事务:
1. 计算楼层: topic.reply_count + 1
2. 创建 TopicReply 记录
3. topic.reply_count +1
4. Markdown → HTML 转换
5. 如果有 target (回复某人):
   - 创建 TopicReplyTarget 记录
   - 创建消息通知被回复者 (type=replied, +1 萌萌点)
6. 创建消息通知话题作者 (如果不是自己回复自己)
```

**最佳回答:**
- 只有话题作者可以设置
- 更新 topic.best_reply_id

### Step 4: Topic Comment (3 端点)

| HTTP | 路径 | 说明 |
|------|------|------|
| POST | /api/topic/:tid/comment | 发表评论 |
| PUT | /api/topic/:tid/comment/like | 评论点赞 |
| DELETE | /api/topic/:tid/comment | 删除评论 |

### Step 5: Topic Poll 投票 (7 端点)

| HTTP | 路径 | 说明 |
|------|------|------|
| POST | /api/topic/:tid/poll | 创建投票 |
| GET | /api/topic/:tid/poll | 获取投票 |
| PUT | /api/topic/:tid/poll | 编辑投票 |
| DELETE | /api/topic/:tid/poll | 删除投票 |
| POST | /api/topic/:tid/poll/vote | 投票 |
| GET | /api/topic/:tid/poll/log | 投票记录 |
| GET | /api/topic/:tid/poll/topic | 话题的投票 |

**投票约束:**
- 单选 vs 多选 (max_choice)
- 截止时间检查
- 是否允许改票 (can_change_vote)
- 结果可见性控制 (result_visibility)
- 事务内: 创建 vote + option.vote_count +1

## Nitro 文件参考

```
topic/
├── index.get.ts              → 列表筛选
├── index.post.ts             → 创建 (section + 发帖限制)
└── [tid]/
    ├── index.get.ts          → 详情 (互动状态)
    ├── index.put.ts          → 编辑
    ├── index.delete.ts       → 删除
    ├── like.put.ts           → 点赞
    ├── dislike.put.ts        → 踩
    ├── upvote.put.ts         → 推
    ├── favorite.put.ts       → 收藏
    ├── hide.put.ts           → 隐藏
    ├── best-answer.put.ts    → 最佳回答
    ├── reply/
    │   ├── index.get.ts      → 回复列表
    │   ├── index.post.ts     → 发表回复 (最复杂)
    │   ├── index.put.ts      → 编辑
    │   ├── index.delete.ts   → 删除
    │   ├── detail.get.ts     → 单条详情
    │   ├── like.put.ts       → 回复点赞
    │   └── dislike.put.ts    → 回复踩
    ├── comment/              → 评论 CRUD
    └── poll/                 → 投票系统 (7 文件)
```

## 预估工作量

| Step | 端点数 | 复杂度 | 关键难点 |
|------|--------|--------|---------|
| 1. 基础 CRUD | 5 | ⭐⭐⭐⭐ | 创建事务 (section + 发帖限制) |
| 2. 互动 | 5 | ⭐⭐ | like/dislike 互斥, upvote 特殊 |
| 3. 回复 | 8 | ⭐⭐⭐⭐ | 楼层计算, 消息通知, Markdown |
| 4. 评论 | 3 | ⭐⭐ | 同 galgame comment |
| 5. 投票 | 7 | ⭐⭐⭐ | 多选/单选约束, 截止时间 |
