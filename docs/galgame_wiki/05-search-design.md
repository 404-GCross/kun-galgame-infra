# Galgame Wiki 搜索设计

## 背景 & 目标

wiki 后端（cmd/galgame，:9280）目前对 galgame 只有极简的 `?search=xxx` —— 对 4 个 `name_*` 列做 `ILIKE`，不支持 vndb_id / 别名 / tag / official 命中，不排序、不高亮、不支持 facet。tag / official 有 `/search` 但只会 LIKE 本表字段。

本设计为三个实体引入基于 Meilisearch 的搜索：

| Entity | Endpoint | Index uid | 文档数级别 |
|--------|----------|-----------|-----------|
| Galgame | `GET /galgame/search` | `galgames` | ~60k |
| Galgame Tag | `GET /tag/search` | `galgame_tags` | ~3k |
| Galgame Official | `GET /official/search` | `galgame_officials` | ~22k |

engines 数量太少（<200），前端已经全量拉取后客户端过滤，**不建 index**。

## 选型：为什么 Meilisearch

- **CJK 原生支持**（Charabia tokenizer，1.3+），无需 pg_jieba / MeCab
- **Typo tolerance + ranking + highlight + facet** 开箱即用
- **索引维护简单**（REST API 批量写），对 Go 有官方 SDK (`meilisearch-go`)
- 生产单二进制部署，资源开销小

## 基础设施

### 连接

```env
KUN_MEILISEARCH_HOST=http://127.0.0.1:7700
KUN_MEILISEARCH_API_KEY=                  # dev 留空，生产填 admin key
KUN_MEILISEARCH_INDEX_PREFIX=             # 可选：dev_ / staging_ 之类
```

`pkg/config` 加载，`internal/infrastructure/search/client.go` 初始化 SDK。

### Index 命名

生产：`galgames`、`galgame_tags`、`galgame_officials`。开发/测试带 prefix：`dev_galgames` 等。

## 文档 Schema

### galgames

```jsonc
{
  "id": 123,
  "vndb_id": "v17",
  "bid": 4567,
  "name_zh_cn": "...",
  "name_ja_jp": "...",
  "name_en_us": "...",
  "name_zh_tw": "...",
  "aliases": ["..."],
  "tag_names": ["校园", "日常"],
  "tag_ids": [1, 2],
  "official_names": ["Key", "ビジュアルアーツ"],
  "official_ids": [7],
  "engine_names": ["KiriKiri"],
  "engine_ids": [3],
  "series_id": 42,

  "intro_zh_cn": "...",            // 可选入搜
  "intro_ja_jp": "...",
  "intro_en_us": "...",
  "intro_zh_tw": "...",

  "banner": "https://...",
  "content_limit": "sfw",
  "age_limit": "r18",
  "original_language": "ja-jp",
  "status": 0,
  "view": 1234,
  "release_date": "2020-05-15",    // PR1：YYYY-MM-DD 或 "" 表示未知
  "release_date_tba": false,       // PR1：官方已宣布但日期未定
  "released_year": 2020,           // 派生自 release_date；release_date 为空则 null
  "released_ts": 1588291200,       // 派生自 release_date；缺失则 null
  "effective_banner_hash": "abcd1234...ef", // PR5：派生自 covers[sort_order=0]，null 表示无 image_service 封面
  "updated_ts": 1700000000,
  "created_ts": 1600000000
}
```

**Index settings**:

