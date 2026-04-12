# Nitro 端点完整参考

> 供实现 Go 端点时快速查找对应 Nitro 源码

## 目录

所有 Nitro 源码位于 `apps/nitro-server/api/`

## 按模块索引

### auth (4 文件)
```
auth/email/code/register.post.ts   → 发送注册验证码
auth/email/code/forgot.post.ts     → 发送找回密码验证码
auth/email/code/reset.post.ts      → 发送改邮箱验证码
auth/password/reset.post.ts        → 重置密码
```

### user (18 文件)
```
user/register.post.ts              → 注册 (已被 OAuth 替代)
user/login.post.ts                 → 登录 (已被 OAuth 替代)
user/status.get.ts                 → 在线状态
user/email.get.ts                  → 获取脱敏邮箱
user/email.put.ts                  → 更新邮箱
user/username.put.ts               → 修改用户名 (-17 萌萌点)
user/password.put.ts               → 修改密码 (已被 OAuth 替代)
user/bio.put.ts                    → 更新签名
user/avatar.post.ts                → 上传头像
user/check-in.post.ts              → 每日签到
user/[uid]/index.get.ts            → 用户资料
user/[uid]/topics.get.ts           → 用户话题列表
user/[uid]/replies.get.ts          → 用户回复列表
user/[uid]/comments.get.ts         → 用户评论列表
user/[uid]/galgames.get.ts         → 用户 galgame 列表
user/[uid]/resources.get.ts        → 用户资源贡献
user/[uid]/ratings.get.ts          → 用户评分列表
user/[uid]/ban.put.ts              → 封禁用户
user/[uid]/permanent.delete.ts     → 删除用户
```

### galgame (20 文件)
```
galgame/index.get.ts               → 列表 (筛选+分页)
galgame/index.post.ts              → 创建 (事务, +3 萌萌点)
galgame/check.get.ts               → 检查 vndb_id 是否存在
galgame/[gid]/index.get.ts         → 详情 (10+ 关联)
galgame/[gid]/banner.put.ts        → 更新 banner (S3)
galgame/[gid]/like.put.ts          → 点赞 toggle
galgame/[gid]/favorite.put.ts      → 收藏 toggle
galgame/[gid]/contributor.put.ts   → 贡献者管理
galgame/[gid]/comment/index.get.ts → 评论列表
galgame/[gid]/comment/index.post.ts→ 发表评论
galgame/[gid]/comment/like.put.ts  → 评论点赞
galgame/[gid]/comment/index.delete.ts → 删除评论
galgame/[gid]/link/index.get.ts    → 链接列表
galgame/[gid]/link/index.post.ts   → 添加链接
galgame/[gid]/link/index.delete.ts → 删除链接
galgame/[gid]/history/index.get.ts → 编辑历史
galgame/[gid]/resource/all.get.ts  → 资源列表
galgame/[gid]/resource/index.post.ts → 添加资源 (+3 萌萌点)
galgame/[gid]/resource/index.put.ts  → 编辑资源
galgame/[gid]/resource/index.delete.ts → 删除资源
galgame/[gid]/resource/like.put.ts    → 资源点赞
galgame/[gid]/resource/valid.put.ts   → 标记有效
galgame/[gid]/resource/expired.put.ts → 标记过期
galgame/[gid]/pr/index.get.ts     → PR 详情
galgame/[gid]/pr/all.get.ts       → PR 列表
galgame/[gid]/pr/index.post.ts    → 提交 PR
galgame/[gid]/pr/merge.put.ts     → 合并 PR (最复杂)
galgame/[gid]/pr/decline.put.ts   → 拒绝 PR
```

### galgame-rating (8 文件)
```
galgame-rating/index.post.ts       → 创建评分 (+3/5/10 萌萌点)
galgame-rating/all.get.ts          → 评分列表
galgame-rating/[id]/index.get.ts   → 评分详情
galgame-rating/[id]/index.put.ts   → 编辑评分
galgame-rating/[id]/index.delete.ts→ 删除评分
galgame-rating/[id]/like.put.ts    → 评分点赞
galgame-rating/[id]/comment/index.get.ts  → 评分评论列表
galgame-rating/[id]/comment/index.post.ts → 发表评论
galgame-rating/[id]/comment/index.delete.ts → 删除评论
```

