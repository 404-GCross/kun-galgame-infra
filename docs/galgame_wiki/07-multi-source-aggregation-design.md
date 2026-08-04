# Galgame Wiki 多源聚合设计

> 2026-06-18 — 把 VNDB / DLsite / ErogameScape(批评空间)/(未来)Bangumi 的数据
> 聚合进 galgame wiki 的统一设计:身份解析、来源治理(provenance / survivorship)、
> 按源增量协调,以及把现状的「静默 jsonb 补丁」升级为可审计、可回滚的
> **同步汇总 revision(`action='synced'`)+ `galgame_sync_log` 审计表**。
>
> 关联文档:[01 版本系统设计](./01-revision-system-design.md)、
> [03 VNDB 同步设计](./03-vndb-sync-design.md)、
> [04 VNDB 关系同步设计](./04-vndb-relations-sync-design.md)、
> 契约侧 [integration/galgame_wiki/README.md](../integration/galgame_wiki/README.md)。

## 1. 设计目标

1. **多源汇聚而非取代**:wiki 仍以 VNDB id 为主键身份,但 DLsite / EGS /(未来)Bangumi 的数据要能确定性地落到同一条 galgame 上,各源数据可并存。
2. **来源可追溯(provenance)**:每一份数据都知道「来自哪个源」;发布到 golden record 时**不丢来源信息**。
3. **冲突有规则(survivorship)**:同一字段被多源提供时,有明确的「谁权威」规则;**用户编辑永远凌驾于机器源**。
4. **同步可审计、可回滚**:任何一次自动同步改了什么,都能在版本历史里看到、能定位到具体源与具体 run、能回滚 —— 且**不制造 revision 垃圾**。
5. **幂等、增量、低 churn**:重复跑无副作用;只处理变化项;不退回「全删重建」反模式。
6. **沿用既有地基**:复用 `ApplySnapshot` / `reconcileSet` / approach-B 协调 / `changed_fields` / `head==live` 不变量,不另起炉灶。

## 2. 背景与现状

当前 wiki 只有 VNDB 一个机器源,两个定时任务在跑(见 `internal/jobs/all.go`):

- `sync-vndb`(每天 03:00):VNDB → `status=2` 草稿。
- `sync-vndb-enrich`(每天 05:00):给已发布游戏富集 **links + tags + officials**(approach-B 协调)。

现状的两个关键事实,直接决定本设计:

- **富集是「静默」的**:enrich 改完用 `jsonb_set` 把 head revision 的快照补成实时值(`§1.5 #4` 的 `head==live` 不变量),**不落 revision、不写 `changed_fields`、无逐字段审计**。单源可忍,多源(「这条到底是 dlsite 还是 bangumi 改的、何时」)会成为常见排查盲区。
- **covers / screenshots / bid / release_date / aliases 没有 ongoing 同步**:它们只在建草稿那一刻写一次 + 一次性回填。多源的图片/标签同步等于是**绿地**,从一开始就按对的方式建。

> 历史教训(必读):富集绕过 revision 直接写实时表,正是 2026-06 那次「改一个字段却显示全字段新增」的 stale-baseline bug 的根因。本设计的同步 revision 方案(§7)**从机制上根除**它 —— 把「写库不留痕」变成「写库即留痕」。

## 3. 关键发现:ErogameScape 是「身份桥」

三源的身份能力盘点(来自 `kun-dlsite-api` / `kun-erogamescape-api` 代码勘察):

| 源 | 主键 | 携带的交叉引用 id | 规模 |
|---|---|---|---|
| **VNDB** | `v<NNN>` | —(wiki 已以它为主键) | wiki 现有全量 |
| **DLsite**(`kun-dlsite-api`) | `workno`(`RJ/VJ/BJ/AJ…`) | **无任何交叉引用**,只有 title / maker / regist_date 可供 fuzzy | ~1.2M works(多数非 VN) |
| **EGS**(`kun-erogamescape-api`) | game id(int) | **`vndb` / `dlsite_id`+`dlsite_domain` / `steam` / `dmm` / getchu / gyutto…(生成列 + 反查 API `by-vndb`/`by-dlsite`)** | **34,838 games(23,942 成人)** |

