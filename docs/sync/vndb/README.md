# VNDB 数据同步现状

> 本文记录**当前代码实际做了什么**（现状参考），不是设计意图。
> 设计意图/决策追溯见 `docs/galgame_wiki/03-vndb-sync-design.md`（草稿同步）
> 和 `docs/galgame_wiki/04-vndb-relations-sync-design.md`（关联同步，部分已被本文取代）。
> 二者出现分歧时，**以本文 + 代码为准**。
>
> 代码位置：
> - `apps/api/internal/jobs/vndbsync`（草稿同步，`cmd/sync-vndb` 薄壳）
> - `apps/api/internal/jobs/vndbenrich`（已发布游戏富集，`cmd/sync-vndb-enrich` 薄壳）
> - `apps/api/internal/platform/galgame/vndbresolve`（共享的 tag/official 解析）
> - `apps/api/internal/platform/galgame/vndb`（VNDB API 客户端 + 链接 curation）
> - `apps/api/internal/platform/galgame/service/vndb_sync.go`（approach-B reconcile）

---

## 1. 一句话总览

两条 VNDB 同步路径，按职责切分；两者共用同一套 tag/official 解析（`vndbresolve`），
不再分叉：

| 命令 / job | 职责 | 触发 | 写法 |
|---|---|---|---|
| `sync-vndb` | 拉 VN 主表 → 建 **status=2 草稿** + 标签/会社关联（标 `source="vndb"`） | 每日 03:00 + 手动 | 直接写（草稿，不建 revision） |
| `sync-vndb-enrich` | 给**已发布游戏**（status=0）富集 **links + tags + officials**，与 VNDB 当前真值幂等对账 | 每日 05:00 + 手动 | approach-B：改关联表 + jsonb-patch 最新快照，不建 revision |

> 旧的 `sync-vndb-relations`（一次性「零关联」补洞、且 resolver 与 `sync-vndb`
> 分叉）已**废弃删除**，能力并入 `sync-vndb-enrich`。

`sync-vndb-enrich` 的调度默认 `--only-missing`（只处理还没有任何 `source="vndb"`
链接的已发布游戏，即新建/claim 的），便宜；全量对账走 CLI（`--apply` 不带
`--only-missing`）或 `--ids` 定向。

---

## 2. provenance（`source` 列）—— 全套的地基

`galgame_link` / `galgame_tag_relation` / `galgame_official_relation` 都有
`source` 列：`""`=用户加，`"vndb"`=同步加。引擎关联**没有** `source`（引擎不来自
VNDB，wiki 自管）。

- **不进快照**：relation 的 `source`（和 tag 的 `spoiler_level`）只在 DB 列，不写
  进 revision snapshot（快照里 `tag_ids`/`official_ids` 仍是 `[]int`）。`reconcileSet`
  是增量式（只增删差异、保留未变行的列），所以用户编辑、revert、PR-merge 都自动
  保留 `source`。link 的 `source`/`source_key` 则**进**快照（`SnapshotLink`）。
- **编辑保留**：`PUT /galgame/:gid` 的 `links`/`tag_ids`/`official_ids` 只替换用户
  子集，`source="vndb"` 的恒被保留（`overlayUpdate` 把当前 vndb 子集并回 + approach-B
  reconcile）。详见 `docs/integration/galgame_wiki/01-galgame.md`。

---

## 3. `sync-vndb` —— 草稿同步

增量（默认，从 DB 最大有效 `vndb_id` 之后、以 VNDB 实际最大 id 封顶）或全量
（`--full`）。每条 VN：`vndb_id` 已在库 → 整条跳过（只建不更新）；`devstatus==2`
→ 跳过；否则一个事务建主表 + 别名 + 标签关联 + 会社关联。标签/会社关联现在标
`source="vndb"`，且 tag 过滤 `lie || rating<1.0`。主表字段映射见代码 `buildGalgame`。

已发布游戏的**持续**对账不归它管 —— 归 `sync-vndb-enrich`。

---

## 4. `sync-vndb-enrich` —— 已发布游戏富集