### galgame-series (7 文件)
```
galgame-series/index.get.ts        → 系列列表
galgame-series/index.post.ts       → 创建系列
galgame-series/search.get.ts       → 搜索系列
galgame-series/modal.post.ts       → 模态创建
galgame-series/[id]/index.get.ts   → 系列详情
galgame-series/[id]/index.put.ts   → 编辑系列
galgame-series/[id]/index.delete.ts→ 删除系列
```

### galgame-tag (5 文件)
```
galgame-tag/index.get.ts           → 标签列表
galgame-tag/[name].get.ts          → 按名查询
galgame-tag/search.get.ts          → 搜索标签
galgame-tag/multi.get.ts           → 批量查询
galgame-tag/index.put.ts           → 更新标签
```

### galgame-engine (3 文件)
```
galgame-engine/index.get.ts        → 引擎列表
galgame-engine/[name].get.ts       → 按名查询
galgame-engine/index.put.ts        → 更新引擎
```

### galgame-official (4 文件)
```
galgame-official/index.get.ts      → 开发商列表
galgame-official/[name].get.ts     → 按名查询
galgame-official/search.get.ts     → 搜索开发商
galgame-official/index.put.ts      → 更新开发商
```

### galgame-resource (4 文件)
```
galgame-resource/index.get.ts      → 资源浏览列表
galgame-resource/[id]/index.get.ts → 资源详情
galgame-resource/[id]/detail.get.ts→ 扩展详情
galgame-resource/[id]/recommend.get.ts → 推荐资源
```

### topic (28 文件)
```
topic/index.get.ts                 → 列表
topic/index.post.ts                → 创建 (section + 发帖限制)
topic/[tid]/index.get.ts           → 详情
topic/[tid]/index.put.ts           → 编辑
topic/[tid]/index.delete.ts        → 删除
topic/[tid]/like.put.ts            → 点赞
topic/[tid]/dislike.put.ts         → 踩
topic/[tid]/upvote.put.ts          → 推
topic/[tid]/favorite.put.ts        → 收藏
topic/[tid]/hide.put.ts            → 隐藏
topic/[tid]/best-answer.put.ts     → 最佳回答
topic/[tid]/reply/index.get.ts     → 回复列表
topic/[tid]/reply/index.post.ts    → 发表回复 (楼层+消息)
topic/[tid]/reply/index.put.ts     → 编辑回复
topic/[tid]/reply/index.delete.ts  → 删除回复
topic/[tid]/reply/detail.get.ts    → 回复详情
topic/[tid]/reply/like.put.ts      → 回复点赞
topic/[tid]/reply/dislike.put.ts   → 回复踩
topic/[tid]/comment/index.post.ts  → 评论
topic/[tid]/comment/like.put.ts    → 评论点赞
topic/[tid]/comment/index.delete.ts→ 删除评论
topic/[tid]/poll/index.post.ts     → 创建投票
topic/[tid]/poll/index.get.ts      → 获取投票
topic/[tid]/poll/index.put.ts      → 编辑投票
topic/[tid]/poll/index.delete.ts   → 删除投票
topic/[tid]/poll/vote.post.ts      → 投票
topic/[tid]/poll/log.get.ts        → 投票记录
topic/[tid]/poll/topic.get.ts      → 话题的投票
```

### toolset (17 文件)
```
toolset/index.get.ts               → 列表
toolset/index.post.ts              → 创建 (+3 萌萌点)
toolset/[id]/index.get.ts          → 详情
toolset/[id]/index.put.ts          → 编辑
toolset/[id]/index.delete.ts       → 删除
toolset/[id]/practicality.get.ts   → 评分分布
toolset/[id]/practicality.put.ts   → 提交评分
toolset/[id]/comment/index.get.ts  → 评论列表
toolset/[id]/comment/index.post.ts → 发表评论
toolset/[id]/comment/like.put.ts   → 评论点赞
toolset/[id]/comment/index.delete.ts → 删除评论
toolset/[id]/resource/index.post.ts  → 创建资源
toolset/[id]/resource/index.put.ts   → 编辑资源
toolset/[id]/resource/index.delete.ts→ 删除资源
toolset/[id]/resource/detail.get.ts  → 资源详情
toolset/[id]/upload/small.post.ts    → 小文件上传
toolset/[id]/upload/large.post.ts    → 大文件上传
toolset/[id]/upload/complete.post.ts → 完成分片
toolset/[id]/upload/abort.post.ts    → 取消分片
```