```jsonc
{
  "searchableAttributes": [        // 顺序 = 权重（前高后低）
    "vndb_id",                     // 精确匹配最高
    "name_zh_cn", "name_ja_jp", "name_en_us", "name_zh_tw",
    "aliases",
    "tag_names",
    "official_names"
    // intro_* 故意不列入默认 searchable，详见"intro 入搜策略"
  ],
  "filterableAttributes": [
    "status", "content_limit", "age_limit", "original_language",
    "released_year", "tag_ids", "official_ids", "engine_ids", "series_id"
  ],
  "sortableAttributes": [
    "released_ts", "view", "updated_ts", "created_ts"
  ],
  "rankingRules": [
    "words", "typo", "proximity", "attribute", "sort", "exactness",
    "view:desc"                    // 同分 tiebreaker：热门靠前
  ],
  "typoTolerance": {
    "minWordSizeForTypos": { "oneTypo": 4, "twoTypos": 8 },   // 默认 5/9 对短 CJK 太严
    "disableOnAttributes": ["vndb_id"]                          // vndb_id 精确匹配
  },
  "faceting": {
    "maxValuesPerFacet": 100
  }
}
```

### galgame_tags

```json
{
  "id": 45,
  "name": "校园",
  "aliases": ["学园", "School"],
  "category": "content",
  "galgame_count": 850
}
```

- `searchableAttributes`: `name`, `aliases`
- `filterableAttributes`: `category`
- `sortableAttributes`: `galgame_count`

### galgame_officials

```json
{
  "id": 7,
  "name": "Key",
  "original": "Key",
  "aliases": ["ビジュアルアーツ"],
  "category": "company",
  "lang": "ja",
  "galgame_count": 42
}
```

- `searchableAttributes`: `name`, `original`, `aliases`
- `filterableAttributes`: `category`, `lang`
- `sortableAttributes`: `galgame_count`

## API 设计

### `GET /galgame/search`

**Query params**:

| 参数 | 类型 | 默认 | 说明 |
|------|------|------|------|
| `q` | string | `""` | 搜索词；空时仅按 filter 返回 |
| `status` | int (csv) | `0` | 可 `0`、`0,1,2` 等 |
| `content_limit` | `sfw` \| `nsfw` | — | 可选 |
| `age_limit` | `all` \| `r18` | — | 可选 |
| `original_language` | string (csv) | — | `ja-jp,en-us` OR |
| `tag_ids` | int (csv) | — | `1,2,3` AND（必须全命中）|
| `official_ids` | int (csv) | — | 同上 |
| `engine_ids` | int (csv) | — | 同上 |
| `series_id` | int | — | 精确 |
| `released_from` | int | — | 年 |
| `released_to` | int | — | 年 |
| `include_intro` | bool | `false` | 入 `intro_*` 到 searchable（运行时搜索时切换 `attributesToSearchOn`）|
| `sort` | enum | `relevance` | `relevance` \| `released_desc` \| `released_asc` \| `view` \| `updated` |
| `page` | int | 1 | 1-based |
| `limit` | int | 24 | max 100 |
| `facets` | bool | `true` | 是否返回 facet 聚合 |
| `highlight` | bool | `true` | 是否返回高亮片段 |

**`q` 是纯文本，不是查询 DSL（`sanitizeQuery`）**：Meilisearch（v1.8+）在 `q` 里把前导 `-` 当作**取反算符**（`-word` = 排除含 word 的文档）、把 `"` 当作短语定界符，且**没有服务端开关可以关闭**。VNDB 标题大量使用 `-副标题-` 命名（如 `CRAZY CHA!N -エルピスの鎖-`）——用户原样粘贴标题搜索时，`-エルピスの鎖` 被解析成"排除 エルピスの鎖"，反而把要找的那部游戏排除掉 → 用游戏原名搜不到它。因此后端在进入 Meilisearch 前，对 galgame / tag / official 三个搜索的 `q` 统一做一次 `sanitizeQuery`：把 ASCII `-`、`"` 替换成空格（二者本就是分词分隔符，替换对匹配无损），再 TrimSpace。日文长音符 `ー`（U+30FC）是字母、非 ASCII `-`，不受影响。这是标题检索框，不是高级查询语法，不损失表达力。**纯查询层修复，改动即生效，不需要重建索引。**

**后端 → Meilisearch 转换**：

