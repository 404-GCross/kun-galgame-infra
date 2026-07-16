# Galgame 发售月表（发售日历）设计

> 一句话定位:给 wiki 增加一个**按自然月翻页的发售日历**——用户一个月一个月地翻,同一条时间线里**已发售与未发售混排**,并正确容纳「只知道月」「只知道年」「待定(TBA)」这类**模糊发售日**。月的形态采用 ISO 8601 自然月(`YYYY-MM`)。

> 状态:设计稿(approved,待实施)。本设计建立在
> [03-vndb-sync-design](./03-vndb-sync-design.md)(发售日来自 VNDB)、
> [05-search-design](./05-search-design.md)(已有 `released_*` 月精度过滤)、
> [07-multi-source-aggregation-design](./07-multi-source-aggregation-design.md)(`release_date` 多源 overlay)、
> [99-final-upgrade-plan](./99-final-upgrade-plan.md)(`released string → release_date date + release_date_tba`)之上。

---

## 1. 背景 & 目标

### 1.1 现状

`galgame` 当前的发售日模型(见 [99 §U1](./99-final-upgrade-plan.md)):

- `release_date` —— PG `date` 列(GORM `ReleaseDate *model.Date`),`null = 未知`。
- `release_date_tba bool` —— `true = 已公布但日期待定`。
- 上游解析 `ParseLegacyReleased`(`internal/platform/galgame/model/snapshot.go`):把 VNDB 的 `released` 串解析成上面两个字段。
- 搜索侧([05](./05-search-design.md))已经能按 `released_from / released_to`(月精度)过滤,Manticore/索引里有 `released_ts / released_year / released_month`。

### 1.2 核心缺陷:发售日**丢失了精度**

`ParseLegacyReleased` 解析时以 `月=1, 日=1` 兜底,于是:

| VNDB `released` | 现在落库的 `release_date` | 问题 |
|---|---|---|
| `2026-06-15` | `2026-06-15` | ✅ 正确 |
| `2026-06`(只知道月) | `2026-06-01` | ⚠️ 看起来像"6月1日",分不清"确切1号"还是"6月内某天" |
| `2026`(只知道年) | `2026-01-01` | ❌ **会被当成"1月1日"**,在月表里错误地出现在 **1 月** |
| `tba` | `null` + `tba=true` | 与"完全未知"都落到 `null`,靠 `tba` 区分 |
| 空 / `unknown` | `null` + `tba=false` | —— |

做月表必须先解决这一点:**没有精度,既无法把"只知道年"的游戏挡在具体月份之外,也无法在 UI 上区分"6月15日"和"6月(日未定)"**。这是整个设计的地基。

### 1.3 目标

1. 一个**精度感知**的发售日模型,正确表达 日 / 月 / 年 / TBA / 未知 五种状态。
2. 一个**按 ISO 自然月翻页**的日历接口,已发售 + 未发售混排,模糊日期有去处。
3. 查询走索引、可缓存、对编辑改动可定向失效。
4. 复用现有 `release_date` 列与多源 overlay,迁移成本最小。

---

## 2. 核心决策:精度感知的发售日

### 2.1 模型:归一化日期 + 精度枚举

保留 `release_date` 作为**可排序的归一化日期**(日未知归一到当月 1 号),新增一个**精度枚举**承载真相:

```sql
-- migration on kun_galgame_wiki(cmd/migrate-galgame)
-- 本项目全仓不使用 PG ENUM 类型(一律 int 码 + CHECK 约束),故 release_precision
-- 用 varchar + CHECK,与既有 chk_galgame_revision_action / chk_galgame_vndb_id_format
-- 同风格。列本身由 AutoMigrate 依模型字段创建(默认 'unknown');CHECK 用幂等的
-- drop-then-add raw SQL 追加。
ALTER TABLE galgame
  ADD CONSTRAINT chk_galgame_release_precision
  CHECK (release_precision IN ('day','month','year','tba','unknown'));
```

GORM 字段:

```go
// model.Galgame
ReleasePrecision string `gorm:"column:release_precision;size:10;not null;default:'unknown'" json:"release_precision"`
```

字段语义(`release_precision` 是 `release_date` 精度的唯一真相来源;**最终取代** `release_date_tba`——P1 阶段二者并存,`release_date_tba` 由 `release_precision == 'tba'` 派生保持同步,待所有读者迁移到 `release_precision` 后再删 bool):