每批 ≤100 个 VN 一次组合抓取：1 次 `/vn`（tags+developers，`FetchVNMetaBatch`）+
分页 `/release`（store extlinks，`FetchGameLinksBatch`）。每个游戏：

- **链接**：`service.ReconcileVndbLinks` —— curate（schema 白名单：商店/官网，去掉
  百科/统计）后对账 `source="vndb"` 链接，保留用户链接。
- **标签 / 会社**：`vndbresolve` 把 VNDB tag/developer 解析成 wiki id（见 §5），再
  `ReconcileVndbTags` / `ReconcileVndbOfficials` 对账 `source="vndb"` 子集（增缺、删
  VNDB 已不列的、把和 VNDB 重合的用户行转 vndb），保留纯用户关联。

三者都 approach-B、幂等。首次跑某游戏 = 分类（把当前与 VNDB 当前真值匹配的关联标
`source="vndb"`，其余留用户）。dry-run（不带 `--apply`）只查不写、且 resolver 只查
不建。

---

## 5. 共享 resolver（`vndbresolve`）

`sync-vndb` 和 `sync-vndb-enrich` **共用**，命名统一、不再分叉：

- **tag**：tagMap 命中→中文名，未命中→英文原名；**先复用已有**（中文缓存→英文缓存），
  都没有才新建（中文优先）——所以不会给现存的中/英混杂集合**新增**重复。过滤
  `lie || rating<1.0`。`category`：cont/ero/tech→content/sexual/technical。
- **official**：`original`（原文）优先、`name`（罗马音）兜底；按二者任一复用，否则
  新建（写 `original`）。type co/in/ng→company/individual/amateur。

> 历史遗留：早期 `sync-vndb` 用英文建标签、旧 `sync-vndb-relations` 用中文建，DB 里
> 有中/英重复 tag。新 resolver 只保证**不再新增**重复；清理存量重复是单独的事。

---

## 6. 运行方式速查

```bash
cd apps/api

go run ./cmd/sync-vndb                          # 草稿增量（每日 job 同款）
go run ./cmd/sync-vndb --full                   # 草稿全量
go run ./cmd/sync-vndb-enrich --only-missing    # dry，仅未富集的已发布游戏
go run ./cmd/sync-vndb-enrich --apply           # 全量富集对账（已发布）
go run ./cmd/sync-vndb-enrich --apply --ids 1,2 # 定向（任意 status）
```

- 都连 `cfg.GalgameDatabase`（默认库 `kun_galgame_wiki`）、依赖 `docs/tagMap.ts`
  （`--tagmap`/`KUN_VNDB_TAGMAP_PATH` 可改；<100 条报错退出）。
- 调度跑在 oauth 进程内置 job 调度器里（`sync-vndb` 03:00 / `sync-vndb-enrich` 05:00），
  admin `/api/v1/admin/jobs/*` 可手动触发 / 看历史。

---

## 7. 仍**不同步**的数据

| 缺口 | 说明 |
|---|---|
| **草稿主表字段的后续更新** | `sync-vndb` 对已存在条目整条跳过；VNDB 改的标题/简介/发售日不回传（`sync-vndb-enrich` 只管 links/tags/officials，不动主表标量） |
| **系列 `GalgameSeries` / `series_id`** | 不同步 |
| **引擎 `GalgameEngine`** | 不同步（VNDB 仅在 /release 暴露 engine 字符串，且需名称→实体映射；引擎 wiki 自管） |
| **标签别名 / 描述、会社别名 / 描述 / Link** | 不写 |
| **banner 转存** | 直接存 VNDB 图床 URL，未过 image service |
| **devstatus=2（已取消）VN** | 整条不入库 |

---

## 8. 待决策（记录，不在本文范围内修改）

1. **草稿主表字段无增量 upsert**：已入库草稿的 VNDB 侧标量变更不回传。
2. **中/英重复 tag 存量清理**：resolver 只防新增，存量需单独迁移。
3. **系列/引擎从未同步**。
