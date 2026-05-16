# VNDB 数据同步现状

> 本文记录**当前代码实际做了什么**（现状参考），不是设计意图。
> 设计意图/决策追溯见 `docs/galgame_wiki/03-vndb-sync-design.md`（主表同步）
> 和 `docs/galgame_wiki/04-vndb-relations-sync-design.md`（关联回填）。
> 二者出现分歧时，**以本文 + 代码为准**。
>
> 代码位置：
> - `apps/api/cmd/sync-vndb/main.go`（主同步）
> - `apps/api/cmd/sync-vndb-relations/main.go`（关联回填）

---

## 1. 一句话总览

VNDB 同步**会**连带写入标签（tag）和制作会社（official/developer），但只在
**首次创建某 galgame** 的同一个事务里写一次；已存在的 galgame 一律整条跳过，
不做任何更新。系列、引擎、外链、标签/会社的别名与描述等**完全不同步**。

| 命令 | 职责 | 触发 | 对已存在条目 |
|---|---|---|---|
| `sync-vndb` | 拉 VN 主表 → 建 galgame + 顺带建标签/会社关联 | 定时 / 手动 | **整条跳过**（不更新） |
| `sync-vndb-relations` | 给「完全没有关联」的已存在 galgame 回填标签/会社关联 | 一次性补洞 | 只补「零关联」的，已有任意关联即跳过 |

---

## 2. `sync-vndb` —— 主同步

### 2.1 运行模式

| 模式 | 命令 | 行为 |
|---|---|---|
| 增量（默认） | `go run ./cmd/sync-vndb` | 从 DB 内最大有效 `vndb_id` 之后开始；并以 VNDB 实际最大 VN id 封顶，避免历史脏 `vndb_id` 触发 VNDB 400 |
| 全量 | `go run ./cmd/sync-vndb --full` | 从头遍历所有 VN |

- 限流：每请求间隔 2s，429 等 60s 重试（`main.go` 常量 `requestDelay`）。
- 结束后对 `galgame / galgame_alias / galgame_tag / galgame_official`
  做 `setval` 重置自增序列（`main.go:698` 附近）。

### 2.2 每条 VN 的处理判定（`processBatch`，`main.go:411`）

按顺序：

1. `vndb_id` 已在库 → **跳过**（`existingVNDBIDs`，`main.go:415`）——这是
   「只创建不更新」的根因。
2. `devstatus == 2`（VNDB 标记已取消）→ **跳过**（`main.go:421`）。
3. 否则 `insertVN`，**一个事务**内写入主表 + 别名 + 标签关联 + 会社关联
   （`main.go:439`）。

### 2.3 实际写入的字段（`buildGalgame`，`main.go:486`）

请求的 VNDB 字段集见 `main.go:37` 的 `vndbFields`。

| VNDB | Wiki | 说明 |
|---|---|---|
| `id` | `vndb_id` | 保留 `vXXXXX` |
| `titles[lang=en/ja/zh-Hans/zh-Hant]` | `name_en_us / name_ja_jp / name_zh_cn / name_zh_tw` | 按语言分发 |
| `aliases[]` | `galgame_alias` | 每项一行（空白项跳过） |
| `olang` | `original_language` | `mapLang`：ja→ja-jp 等 |
| `released` | `released` | 空 / `tba` → `"unknown"` |
| `description` | `intro_en_us` | VNDB 英文描述 |
| `image.url` | `banner` | 封面 URL（**原始 VNDB 图床地址，未走 image service**） |
| `image.sexual` | `content_limit` | `>=1` → `nsfw`，否则 `sfw` |
| — | `status` | 固定 `2`（草稿） |
| — | `user_id` | 固定 `1`（系统用户） |
| — | `age_limit` | 固定 `"r18"` |
| `devstatus` | — | `=2` 跳过，其余不落库 |

> 注意：`age_limit` 与 `content_limit` 是两套；`content_limit` 由
> `image.sexual` 决定，`age_limit` 恒为 `r18`（不读 VNDB）。

### 2.4 标签（`resolveTag`，`main.go:533`）

- 过滤：`tag.Lie == true` **或** `tag.Rating < 1.0`（`tagRatingMin`）→ 丢弃
  （`main.go:457`）。
- 命名优先级：
  1. tagMap 命中（英文→中文）且该中文名已在 `galgame_tag` → 复用；
  2. 否则英文名已在 `galgame_tag` → 复用；
  3. 否则**新建标签，`name` 用英文原名**（即便 tagMap 里有中文，新建时
     也只写英文——tagMap 只用于"查"已存在的中文标签，不用于"建"）。
- `category`：`cont→content / ero→sexual / tech→technical`（其余→content）。
- 关联：`INSERT galgame_tag_relation (... spoiler_level=tag.spoiler ...)
  ON CONFLICT DO NOTHING`。

### 2.5 制作会社 / Developer（`resolveOfficial`，`main.go:567`）

- 按 `dev.Name` 查 `galgame_official`，命中复用；`dev.Original` 非空时也按
  `Original` 查缓存。
- 未命中 → 新建：`Name = dev.Name`、`Category = mapDevType(dev.Type)`
  （`co→company / in→individual / ng→amateur`）、`Lang = dev.Lang`。