```jsonc
// filter 拼接规则（AND 连接；同字段多值 OR / IN）
"filter":
  "status IN [0] AND content_limit = 'sfw' AND tag_ids = 1 AND tag_ids = 2 AND released_year >= 2020"
"sort": ["view:desc"]            // relevance 时不传 sort
"facets": ["age_limit", "original_language"]
"attributesToHighlight": ["name_zh_cn","name_ja_jp","name_en_us","aliases"]
"highlightPreTag": "<mark>", "highlightPostTag": "</mark>"
"hitsPerPage": 24, "page": 1     // 用 page 模式，返回 exact totalHits
"attributesToSearchOn":          // 仅在 include_intro=true 时追加 intro_*
  ["vndb_id","name_zh_cn","name_ja_jp","name_en_us","name_zh_tw","aliases","tag_names","official_names","intro_zh_cn","intro_ja_jp","intro_en_us","intro_zh_tw"]
```

**响应**：

```json
{
  "items": [
    {
      "id": 123,
      "vndb_id": "v17",
      "name_zh_cn": "...",
      "banner": "...",
      "status": 0,
      "content_limit": "sfw",
      "age_limit": "r18",
      "original_language": "ja-jp",
      "release_date": "2020-05-15",
      "release_date_tba": false,
      "effective_banner_hash": "abcd1234...ef",
      "_formatted": {
        "name_zh_cn": "...<mark>关键字</mark>..."
      }
    }
  ],
  "total": 127,
  "facets": {
    "age_limit": {"all": 40, "r18": 87},
    "original_language": {"ja-jp": 100, "en-us": 20, "zh-Hans": 7}
  },
  "processing_time_ms": 12
}
```

### `GET /tag/search`

**Query**:

| 参数 | 类型 | 默认 | 说明 |
|------|------|------|------|
| `q` | string | `""` | 搜索词；空时返回按 `galgame_count` 倒序 top N |
| `category` | string | — | `content` / `sexual` / `technical` |
| `limit` | int | 50 | max 100 |

**响应**: `{ items: [...tag], total: int }`

### `GET /official/search`

**Query**:

| 参数 | 类型 | 默认 | 说明 |
|------|------|------|------|
| `q` | string | `""` | |
| `category` | string | — | `company` / `individual` / `amateur` |
| `lang` | string | — | `ja` / `en` / ... |
| `limit` | int | 50 | max 100 |

**响应**: `{ items: [...official], total: int }`

## intro 入搜策略

`intro_*` 默认**不参与**搜索：rationale —
- 简介里会出现大量非游戏本身的词（制作人名、其他作品名），容易把"正文提了某名字一次"的 VN 顶到前面
- 噪音高 + attribute 位置低 → 反而稀释 ranking

**但 `intro_zh_cn / ja_jp / en_us / zh_tw` 仍然写入文档**（只是不放在默认 `searchableAttributes` 里）。运行时通过 `?include_intro=true` 传入时，后端用 Meilisearch 的 `attributesToSearchOn` 临时扩大搜索字段，不需要改 index settings。

## 同步策略（Phase 1）

**Write-through + 启动 settings 自愈 + 手动全量重建**，无新组件，代码变更集中在 service 层。

### 1. Write-through

在 `galgame / tag / official` 的 Create / Update / Delete 成功后，goroutine 异步调 indexer：

```go
// 伪码
if err := repo.Create(ctx, g); err != nil {
    return nil, err
}
go func() {
    if err := indexer.UpsertGalgame(context.Background(), g); err != nil {
        slog.Warn("meilisearch upsert failed", "id", g.ID, "err", err)
    }
}()
return g, nil
```

- **失败只 log**，不阻塞主流程
- 不需要事务参与 MS（不是关键路径，索引稍微漂移可接受）
- 删除同理（Series 删除会把 galgame 的 series_id 清空 → 也要 upsert 那些 galgame 的文档）