**结论:EGS 同时握有 `vndb` 和 `dlsite_id`,是把三源对齐的「Rosetta Stone」。** 因此主链路是**确定性**的,无需 fuzzy:

```
wiki.galgame(vndb_id)  →  EGS.by_vndb(vndb_id)  →  EGS.dlsite_id  →  DLsite.by_dlsite(workno)
        确定性                    确定性                   确定性
```

这与 entity resolution 的最佳实践一致:**有可信 id 就做确定性匹配,fuzzy 只作兜底**。我们正好有桥,所以 v1 主路径零 fuzzy。覆盖宇宙 = EGS 的 ~3.5 万条(有交叉引用 + 丰富元数据的 galgame),有界、可控;DLsite-only(不在 EGS、无 vndb)多为非 VN,v1 不强求(见 §11)。

## 4. 分层架构:Bronze → Silver → Gold(medallion)

把整条链路按 medallion 分层,职责清晰、可幂等重跑:

| 层 | 组件 | 职责 | 状态 |
|---|---|---|---|
| **Bronze**(raw mirror) | `kun-dlsite-api`、`kun-erogamescape-api` | 原样镜像各源:`raw jsonb` + 生成列 + `content_hash`/`synced_at`(record-level lineage)。append/refresh,不做跨源转换 | ✅ 已建好 |
| **Silver**(解析/对齐) | 本设计新增的 enrich pipeline | 身份解析(EGS 桥)、分类/厂商 resolver、图片去重、字段规范化、survivorship 决策。无状态、幂等、delta-aware | 🔨 待建 |
| **Gold**(golden record) | wiki `galgame` 表 + `galgame_revision` | 人工可编辑的权威记录,带 attribute-level provenance(`source` 列)+ survivorship + 同步审计 | 有地基 |

铁律:**Bronze 永不直接写 Gold;Silver→Gold 的每一次写入,都走「按源 reconcile(§6)+ 落一条同步 revision(§7)」。** Bronze 通过各服务的 HTTP API(`by-vndb` / `by-dlsite` / batch)被 Silver 读取,Silver 与 Gold 同库(`kun_galgame_wiki`),写入在一个事务内完成。

## 5. 不变量(必读)

> 这几条是本设计的脊柱。任何新写入路径都必须遵守,否则会重蹈 stale-baseline / 反模式覆辙。

1. **来源是行/字段的一等属性。** 每个集合型关系行带 `source`/`source_key`;标量字段的来源由 survivorship 规则 + (v2)provenance 侧表决定。`source=''` 恒表示「用户」。
2. **用户数据不可被机器覆盖。** 任何源的同步都**只动自己 `source` 的行**、**只填空的标量**;`source=''` 的行与用户改过的标量永不被机器触碰。
3. **同步即留痕。** 自动同步对某 galgame 产生**净变化**时,必须落一条 `action='synced'` 的 revision(§7),其 `snapshot` = 同步后实时全量、`changed_fields` = 本次净改字段集。**禁止**再用 `jsonb_set` 静默改写 head 快照来「保持 head==live」—— 落 revision 已天然保证 head==live。
4. **无净变化则零写入。** 协调器幂等:`ChangedKeys` 为空就不写库、不落 revision、不写 sync_log。
5. **身份用确定性 id,绝不自动 fuzzy 合并。** 主链路靠 EGS 桥的 exact id;任何 fuzzy 匹配只能产出**待人工确认**的候选,绝不自动 merge(§11)。
6. **集合语义沿用 §1.5#6。** ID 数组规范排序、顺序无关;协调只算增删差量(§6)。

## 6. 来源治理:Provenance & Survivorship