| 状态 | `release_date` | `release_precision` | 月表归属 |
|---|---|---|---|
| 确切日 | `2026-06-15` | `day` | 6 月 |
| 只知月 | `2026-06-01`(归一到 1 号) | `month` | 6 月,展示"6月 · 日未定" |
| 只知年 | `2026-01-01`(归一到 1/1) | `year` | **不进任何具体月**;进"年内待定"桶 |
| 待定 | `NULL` | `tba` | 全局"发售日未定"桶 |
| 未知 | `NULL` | `unknown` | 不展示 |

> **决策:为什么是 `date + 精度枚举`,而不是把日期打包成整数。**
> 一种业界常见做法(本项目上游 **VNDB** 即如此)是把发售日编码成单个整数 `YYYYMMDD` 并用哨兵位表达精度(`20260699`=6月日未定、`20269999`=只知年、`99999999`=TBA、`0`=未知),好处是排序天然正确、单列搞定。但本项目已有 `release_date` 的 `date` 列、`model.Date` 类型、以及索引了 `released_ts/year/month` 的搜索层——改成整数会丢掉原生日期运算(`date_trunc` / `interval` / `age()`)和这一整套接线。**`date + 枚举`是对现有结构的最小增量(只加一列),且保留原生日期语义。** 整数方案仅作为备选记录在此。

> **决策:精度模型对齐国际标准 EDTF / ISO 8601-2:2019**(Library of Congress 维护)。EDTF 用 `2026-06-XX`(月已知日未知)、`2026-XX`(只知年)、`XXXX-XX-XX`/`2026/..`(待定)表达不完整日期。我们**内部用枚举存储与查询**,在**对外展示 / 互操作**时可渲染为 EDTF 串(可选,见 §2.4)。

### 2.2 修复 `ParseLegacyReleased`:返回精度

```go
// nil/""    → (nil, "unknown")
// "tba"     → (nil, "tba")
// "2026"    → (2026-01-01, "year")
// "2026-06" → (2026-06-01, "month")
// "2026-06-15" → (2026-06-15, "day")
func ParseLegacyReleased(s string) (*time.Time, ReleasePrecision)
```

调用点(`internal/jobs/vndbsync/vndbsync.go` 等)同步改为写入 `release_precision`。

### 2.3 与多源 overlay 的关系

[07](./07-multi-source-aggregation-design.md) 里 `release_date` 是按源优先级 overlay 的标量字段(User > VNDB > EGS `sellday` > DLsite `regist_date`)。**`release_precision` 必须与 `release_date` 作为一个整体一起 overlay**——同一个胜出源,日期与其精度成对取用,不能日期来自 VNDB、精度来自别处。在 overlay 的 source-value 记录里把二者绑成一组。

### 2.4 输入契约扩展(✅ P4b 后端已实现)

写接口 DTO 支持月/年精度录入:

- DTO 接受 `YYYY-MM-DD` | `YYYY-MM` | `YYYY` | 空(+ 独立的 `release_date_tba`)——`date_or_empty` validator 已放宽,非法串 → 400。
- 后端把输入**归一化**(`model.NormalizeReleaseDateInput`:`"2026-06"`→`2026-06-01`、`"2026"`→`2026-01-01`)写入 `snapshot.ReleaseDate`,并由 `model.DeriveInputPrecision(rawInput, tba)`(tba 优先,否则按粒度给 day/month/year/unknown)记录 `release_precision`。**精度从 RAW 输入推,日期存归一化值**——二者配套,create/edit/PR 三条写路径一致。
- 仅切 `release_date_tba`(日期不在本次 patch)时不重算粒度,沿用基线精度(tba→tba;关 tba 时退化为 day/unknown),避免丢失既有 month/year。