**batch / script 路径不走 write-through**（sync-vndb / sync-vndb-relations / migrate-* 等）—— 避免每条记录一次 HTTP。这些脚本跑完后建议跑一次 `cmd/reindex-search` 触发全量重建。

### 2. 启动 settings 自愈

`cmd/galgame` 启动时调 `search.EnsureIndexes()`：
1. 检查 3 个 index 是否存在；不存在则创建（空的）
2. 对比当前 settings 与代码里声明的 settings；不一致则 PATCH
3. **不推数据** —— 只管 schema/settings，避免启动慢

### 3. 手动全量重建

新增 `cmd/reindex-search/main.go`：

```
go run ./cmd/reindex-search                  # 三个 index 全部重建
go run ./cmd/reindex-search --index=galgames # 只重建指定 index
```

批量流程：
1. `EnsureIndexes`（确保 settings）
2. `SELECT * FROM galgame ... LIMIT 1000 OFFSET N`，带 `Preload("Tag.Tag", "Official.Official", "Engine.Engine")`
3. 转成 `GalgameDoc` 数组，`index.AddDocumentsInBatches(docs, 1000, "id")`
4. tag / official 同理（更快，数量级小）

60k galgames + preloads 预估 1-3 分钟完成。

### 4. 失败恢复

MS 短暂宕机期间的写 → 漂移 → 下次 `cmd/reindex-search` 会修复。

**运维规则**（建议）：
- 首次部署：必跑一次 `cmd/reindex-search`
- sync-vndb / migrate-* 跑完：必跑一次 `cmd/reindex-search`
- 日常：每周低峰期 cron 跑一次（可选，对抗漂移）

## 架构分层

```
apps/api/
├── internal/infrastructure/search/
│   └── client.go              # Meilisearch client wrapper (SDK 连接、重试)
│
├── internal/platform/galgame/search/
│   ├── doc.go                 # GalgameDoc / TagDoc / OfficialDoc 定义
│   ├── indexer.go             # DB model → doc → MS upsert/delete
│   ├── query.go               # HTTP query params → MS SearchRequest
│   ├── service.go             # SearchGalgames / SearchTags / SearchOfficials
│   └── settings.go            # EnsureIndexes + settings 声明
│
├── internal/platform/galgame/handler/
│   ├── galgame_handler.go     # 新增 Search method
│   ├── tag_handler.go         # 重写 Search（走 MS）
│   └── official_handler.go    # 重写 Search（走 MS）
│
├── cmd/galgame/main.go        # 注入 SearchService + EnsureIndexes
└── cmd/reindex-search/main.go # 手动全量重建工具
```

## 配置改动

```go
// pkg/config/config.go
type Meilisearch struct {
    Host     string
    APIKey   string
    Prefix   string
}
// 读取 KUN_MEILISEARCH_HOST / KUN_MEILISEARCH_API_KEY / KUN_MEILISEARCH_INDEX_PREFIX
```

`.env` / `.env.example` 新增对应三行。

## 不做的事（明确范围）

- **不做**异步队列 / 脏标记表（Phase 2 备选，当 MS 宕机丢写成为实际问题时再上）
- **不做**实时搜索建议（`/suggest` 下拉）—— 现有 `/tag/search?q=` 已经足够快，未来加
- **不做** engines 的独立 index（数量少，客户端过滤足够）
- **不做**跨 index 联合搜索（搜一次同时返回 galgame + tag + official），每个 index 独立 endpoint
- **不做**写 MS 的原子性 / 事务保障 —— 接受最终一致性 + 定期 reindex 兜底

## Phase 2 路径（预留）

触发条件：MS 短时不可用 + 漂移数据导致业务问题重现
- 加 `search_index_queue` 表 + `cmd/worker` 异步消费
- Service 层写 queue 表而非直接发 MS
- Worker 批量消费、失败重试、死信
- 需要 3-5 天额外投入，当前不做
