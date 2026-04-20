# VNDB 数据同步设计

## 概述

全量同步 VNDB 的 Visual Novel 数据到 galgame wiki 数据库，作为"预填充草稿"。用户创建游戏时从草稿中选择并发布，而非手动填写。

## 数据流

```
VNDB API (POST /vn) → sync-vndb 脚本 → kun_galgame_wiki DB (status=2 草稿)
                                                ↓
                                        用户选择 → 发布 (status=0)
```

## 同步模式

| 模式 | 触发方式 | 行为 |
|------|----------|------|
| `--full` | 手动 | 从 v1 开始遍历所有 VN |
| 默认（增量） | 定时任务 | 从 DB 中最大 vndb_id 之后开始 |

## 字段映射

### Galgame 主表

| VNDB 字段 | Wiki 字段 | 说明 |
|-----------|-----------|------|
| `id` | `vndb_id` | 保留 "vXXXXX" 格式 |
| `titles` (lang=en) | `name_en_us` | 英文标题 |
| `titles` (lang=ja) | `name_ja_jp` | 日文标题 |
| `titles` (lang=zh-Hans) | `name_zh_cn` | 简体中文标题 |
| `titles` (lang=zh-Hant) | `name_zh_tw` | 繁体中文标题 |
| `title` | 备用 | 无对应语言标题时用作 fallback |
| `aliases` | `galgame_alias` | 字符串数组 → 每项一行 |
| `olang` | `original_language` | ja→ja-jp, en→en-us 等 |
| `released` | `released` | null/tba → "unknown" |
| `description` | `intro_en_us` | VNDB 描述（英文） |
| `image.url` | `banner` | 封面图 URL |
| `image.sexual` | `content_limit` | ≥1 → nsfw, 否则 sfw |
| `devstatus` | — | =2 时跳过（已取消） |
| — | `status` | 固定为 2（草稿） |
| — | `user_id` | 固定为 1（系统用户） |

### Tag 关联

| VNDB 字段 | Wiki 处理 |
|-----------|-----------|
| `tags[].name` | 通过 tagMap.ts 查中文名 → 匹配 `galgame_tag` |
| `tags[].category` | cont→content, ero→sexual, tech→technical |
| `tags[].rating` | < 1.0 的跳过（过滤噪声） |
| `tags[].spoiler` | → `galgame_tag_relation.spoiler_level` |
| 无匹配 | 新建 tag，使用英文原名 |

### Developer/Official 关联

| VNDB 字段 | Wiki 处理 |
|-----------|-----------|
| `developers[].name` | 匹配 `galgame_official.name` |
| `developers[].type` | co→company, in→individual, ng→amateur |
| `developers[].lang` | → `galgame_official.lang` |
| 无匹配 | 新建 official |

### 不同步的内容

- Revision（草稿不创建 revision，发布时再建）
- Engine（需查 release 接口，后续补充）
- Bangumi 数据（后续独立同步）

## 性能估算

- VNDB 约 40000-50000 条 VN
- 每次请求 100 条，需 400-500 次请求
- 限流 200 次/5 分钟，每次间隔 2 秒
- 全量约 15-20 分钟完成

## status 语义变更

| status | 含义 |
|--------|------|
| 0 | 已发布（公开 API 可见） |
| 1 | 封禁 |
| 2 | 草稿（VNDB 同步，未发布） |

公开 API 查询条件从 `status != 1` 改为 `status = 0`。