> 仍待做(产品决策):wiki 的 Create/Edit 表单**目前没有发售日字段**(发售日来自 VNDB)。后端已能接收部分日期;要让编辑者**手动录入**还需在表单加一个精度感知的日期控件(日/月/年/待定)。见 [§11 P4](#11-落地阶段)。

### 2.5 快照 / ChangedKeys(✅ P3 已实现)

[99 §U1](./99-final-upgrade-plan.md) 把 `released` 拆成了 `release_date` + `release_date_tba` 两个 ChangedKeys。本次:

- revision `Snapshot` 增加 `release_precision` 字段;`TakeSnapshot` 从模型带出、`ChangedKeys` 独立 diff、`ApplyChanges` 复制——`release_date_tba` **保留并存**(由精度派生),不直接替换。
- **关键不变量**:`release_precision` 随快照走,所以"改简介"等**无关编辑不会把 VNDB 给的 month/year 精度冲成 day**(`TakeSnapshot` 带出旧精度,只有动到日期/tba 的编辑才用 `DeriveInputPrecision` 重算)。
- `ApplySnapshot` 写库时用 `Snapshot.ResolveReleasePrecision()`:旧快照(无该字段)回退推导出合法值(tba→tba / 有日期→day / 否则 unknown),保证 **revert 到 P3 之前的版本不会写空值触发 CHECK**。
- 保证 `/diff` 把"6月日未定 → 6月15日"显示成一次真实变更。

---

## 3. 「月」的定义:ISO 自然月,边界用 JST

- **月 = ISO 8601 `YYYY-MM` 自然月**,查询用**半开区间** `[2026-06-01, 2026-07-01)`。
- `release_date` 是**无时区的 calendar date**(日本公布的民用日期),用 PG `date`,**不要 `timestamptz`**——否则一个 JST 发售会因观看者时区偏移而落到错误的月份。月份边界就是 `date` 字面量,**不做任何时区换算**。
- **JST(`Asia/Tokyo`)只用于两处**:① 默认"当前月" = `now()` 在东京的月份;② "今天"标记 = 东京的今天。避免 UTC 服务器把当前月算早一天。
- 若将来要做"精确到秒的倒计时",再单加 `release_at timestamptz`(仅 `day` 精度的游戏有),与用于分组排序的 `date` 分开。

---

## 4. TBD 三桶模型(模糊日期要有去处,不能藏)

月表不能只显示有确切日期的游戏,也不能把"只知道年/待定"的游戏硬塞进 1 月。三个互斥的桶:

1. **月内**:`day` + `month` 精度且落在该月。`month` 精度展示"日未定"。
2. **「年内月份待定」桶**:`year` 精度。按年聚合(月表底部可折叠区,或年视图)。
3. **「发售日未定 TBA」桶**:`tba`。全局一个入口。

`unknown` 不展示。

---

## 5. 排序规则

同一区间内:**精度越低排越后,TBA 最后**(符合"有确切日的在前、模糊的压后"的直觉)。因为 `month` 精度被归一到 1 号,纯 `release_date` 升序会把它排到月初,需要加精度 tiebreak:

```sql
ORDER BY release_date ASC, (release_precision = 'month') ASC, id ASC
-- 同月:day(false 在前) 先于 month(true 在后);id 兜底
```

---

## 6. API 契约

```
GET /galgame/calendar?month=2026-06          # 严格 ISO YYYY-MM
GET /galgame/calendar/pending?year=2026      # 「年内月份待定」桶(year 精度)
GET /galgame/calendar/tba                    # 全局 TBA 桶
```

校验:`month` 必须是零填充的 `YYYY-MM`;无法解析 → `400`;月份 > 12 等语义非法 → `422`。

月接口响应:

```jsonc
{
  "month": "2026-06",
  "today": "2026-06-29",            // 东京今天,前端据此画"今日"分隔线
  "items": [ /* 该月,按 §5 排序 */ ],
  "links": { "self": "...month=2026-06", "prev": "...month=2026-05", "next": "...month=2026-07" },
  "meta": {
    "prev_month": "2026-05", "next_month": "2026-07",
    "has_prev": true, "has_next": true, "count": 37
  }
}
```

`items[]` 每条(沿用 galgame summary 形态:`id / name_* / release_date / release_precision / covers / officials`)。

约定:

- **空月 = `200` + `items: []`**(不是 404 / 204),保留 prev/next 让用户翻出去。
- **未来翻页不抛错**:在数据边界(最晚有公布的月 + 小缓冲)处用 `has_next=false` 夹住;不静默跳到最近的非空月(会破坏 prev/next 的确定性)。
- **筛选**(query 参数,精简自通用发售筛选):平台、语言/区域、全年龄 vs 18+(`age_limit`)、发售类型(完整/部分/试玩)。

> **决策:翻页单元 = 月 key,而非 offset / cursor。**
> 日历有一个天然的、全序的、离散有限且对人类有意义的分区——「月」。于是 `next = month ± 1` 是纯客户端算术(无需先取上一页才知道下一页 key);对插入稳定(编辑新增一个游戏不影响其它月的边界);`?month=2026-06` 可收藏可分享,且 **URL 本身就是 CDN 缓存键**。offset 会 O(offset) 且并发插入时漂移;不透明 cursor 在这里是缓存与导航的反优化。

---

## 7. 数据库查询与索引

### 7.1 月查询(SARGable 半开区间)

边界在 Go 里算好传参,走索引:

```go
// 月边界在 JST 算(决定"当前月"/"今天"),但传给 SQL 的是 calendar date 字符串。
start    := time.Date(y, m, 1, 0, 0, 0, 0, jstZone)
startStr := start.Format("2006-01-02")
nextStr  := start.AddDate(0, 1, 0).Format("2006-01-02")
db.Where("release_date >= ? AND release_date < ?", startStr, nextStr). // ← 字符串,不是 time.Time
   Where("release_precision IN ?", []string{"day", "month"}).
   Where("status IN ?", []int{0, 2}). // 0 已发布 + 2 未认领 VNDB 草稿(月历要完整)
   Order("release_date ASC, release_precision ASC, id ASC") // 同日内 day 在前;裸列 = 走索引序、无 Sort
// status IN (0,2) 对上 partial index idx_galgame_calendar_pubdraft(见 §7.2)
```

> ⚠️ **必须传 `YYYY-MM-DD` 字符串,不能传 `time.Time`**:`time.Time` 实参会被隐式强制成 `timestamptz`,导致与 `date` 列比较时**放弃 btree 索引**(与 `repo.List` 同一教训)。字符串字面量直接对 `date` 列比较、走索引。

- **禁止**用 `EXTRACT(MONTH FROM release_date) = 6` 或 `date_trunc('month', release_date) = …` 作 `WHERE`——函数包住列会废掉 B-tree 索引、退化为 Seq Scan。只有半开 range 才 SARGable。
- 年内待定桶:`release_precision='year' AND release_date >= 'YYYY-01-01' AND < 'YYYY+1-01-01'`。
- TBA 桶:`release_precision='tba'`(`release_date IS NULL`)。

### 7.2 索引

```sql
-- 既服务 range 过滤,又覆盖 ORDER BY。月历收录 已发布(0) + 未认领 VNDB 草稿(2),
-- 故 partial 谓词为 status IN (0,2);名称与 cmd/migrate-galgame 一致(旧的 status=0
-- 版 idx_galgame_calendar 已 drop,改名避免每次部署重建同名索引)。
CREATE INDEX IF NOT EXISTS idx_galgame_calendar_pubdraft
  ON galgame (release_date, release_precision, id)
  WHERE status IN (0, 2);
```

### 7.3 每月计数 / 年度概览

- 单月计数走同一索引的 range `count(*)`。
- 年度热力图 / "每月 N 部":`GROUP BY date_trunc('month', release_date)`(`date_trunc` 在 SELECT/GROUP BY,不在 WHERE),或加一个 generated 列:

```sql
ALTER TABLE galgame ADD COLUMN release_ym integer
  GENERATED ALWAYS AS (EXTRACT(YEAR FROM release_date)::int*100 + EXTRACT(MONTH FROM release_date)::int) STORED;
```

- 重度读取的年度概览可用 materialized view + `REFRESH MATERIALIZED VIEW CONCURRENTLY`(需唯一索引)。

### 7.4 走 Postgres 还是 Manticore?

搜索层([05](./05-search-design.md))已有 `released_ts/year/month` + 月精度过滤,月表**也可**走 Manticore。但月表是纯日期 range 扫描、结果可重缓存,**直连 Postgres 更简单可控**;Manticore 留给全文搜索 + facet。两者皆可,本设计默认 Postgres 直连。

---

## 8. 缓存策略

按"该月相对 `now`"分治(在 handler 内判定 过去/当前/未来):

| 该月 | 缓存头 |
|---|---|
| 当前 / 未来 | `Cache-Control: public, max-age=0, s-maxage=300, stale-while-revalidate=60` + 弱 ETag(`s-maxage` 上限 ≤ 距月末秒数) |
| 过去 | 长 `s-maxage` + **强 ETag**(见下警告) |

> ⚠️ **过去的月份并非"永久不可变"**:wiki 是协作编辑的,过去的游戏可被改(改日期/封面/标题)。因此**不要无脑下 `immutable` + 一年 `max-age`**。正确做法:
> - **ETag = `hash(month, count, max(updated_at) of that month)`** —— 该月被编辑过即变,自动失效。
> - 每个响应打 **`Cache-Tag: gal-cal-2026-06`**;编辑保存时按"旧月 + 新月"**定向 purge 那一两个月**,其它过去月的缓存全程不动。
> - Redis 同样按 `gal:cal:2026-06` 存,编辑写穿失效该键;**短 TTL 作为"漏 purge"的自愈兜底**。
> - 不要同时下 `must-revalidate` 与 `stale-while-revalidate`(二者矛盾);CDN 侧若要 SWR,用 `CDN-Cache-Control: max-age=…` 而非 `s-maxage`。

失效时机:编辑器保存一个游戏时,从**旧值与新值**各算出受影响月份(日期可能跨月改动),purge/失效这些月 key;`pending`/`tba` 桶按需失效。

---

## 9. 前端(Nuxt)

- `useAsyncData`,key 为 **computed** `calendar-${ym}`(月变自动重取)+ `getCachedData`(回看秒开);**预取 M±1**;暖切换时保留上月内容并 dim,**仅冷加载出骨架**。
- **月/年跳转选择器**,而非只有 prev/next——远距离跳转用输入年月远快于一格格翻。
- 列表**按日分组 + 周几表头**(`6/27 (金)`),已发售 / 未发售**混在一条时间线**,用 `today` 画一条淡色"今日"分隔(不用大徽章)。
- 月表底部接"年内月份待定(2026)"可折叠桶;TBA 单独入口。
- **空月是一等状态**("2026 年 6 月暂无发售计划"),保留布局。
- 无障碍:日历网格 `role="grid"` + 键盘(PageUp/Down = 上/下月)+ 月份变更 `aria-live` 播报。
- 一律使用 `components/kun/` 设计系统组件,不自造。

---

## 10. 迁移与回填

> **数据库 schema 变更 → 必须跑迁移**:加 `release_precision` 列(+ 可选 `release_ym` generated 列)+ `idx_galgame_calendar` 索引 + `chk_galgame_release_precision` 约束。
> 命令(现为 W5 单一入口):`go run ./cmd/migrate-catalog`,galgame 族对 **`KUN_GALGAME_PG_DATABASE`**(生产 `kun_catalog`,本地 `kun_galgame_wiki`)。生产部署随 compose 自动跑;outage 级变更仍按「先迁移后部署」手动执行。

回填:**精度无法从已塌缩的 `release_date` 反推**(`2026-01-01` 既可能是真 1/1,也可能是"只知年")。需一个一次性回填,从 **VNDB 原始 `released` 串**重解析精度写回:

- 路径 A:`cmd/sync-vndb` 已持有 `vn.Released`,加一个 backfill 子命令重解析 → 写 `release_precision`。
- 路径 B:专用 `cmd/migrate-galgame-release-precision`,从 VNDB dump 重读。
- 用户手填、或来自 EGS/DLsite 的非 VNDB 源,精度按其源的日期格式推导(见 §2.3 overlay)。
- 回填前现有 `release_date_tba=true` 的行 → `release_precision='tba'`;`release_date` 非空但精度未知的行先保守置 `day`,再由重解析覆盖。

---

## 11. 落地阶段

| 阶段 | 内容 | 状态 |
|---|---|---|
| **P1 模型** | 加 `release_precision` 列 + 索引;改 `ParseLegacyReleased` 返回精度;sync 写入;迁移(`cmd/migrate-galgame`)+ 回填(`cmd/migrate-galgame-release-precision`) | ✅ 已实现(待跑迁移+回填) |
| **P2 接口** | `/galgame/calendar`(+ `pending` / `tba`);SARGable 查询 + 封面 preload + meta-ETag(命中 If-None-Match → 304)+ past/current 分治缓存头 | ✅ 已实现 |
| **P3 写侧(精度)** | revision snapshot + ChangedKeys + ApplySnapshot 带精度;create/edit/PR 用 `DeriveInputPrecision` 重算;旧快照回退安全 | ✅ 已实现 |
| **P4 前端月历** | 月历 UI(翻页 / 分组 / 今日 / 空态)—— **本仓 wiki 不需要,已撤除**;月历为下游(forum/moyu)消费,后端 API(P2)保留 | 撤除(下游各自实现) |
| **P4b 部分日期录入** | 放宽 `date_or_empty` validator 接受 `YYYY-MM` / `YYYY` + `NormalizeReleaseDateInput` 归一化(后端✅);精度感知录入控件由下游表单实现 | 后端 ✅ |
| **P5 边界 + 缓存失效** | `has_prev`/`has_next` + `min_month`/`max_month` 边界夹取;每键 `Cache-Tag`(`gal-cal-<month>` 等)供 CDN 定向 purge。编辑失效由 **ETag(嵌 `max(updated)`)自动兜住**——本服务无 Redis 缓存(无需)、无 CDN purge API(接 CDN 时按 tag purge 即可,属运维集成) | ✅ 已实现 |

> P2 实现备注:`content_limit` 走 query 参数(默认 `sfw`,与 `List` 一致)→ URL 完整决定缓存。月接口返回 `{month, today, items, links{self,prev,next}, meta{prev_month,next_month,count}}`;**`has_prev/has_next` 边界夹取暂缓**(前端可自由翻页,空月是一等状态)——待 P5 一并补,届时 `has_next` 由"最晚有数据的月"判定。过去月用 `max-age=0, s-maxage=86400, swr=3600` + 弱 ETag,当前/未来月 `s-maxage=300, swr=60`;`max-age=0` 让浏览器每次以 ETag 复验(304 廉价、编辑不会被缓存成陈旧),CDN 按月定向 purge 在 P5 接。

---

## 11.5 类型化契约(code-first OpenAPI → 生成式 TS)

月历三端点的响应现由**类型化 DTO**（`dto.CalendarItem` / `CalendarMonthData` / `CalendarPendingData` / `CalendarTBAData`，见 `internal/platform/galgame/dto/calendar_dto.go`）单源产出：Fiber handler 用它们组装 body（**保留 ETag/304 + Cache-Control 条件缓存** —— 这套缓存语义不适合 Huma 的类型化 serving 模型，故月历**继续由 Fiber serving**），同一批 DTO 又经 Huma 导出 OpenAPI（`SetupCalendarSpec` + `gen-openapi -galgame-calendar` → `docs/galgame_wiki/calendar-openapi.yaml`），再经 `openapi-typescript` 生成 `apps/web/shared/types/generated/galgame-calendar-api.ts`。

- **不漂移的保证**:`calendar_dto_test.go` 用 `assert.JSONEq` 把 `CalendarItem` 钉死为「月历预加载的 `model.Galgame`」的 JSON 孪生（含 covers / official.official / effective_banner_hash），所以这次改造是**零线缆变更**;`test.yml`(code→spec)+ `openapi-types.yml`(spec→TS)两道 CI 门闭环 code→spec→TS。
- **重新生成**:`go run ./cmd/gen-openapi -galgame-calendar -o ../../docs/galgame_wiki/calendar-openapi.yaml` 然后 `pnpm -F web run gen:types:galgame-calendar`。
- 这是 galgame-wiki 读端点 Huma 化的**第一片**(item 2 / step 2);其余读端点(详情 / batch / search / …)复用这套 DTO+spec+drift-gate 图式。

## 12. 关键文件 & 参考

关键文件:

- 类型化契约 / DTO:`internal/platform/galgame/dto/calendar_dto.go`(+ `calendar_dto_test.go` 线缆孪生测试);spec 注册 `internal/platform/galgame/handler/calendar_huma.go`
- 模型 / 解析:`internal/platform/galgame/model/`(`Galgame`、`Date`、`ParseLegacyReleased@snapshot.go`)
- 同步:`internal/jobs/vndbsync/`、`cmd/sync-vndb`
- 搜索(已有月精度):`internal/platform/galgame/search/`
- 多源 overlay:见 [07](./07-multi-source-aggregation-design.md)

外部标准 / 上游(本设计依据,非项目内组件):

- EDTF / ISO 8601-2:2019(不完整日期表达),Library of Congress: <https://www.loc.gov/standards/datetime/>
- ISO 8601 `YYYY-MM` 年月形态: <https://en.wikipedia.org/wiki/ISO_8601>
- VNDB(本项目发售日上游数据源)发售日精度约定: <https://api.vndb.org/kana>
- PostgreSQL 索引与 SARGability: <https://www.postgresql.org/docs/current/indexes-ordering.html>
- HTTP 缓存(RFC 9111)/ `stale-while-revalidate`(RFC 5861): <https://www.rfc-editor.org/rfc/rfc9111.html>