参考 MDM 的共识 —— **survivorship 做在字段级而非记录级**,并在字段值层面保留 provenance(来源 + 时间戳)。本项目字段分两类,provenance 粒度不同:

### 6.1 集合型关系(tags / officials / links / covers / screenshots)

每行已有 `source`/`source_key` = 天然的字段级 provenance。

- **survivorship = 按行归属**:每个源只 reconcile `source=<自己>` 的行;`source=''`(用户)永不被机器碰。
- **跨源同物去重**:
  - 图片:按 `image_hash`(内容寻址)折叠 —— 同一张图来自 VNDB 与 DLsite 会落到同一 `(galgame_id, image_hash)`;冲突时保留**优先级更高源**的行级负载(sort_order/caption/sexual/violence)。
  - tag / official:经 resolver(§9)把各源词汇映射到**同一个 wiki id** 折叠。

### 6.2 标量字段(name_* / intro_* / release_date / age_limit / content_limit / bid …)

住在 `galgame` 行上,**没有 per-field source** —— 这是真正的难点,如实记录:

- **v1 规则(简单安全):机器源只「填空」(fill-if-empty)。** 一个机器源只在某标量为空/未设时填入,**绝不覆盖任何非空标量**(无论用户还是别的源先填)。「谁先填」由下面的优先级决定。
- **v2(需要时再上):** 加 `galgame_field_provenance` 侧表(§8.4),记录「每个标量上次由哪个源写入」,支持「源 X 可覆盖源 Y 写的旧值,但永不可覆盖用户值」。这才是完整的 MDM attribute-level survivorship,但复杂度更高,非首期必须。

### 6.3 字段级源优先级表(survivorship policy)

集中在一处声明「哪个源对哪个字段权威」(实现为代码常量表,可单测):

| 字段 / 关系 | 优先级(高 → 低) | 说明 |
|---|---|---|
| `name_ja_jp` / `name_zh_*` / `name_en_us` | 用户 > VNDB > EGS(gamename/furigana)> DLsite | VNDB 标题最规整 |
| `intro_*` | 用户 > VNDB > DLsite(intro)> EGS | |
| `release_date` | 用户 > VNDB > EGS(sellday)> DLsite(regist_date) | |
| `age_limit` / `content_limit` | 用户 > VNDB > EGS(erogame 标志) | |
| `bid`(BangumiID) | 用户 > Bangumi(未来) | 其它源无 bid |
| **screenshots** | 用户行保留;机器行各管各:VNDB / EGS / DLsite(CG)按 `source` 共存,按 hash 去重 | |
| **covers** | 用户 pinned 优先;VNDB cover 与 DLsite 商店封面共存,pinned(sort_order=0)归属优先级最高源 |
| **tags** | 用户行保留;VNDB / EGS(povlist)/ DLsite(genres)各管各 `source` 行,resolver 映射去重 |
| **officials** | 用户行保留;VNDB developer / EGS brand / DLsite circle 各管各,resolver 去重 |
| **links** | 用户行保留;各源 store/info 链接按 host 去重(沿用 `mergeUserAndVndbLinks`) |