- **`dev.Original`（日文等原文名）在本命令里只用于查缓存，不写库**
  （`galgame_official.original` 保持空）。
- 关联：`INSERT galgame_official_relation ... ON CONFLICT DO NOTHING`。

---

## 3. `sync-vndb-relations` —— 关联回填（补洞）

存在原因：moyu 迁移来的 galgame（只导了主表 + `vndb_id`，无任何关联）以及
早期 `sync-vndb` 过滤掉的关联缺失。

### 3.1 选取范围（`loadCoverage`，`main.go:221`）

一条聚合查询算出每个 `vndb_id ~ ^v[0-9]+$` 的 galgame 是否
**完全没有** tag 关联 / **完全没有** official 关联：

```
EXISTS(SELECT 1 FROM galgame_tag_relation     WHERE galgame_id=g.id)  -- has_tag
EXISTS(SELECT 1 FROM galgame_official_relation WHERE galgame_id=g.id)  -- has_off
```

- `has_tag == false` → 进 `needTag` 队列；`has_off == false` → 进 `needOff`。
- 两者独立判断（一个 VN 可能只缺其一）。
- **关键：只要某 galgame 已有「任意一条」该类关联，就视为已覆盖，不会再补
  VNDB 后来新增的标签/会社。** 它是一次性堵漏，不是持续 diff/增量。

### 3.2 与 `sync-vndb` 的行为差异

| 项 | `sync-vndb` | `sync-vndb-relations` |
|---|---|---|
| 标签 rating 过滤 | 丢弃 `rating < 1.0` | **不设阈值**，只丢 `lie=true`（`main.go:372`） |
| 新建标签命名 | tagMap 命中也只用英文建 | tagMap 命中则**用中文名建**（`resolveTag`，`main.go:412`） |
| `official.original` | 不写 | **写入**，且对老记录空值做回填 `UPDATE`（`maybeBackfillOriginal`） |
| 主表/其他字段 | 创建时写 | 一律不动 |

> 后果：同一批数据，两条路径产出的标签集合/标签命名/会社 original 字段
> **不一致**。例如 `sync-vndb` 建的标签可能是英文名、`original` 为空；
> 而 relations 回填出来的可能是中文名、`original` 有值。低评分标签也只在
> relations 路径才会被挂上。

### 3.3 幂等性

- 所有关联写入 `ON CONFLICT DO NOTHING`（复合主键）。
- `original` 回填仅在原值为空时发生，重跑不反复覆盖。
- `needTag/needOff` 判定让重跑自动跳过已完成部分。
- 无 `--dry-run`，无 flag，行为唯一。

---

## 4. 两条路径都**不同步**的数据

以下 model 字段/表存在，但任何 VNDB 同步命令都不会写：

| 缺口 | 说明 |
|---|---|
| **已存在 galgame 的任何更新** | VNDB 后续改的标题/简介/发售日/devstatus、新增标签、新增会社、新增别名——都不回传。`sync-vndb` 跳过已存在条目；`sync-vndb-relations` 跳过已有任意关联的条目 |
| **系列 `GalgameSeries` / `series_id`** | 完全不同步（未拉取 VNDB relations） |
| **引擎 `GalgameEngine` + `galgame_engine_relation`** | 完全不同步（需查 release 接口，未实现） |
| **外链 `GalgameLink`** | 完全不同步 |
| **标签别名 `GalgameTagAlias`、标签 `Description`** | 不写 |
| **会社别名 `GalgameOfficialAlias`、会社 `Description`/`Link`** | 不写；`sync-vndb` 连 `original` 也不写，`sync-vndb-relations` 仅写 `original` |
| **banner 转存** | 直接存 VNDB 图床 URL，未经过本项目 image service |
| **Revision** | 草稿不建 revision（设计如此，发布时再建） |
| **devstatus=2（已取消）VN** | 整条不入库 |

---

## 5. 运行方式速查

```bash
cd apps/api

# 增量主同步（定时任务用这个）
go run ./cmd/sync-vndb

# 全量主同步（首次/重建）
go run ./cmd/sync-vndb --full

# 关联补洞（迁移导入新数据源后跑一次）
go run ./cmd/sync-vndb-relations
```

- 两者都连 `cfg.GalgameDatabase`（默认库 `kun_galgame_wiki`）。
- 两者都依赖 `docs/tagMap.ts`（`--tagmap` 可改路径；解析出 <100 条会
  直接报错退出，防路径错）。

---

## 6. 已知风险 / 待决策（仅记录，不在本文范围内修改）

1. **无真正增量更新**：已入库 galgame 的 VNDB 侧变更永远不同步。
2. **两命令标签过滤/命名不一致**（见 §3.2），同源数据结果分叉。
3. **系列/引擎/外链从未同步**，这些维度的数据只能靠用户手填或后续补脚本。
4. banner 未转存 image service，依赖 VNDB 图床可用性。

> 如需改变上述行为（增量 upsert、对齐过滤、补系列/引擎同步等），属于行为
> 变更，应先定方案再实施，不在「记录现状」这一步内处理。