### message (8 文件)
```
message/index.get.ts               → 消息列表
message/[id]/index.delete.ts       → 删除消息
message/admin/index.get.ts         → 管理员消息
message/admin/read.put.ts          → 标记已读
message/nav/contact.get.ts         → 联系人摘要
message/nav/system.get.ts          → 系统消息
message/system/read.put.ts         → 标记系统消息已读
message/chat/history.get.ts        → 聊天历史
```

### website (8 文件)
```
website/index.get.ts               → 列表
website/index.post.ts              → 创建
website/[domain]/index.get.ts      → 详情
website/[domain]/index.put.ts      → 编辑
website/[domain]/index.delete.ts   → 删除
website/[domain]/like.put.ts       → 点赞
website/[domain]/favorite.put.ts   → 收藏
website/[domain]/comment/*         → 评论 CRUD
```

### website-category (2 文件)
```
website-category/[name].get.ts     → 按名查询
website-category/index.put.ts      → 更新
```

### website-tag (4 文件)
```
website-tag/index.get.ts           → 列表
website-tag/index.post.ts          → 创建
website-tag/index.put.ts           → 更新
website-tag/index.delete.ts        → 删除
```

### doc (12 文件)
```
doc/article/index.get.ts           → 文章列表
doc/article/index.post.ts          → 创建文章
doc/article/index.put.ts           → 编辑文章
doc/article/index.delete.ts        → 删除文章
doc/article/[slug].get.ts          → 按 slug 获取
doc/category/index.get.ts          → 分类列表
doc/category/index.post.ts         → 创建分类
doc/category/index.put.ts          → 编辑分类
doc/category/index.delete.ts       → 删除分类
doc/tag/index.get.ts               → 标签列表
doc/tag/index.post.ts              → 创建标签
doc/tag/index.delete.ts            → 删除标签
```

### admin (6 文件)
```
admin/overview/all.get.ts          → 总览
admin/overview/stats.get.ts        → 统计
admin/setting/register.get.ts      → 获取设置
admin/setting/register.put.ts      → 更新设置
admin/user/index.get.ts            → 用户列表
admin/user/search.get.ts           → 搜索用户
```

### 其他小模块
```
home/index.get.ts                  → 首页数据
ranking/galgame.get.ts             → Galgame 排行
ranking/topic.get.ts               → Topic 排行
ranking/user.get.ts                → 用户排行
activity/index.get.ts              → 活动流
activity/timeline.get.ts           → 时间线
search/index.get.ts                → 统一搜索
report/submit.post.ts              → 举报
rss/galgame.get.ts                 → Galgame RSS
rss/topic.get.ts                   → Topic RSS
image/topic.post.ts                → 图片上传
resource/index.get.ts              → 资源首页
section/index.get.ts               → 板块列表
category/index.get.ts              → 分类列表
unmoe/index.get.ts                 → Unmoe 翻译器
update/history/*.ts                → 更新日志 CRUD (3 文件)
update/todo/*.ts                   → Todo CRUD (3 文件)
```

## 工具函数参考

关键业务逻辑所在的工具函数:

```
utils/zod.ts                       → Zod 验证解析器 (5 个函数)
utils/kunError.ts                  → 统一错误响应
utils/getCookieTokenInfo.ts        → JWT 认证
utils/message.ts                   → 创建消息通知 (含去重)
utils/galgameHistory.ts            → Galgame 历史记录
utils/remark/markdownToHtml.ts     → Markdown → HTML
utils/providerClassifier.ts        → 资源 Provider 检测
utils/activityFetchers.ts          → 活动流查询
utils/sendVerificationCodeEmail.ts → 发送验证码
utils/upload/canUserUpload.ts      → 上传限制
```