> Wikidata 的 [statement rank](https://www.wikidata.org/wiki/Help:Ranking) 类比:用户 = `preferred`(锁定)、机器源 = `normal`、被取代的旧值 = `deprecated`(删除/降级)。

## 7. 🎯 同步汇总 revision 方案

把现状的「静默 jsonb 补丁」升级为**可审计、可回滚、可在历史里过滤**的同步记录,同时不制造 revision 垃圾。设计参考 Wikidata:**每条 statement 带 reference(来源/方法/时间),bot 编辑明确标记、可在历史里隐藏**。

### 7.1 核心规则

**每 galgame × 每次同步 run,当且仅当这次真的产生净变化,落一条「合并的」system revision。**

- **不是** per-field(那是 spam);**不是** per-source 各落一条(那也碎);而是**一个游戏一次 run 一条**,把本次 VNDB+EGS+DLsite 的净改动合进去。
- 与被否决的「每次 enrich 无脑落 revision」的区别:**仅在真有净变化时**(§5#4),且**每游戏每 run 合并一条**。

### 7.2 revision 形态(复用现有 `galgame_revision`)

```go
GalgameRevision{
    Action:        "synced",          // 新增枚举 → 改 CHECK 约束(§10 迁移)
    UserID:        SystemSyncUserID,  // 预留的系统 bot 用户(§8.5),历史里可"隐藏机器人编辑"
    ChangedFields: changed,           // ★ 复用 2026-06-18 新增的机制,/diff 直接消费
    Snapshot:      reTakenLiveSnap,   // ★ 同步后实时全量 → 天然满足 head==live,不再需要 jsonb_set 补丁
    Note:          "VNDB+EGS sync: +3 screenshots, +1 official, release_date set",
    // 复用现有列;不需要给 revision 加新字段
}
```

- `changed_fields` 已经存在(2026-06-18 为修 stale-baseline 而加),`/diff` 已优先消费它 —— 同步 revision 天生兼容版本历史与 diff。
- `snapshot` = 写库后 `TakeSnapshot(live)`(`§1.5 #4`),所以 head 自动 == live;**这条 revision 一落,就同时满足了过去靠 `jsonb_set` 维持的不变量** —— 于是 sync 路径与用户编辑路径**统一**为「写库 → 落 revision」,`patchSnapshotIDs` / `jsonb_set` 那套静默补丁可以从 enrich 路径退役。

### 7.3 逐源逐字段明细:`galgame_sync_log`

revision 的 `changed_fields` 只到「字段名集合」。要回答「**哪个源**把**哪个字段**从 X 改成 Y、在**哪次 run**」需要更细的审计,单开一张 lineage 表(= MDM attribute-level lineage / medallion 的 lineage 层):

```sql
CREATE TABLE galgame_sync_log (
    id          bigserial PRIMARY KEY,
    galgame_id  int         NOT NULL,
    revision_id int,                       -- 本批产生的 'synced' revision(无净变化则该 run 不产 revision)
    run_id      bigint      NOT NULL,      -- = job_run.id,聚合"同一次同步运行"
    source      varchar(16) NOT NULL,      -- 'vndb' | 'egs' | 'dlsite' | ...
    field       varchar(32) NOT NULL,      -- 'screenshots' | 'tag_ids' | 'release_date' | ...
    op          varchar(8)  NOT NULL,      -- 'add' | 'remove' | 'set' | 'fill'
    detail      jsonb,                     -- {added:[...], removed:[...]} 或 {old:..., new:...}
    created     timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX idx_sync_log_galgame ON galgame_sync_log (galgame_id, created DESC);
CREATE INDEX idx_sync_log_run     ON galgame_sync_log (run_id);
CREATE INDEX idx_sync_log_src_fld ON galgame_sync_log (source, field, created DESC);
```

分工:
- **`galgame_revision`(给人看)**:版本历史里一条「系统同步」,可折叠/过滤、可回滚。
- **`galgame_sync_log`(给排查/统计/回滚用)**:可回答「EGS 上周改了哪些游戏的评分」「这个值到底哪个源、哪次 run 写的」。
- `run_id` 复用 `job_run.id`(jobs 框架已有),把审计与「哪次任务运行」绑定,不必另造 uuid。

### 7.4 volume 核对

- **稳态**:每天同步只有少数游戏真变 → 每天少量 `synced` revision + 少量 sync_log 行,可接受。
- **首次全量回填**:会产生大量 `synced` revision(一次性)。用同一个 `run_id` 给这批打标,前端版本历史可聚合成「2026-06-20 批量同步影响 N 条」,像 Wikidata 的 batch/bot 分组。`changed_fields` + `synced` action 让前端能默认折叠机器编辑。

### 7.5 与既有工作的衔接(化隐患为资产)

| 既有 | 同步 revision 方案如何衔接 |
|---|---|
| `changed_fields`(2026-06-18) | 同步 revision 直接写它,/diff 与历史天然兼容 |
| `head==live` 不变量(§1.5#4) | 落 revision 后 head 自动 == live,**不再需要 jsonb_set 静默补丁** |
| stale-baseline bug 根因(富集不落 revision) | **被根除**:同步从「写库不留痕」变成「写库即留痕 + 即对齐」 |
| `action` CHECK 约束 | 加 `'synced'`(§10) |

## 8. 数据结构

### 8.1 身份映射:`galgame_external_id`

`vndb_id` 仍是 wiki 主键身份,留在 `galgame.vndb_id`。次级 id(egs/dlsite/steam/dmm/getchu…)入新表:

```sql
CREATE TABLE galgame_external_id (
    id          bigserial   PRIMARY KEY,
    galgame_id  int         NOT NULL REFERENCES galgame(id) ON DELETE CASCADE,
    source      varchar(16) NOT NULL,      -- 'egs' | 'dlsite' | 'steam' | 'dmm' | 'getchu' | ...
    external_id varchar(64) NOT NULL,      -- '12345' | 'RJ01636464' | ...
    extra       jsonb,                     -- 如 dlsite domain(maniax/pro/bl)
    created     timestamptz NOT NULL DEFAULT now(),
    updated     timestamptz NOT NULL DEFAULT now(),
    UNIQUE (source, external_id)           -- 一个外部 id 只对应一条 galgame
);
CREATE INDEX idx_external_id_galgame ON galgame_external_id (galgame_id);
```

- **不设** `UNIQUE(galgame_id, source)`:一条 galgame 可能有多个 DLsite 版本(RJ 完整版 + 体验版,或 RJ + VJ),允许一对多。
- 从 EGS 交叉引用回填(§9.2)。反查同时支撑「DLsite 翻页时反推 vndb」等场景。

### 8.2 集合型关系(无 schema 变更)

`galgame_cover` / `galgame_screenshot` / `galgame_tag_relation` / `galgame_official_relation` / `galgame_link` 均已有 `source`/`source_key`。`cover`/`screenshot` 用 `(galgame_id, image_hash)` 复合主键 —— 天然适合「按源 upsert + 删该源差集」(§6.1)。**只需扩协调代码,不动表结构。**

### 8.3 审计:`galgame_sync_log`

见 §7.3。

### 8.4 (v2,可选)标量 provenance:`galgame_field_provenance`

```sql
CREATE TABLE galgame_field_provenance (
    galgame_id int         NOT NULL,
    field      varchar(32) NOT NULL,
    source     varchar(16) NOT NULL,   -- 上次写该标量的源;'' = 用户
    value_hash varchar(64),            -- 该源写入值的 hash,检测外部漂移
    updated    timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (galgame_id, field)
);
```

仅当 §6.2 的 fill-if-empty 不够用(需要「源纠正源」)时再上。

### 8.5 系统 bot 用户

`galgame_revision.user_id` 概念上指向身份库 `kun_galgame_infra.users`(跨库,无硬 FK)。为让前端把 `action='synced'` 显示成有名字/头像的「系统同步」账号,在身份库**预建一个专用 bot 用户**,其 id 作为 `SystemSyncUserID` 常量。下游(kungal/moyu)解析 user 时也能正常显示。

## 9. Silver 层细节

### 9.1 按源协调(泛化 approach-B)

把现有 `ReconcileVndb{Links,Tags,Officials}`(硬编码 `source='vndb'`)泛化为:

```go
// 语义:让 galgame 中 source=<src> 的 <relation> 行 == desired,
// 只写差量,绝不碰别的 source / 用户行;返回本次差量供 sync_log/changed_fields 使用。
ReconcileSource(tx, galgameID, src string, relation RelationKind, desired []Row) (delta, error)
```

- **tags / officials / links**:已具备(改成接收 `src` 参数)。
- **covers / screenshots(新增)**:按 `(galgame_id, image_hash)` upsert 该源的图,删「该源不再 desired」的图;保留用户行与别的源行;冲突(同 hash 跨源)按 §6.3 优先级决定行级负载归属。
- **编辑路径并集保护**:用户编辑若显式替换 covers/screenshots,必须像 tag 的 `unionIDs` 那样**与 source 托管行求并集**,否则用户动一次封面会删掉同步行(`§5 #2`)。

### 9.2 身份解析流程(每 galgame)

```
1. 取 galgame(vndb_id)
2. egs := EGS.by_vndb(vndb_id)                       // Bronze 读
   若有:upsert galgame_external_id(egs / egs.dlsite_id / steam / dmm …)
3. dlsite := egs?.dlsite_id ? DLsite.by_dlsite(...) : nil
4. vndbData := (既有 VNDB 同步数据 / 或此处取)
```

### 9.3 分类 / 厂商 resolver

每源一个 resolver,把各源词汇映射到**统一的 wiki tag/official id**,**先按名复用已有、查不到才建**(重蹈英/中重复标签的教训,见 [04](./04-vndb-relations-sync-design.md)):

- tag:VNDB(已有 `vndbresolve` + tagMap)、EGS povlist(215 个,带 A/B/C rank → 可映射 spoiler/权重)、DLsite genres。
- official:VNDB developer、EGS brand、DLsite circle/maker → 同一 wiki official,按名(+原名)复用。
- (后续阶段)角色 / CV / staff 班底:EGS `staff`(28 万)+ DLsite 提取的 characters → wiki 还没有的实体类型,需新表,单独排期。

### 9.4 端到端 enrich(每 galgame,一个事务)

```
begin tx:
  deltas := []
  // 关系:各源各自 reconcile
  deltas += ReconcileSource(tx, gid, "vndb",   tags,        resolve(vndbData.tags))
  deltas += ReconcileSource(tx, gid, "egs",    tags,        resolve(egs.povTags))
  deltas += ReconcileSource(tx, gid, "vndb",   screenshots, vndbData.screenshots)
  deltas += ReconcileSource(tx, gid, "dlsite", screenshots, dlsite.cgImages)     // 按 hash 去重
  deltas += ReconcileSource(tx, gid, "egs",    officials,   resolve(egs.brand))
  ... (covers / links / 各源)
  // 标量:fill-if-empty,按优先级(§6.3)
  deltas += FillScalarsIfEmpty(tx, gid, precedence, {vndb, egs, dlsite})
  // 图片去重 + tag/official id 折叠已在 reconcile/resolver 内完成
  if deltas 为空: return (no-op,不产 revision/sync_log)     // §5#4 幂等
  // 写痕
  snap := TakeSnapshot(reload(tx, gid))
  rev  := create GalgameRevision{action:'synced', user_id:SystemSyncUserID,
                                 changed_fields: fields(deltas), snapshot: snap, note: summarize(deltas)}
  insert galgame_sync_log(deltas, revision_id=rev.id, run_id=jobRun.id)
commit
```

## 10. 迁移清单(schema-affecting → 必须提醒)

跑在 `kun_galgame_wiki`,**不随部署自动执行**(见 [deploy-migration-gap]):

1. **`action='synced'`** 加进 `galgame_revision` CHECK。注意该 CHECK 是迁移入口里**显式 DROP/ADD**(见 `model/pr.go` 注释;W5 起在 `cmd/migrate-catalog/galgame.go`),要同时改**模型 struct tag** 与**该 raw SQL**,否则旧库保留旧约束、INSERT 报 23514。
2. **新表** `galgame_external_id`、`galgame_sync_log`(AutoMigrate 可建)。
3. **(v2 可选)** `galgame_field_provenance`。
4. **身份库** 预建系统 bot 用户,记录其 id 为 `SystemSyncUserID`。
5. **纯代码(无 schema)**:`ReconcileSource` 泛化 + covers/screenshots 协调 + 编辑路径并集保护 + resolver + enrich job。

执行顺序(吸取上次教训):**先跑迁移(W5 起 `cmd/migrate-catalog`)加约束/表,再部署写 `action='synced'` 的新代码**,否则新代码 INSERT 撞缺失约束/表。

## 11. 分阶段实施

- **Phase 0(地基)**:`galgame_external_id` + `action='synced'` + `galgame_sync_log` + 系统 bot 用户;把现有 VNDB enrich 从「jsonb_set 静默补丁」切到「落 `synced` revision」(单源先跑通同步审计闭环,顺带彻底关掉 stale-baseline 根因)。
- **Phase 1(EGS 接入)**:EGS by-vndb 回填 `galgame_external_id`;EGS povlist tag resolver + brand official resolver;EGS 评分等**新字段**(需加列,单独评审)。
- **Phase 2(DLsite 接入)**:经 EGS.dlsite_id → DLsite;商店封面 / CG screenshots(按 hash 去重)/ circle official;价格/销量等新字段(需加列)。
- **Phase 3(实体扩展)**:角色 / CV / staff 班底(EGS staff + DLsite characters)→ 新实体表。
- **Phase 4(可选)**:DLsite-only / fuzzy 兜底(§见下)、标量 provenance v2、Bangumi 源。

每个 Phase 配一次性 **offline、幂等、source-scoped 回填**(模仿 `cmd/migrate-galgame-screenshots`):用 Bronze 的 `content_hash`/`synced_at` 做 delta-aware;**跑完重置当日配额计数 + refping**(踩过的坑);首批用统一 `run_id`。

## 12. 风险与取舍(实事求是)

- **标量字段无 per-field provenance** 是真痛点:v1 fill-if-empty 安全,但「源无法纠正另一个源写错的标量」,要 v2 侧表才行。
- **v1 只覆盖 EGS 能桥接的宇宙**;DLsite-only(无 vndb)的 galgame 需 **fuzzy 兜底**(blocking 按发售年+brand → 相似度阈值 → **进 pending 人工确认,绝不自动合并**,§5#5),留 Phase 4。
- **首次回填产生大量 `synced` revision**(一次性,用 `run_id` 聚合 + 前端折叠机器编辑缓解)。
- **EGS 大表不全搬**:POV 票(270 万)/ reviews(220 万)是 EGS 内部聚合,wiki 只取 per-game 派生值(rank A/B/C 标签、median 评分),不搬明细。
- **跨源同图不同负载**:同一 hash 图的 sort_order/caption 跨源可能冲突 → 按 §6.3 优先级取一份,其余源仅作 `source` 标记共存。

## 13. 参考(外部最佳实践)

- MDM survivorship(字段级规则)与 golden record + attribute-level provenance:
  [Profisee — MDM survivorship](https://profisee.com/blog/mdm-survivorship/)、
  [Golden record + attribute-level provenance](https://greenwolftechlabs.com/survivorship-in-mdm-creating-the-golden-record/)
- Wikidata:statement references + rank + bot import 模型:
  [CACM — Wikidata](https://cacm.acm.org/research/wikidata/)、[Help:Ranking](https://www.wikidata.org/wiki/Help:Ranking)
- Entity resolution(确定性优先、fuzzy 兜底的 hybrid):
  [Zingg — deterministic vs probabilistic](https://www.zingg.ai/post/deterministic-vs-probabilistic-matching-why-enterprise-entity-resolution-needs-both)、
  [Record linkage(Wikipedia)](https://en.wikipedia.org/wiki/Record_linkage)
- Medallion 架构(bronze/silver/gold、幂等 + lineage):
  [Medallion architecture explained](https://bix-tech.com/medallion-architecture-explained-how-bronzesilvergold-layers-supercharge-your-data-lakehouse-mesh-and-data-quality/)
