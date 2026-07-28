# 公开 API 面与端点(精选 + 版本化)

> 本文承载 §3.1 原则、§3.2 v1 端点清单、§3.5 稳定性承诺与演进条款、§10 OpenAPI 策略。设计与命名约定见 [01-design.md](./01-design.md);§3.3 跨面互链、§3.4 面挂载模型见 [01 §3](./01-design.md)。

---

## 3. 公开 API 面与端点(精选 + 版本化)

### 3.1 原则

- **白名单暴露**:只把精选的只读端点放进 `api.nextmoe.dev/v1/…`;internal / admin / 写端点**永不**进入公开路由(物理上不挂到公开路由组)。
- **URL 版本化** `/v1/`:一旦有了无法协调破坏性变更的外部开发者,版本化与弃用策略从"过早优化"变成"硬需求"。
- **弃用策略**:破坏性变更必须升 `/v2/`;字段级弃用走 `Deprecation` / `Sunset` 响应头 + 门户公告 + 不少于 N 个月窗口。
- **路径命名空间 = 面**:`/v1/catalog/*`、`/v1/galgame/*`,未来 `/v1/manga/*` 等。galgame 的领域词表(officials/tags/engines/series)全部收进 `/v1/galgame/` 之下,给未来媒介留干净的顶层命名空间。
- **公开投影与内部契约解耦**:公开面是从既有 Huma spec 精选出的**独立 spec**;内部 S2S/站点契约继续自由演进,互不牵制。

### 3.2 v1 端点清单(草案)

> §3.3 跨面互链、§3.4 面挂载模型见 [01 §3](./01-design.md)。

**galgame 面**(后端 = `cmd/catalog` 承载的 galgame 面(W3 起;撰文时为 `cmd/galgame`),内容真源):

| 公开端点(`/v1`) | 映射内部 | scope | 说明 |
|---|---|---|---|
| `GET /v1/galgame` | `GET /galgame`(List) | `galgame:read` | 分页/排序/搜索/发售范围;**游标分页**(见 [04 §8 备注](./04-platform-internals.md)) |
| `GET /v1/galgame/{id}` | `GET /galgame/:gid` | `galgame:read` | 详情;响应携带 `catalog_work_id`(跨面互链,见 [01 §3.3](./01-design.md)) |
| `GET /v1/galgame/batch` | `GET /galgame/batch` | `galgame:read` | 批量(brief/detail) |
| `GET /v1/galgame/search` | `GET /galgame/search` | `galgame:read` | Meilisearch |
| `GET /v1/galgame/calendar*` | calendar 三件套 | `galgame:read` | 已有 ETag/缓存,直接复用 |
| `GET /v1/galgame/officials` `…/{id}` `…/{id}/galgames` | official List/Get/members | `galgame:read` | 会社目录 + 成员 |
| `GET /v1/galgame/tags` `…/{id}` `…/{id}/galgames` | tag | `galgame:read` | |
| `GET /v1/galgame/engines` / `GET /v1/galgame/series` … | engine/series | `galgame:read` | |
| `GET /v1/galgame/changes` | (新增,updated 时间戳 keyset) | `galgame:read` | **变更流**(doc 19 D5,Phase 1):增量同步游标,管理器免全量重爬 |
| (Phase 3)`POST /v1/galgame/{id}/submit` 等 | 投稿/PR | `galgame:submit` | 需 OAuth2 用户授权 |

**catalog 面**(后端 = `cmd/catalog`,跨媒介身份/图谱真源):

| 公开端点(`/v1`) | scope | 说明 |
|---|---|---|
| `GET /v1/catalog/works/{id}` | `catalog:read` | 注册行:display_name / titles / medium / 分级 / 外部锚(来源白名单过滤,见 [06 §11](./06-security-compliance.md))/ **认领指针**(→ 内容面路由,见 [01 §3.3](./01-design.md))+ **全量聚合 facet**(wave 104 加法扩容:popularity/ratings/tags/playtimes/series/platforms/intro/covers/screenshots/characters/labels/releases——source 键归因、CDN 完整 URL、字符串词表);**R18 调用方自控**:`nsfw=1` 出 r18 作品与 r18 关系端(works/lookup/names/characters/labels 同参;characters 另有 `spoilers=0-2` + sexual traits 随 nsfw),缺省隐藏与 Phase-1 逐字节一致 |
| `GET /v1/catalog/works/{id}/credits` | `catalog:read` | 该作品的 credits(名义/角色/role) |
| `GET /v1/catalog/works/{id}/relations` | `catalog:read` | 跨媒介关系(改编/续作/同世界观…,单行双向渲染) |
| `GET /v1/catalog/names/{id}`(+ `…/credits`) | `catalog:read` | 名义(credited identity;{id}=credit_name id,携 person_id+公开 sibling 名义)——**hidden 名义链接不出现在公开聚合**(既有可见性政策)。v2.1 实施时由 persons/{id} 更名:实体层 credits 指向名义而非 person,公开词表与 resolve/redirects 的 "name" 键统一 |
| `GET /v1/catalog/characters/{id}` | `catalog:read` | 角色(含出演,spoiler 级字段) |
| `GET /v1/catalog/labels/{id}`(+ `…/works`) | `catalog:read` | 厂牌/文库/社团;恒带 `intros[]`(多语言简介,按语言归并、`source`=来源键)与 `links[]`(官网/twitter/ci-en 外链,`{source,url}`;身份锚 exact/probable 永不入 `links`),无供给则为 `[]` |
| `GET /v1/catalog/search` | `catalog:read` | 实体搜索(persons/characters/labels,复用三索引) |
| `POST /v1/catalog/resolve` | `catalog:read` | 批量旧 ID → canonical(redirect 压平语义与内部一致) |
| `GET /v1/catalog/lookup` + `POST …/lookup/batch` | `catalog:read` | **外部 id 反查(killer,doc 19 §3.1,Phase 1)**:`?source=vndb&external_id=v19658` → work + `claimed_by` 指针;批量 ≤100。背书 = 四源 exact 锚(在产) |
| `GET /v1/catalog/redirects` | `catalog:read` | id 收敛事件 keyset 流(内部 S2S 面公开化,doc 19 §3.3) |

> 不进入公开路由:`/admin/*`、人审队列、merge/claim 等 S2S 写面、`/:gid/revert`、消息队列、site 管理等。
> catalog 面范围备注:`stub`(无锚且元数据不达标的未认领行)不进公开聚合——既有不变量,公开面直接继承;asmr/同人未认领波是否进 v1 投影,并入 [01 §15](./01-design.md) 再分发授权一起拍板(倾向:v1 先只放 galgame 可达闭包 + 跨媒介关系可达行,letmoe 上线时再扩)。

### 3.5 稳定性承诺

- 已发布字段不删不改语义;只做**向后兼容**的新增。
- 公开 `content_limit` 语义统一(见 [06 §11](./06-security-compliance.md));各端点默认 = `sfw`。
- catalog 面的实体 ID 全局稳定,合并只产生 redirect,永不复用。~~`w`/`p`/`n`/`b`/`c` 前缀~~(superseded,2026-07-15 步骤 03 裁定 2:公开 id = 纯数字——与 galgame 面已冻结的 `catalog_work_id` 数字形态一致,路径已按实体类型分命名空间)。公开线源键 = 站点真拼写(`erogamescape`;内部注册表键 `erogamespace` 在投影层映射,lookup 双拼容错)。

**演进条款**(step 07 落账,Phase 2「查询灵活性」引入时形式化;五条共同定义"什么样的改动是加性、什么样必须升版本"):

1. **加法优先,永不改语义**:已发布字段的名称、类型、含义与 null 语义一律冻结;演进只能是**新增**可选字段 / 可选查询参数 / 新端点。任何"改"都不是加性,一律走破坏性变更流程(第 3 条)。新增可选参数(如 `include` / `fields`)与新增可选响应键,对既有客户端逐字节无影响——缺省响应恒等于冻结契约。
2. **客户端「必须忽略未知字段」= 契约条款**(升格):公开响应可能在任何时候新增字段;**合规客户端必须容忍并忽略它不认识的字段**。对称地,服务端对 `fields=` / `include=` 里的未知名**静默忽略、绝不 400**(双向前后兼容:老客户端遇新字段不炸,新客户端拼错字段名不炸)。这条对侧承诺正是"加法优先"能成立的前提——加字段对所有正确实现的消费者都无破坏。
3. **破坏性变更 = `/v2` 并行**:确需改语义 / 删字段 / 改类型时,新增 `/v2` 与 `/v1` **并行运行**,旧版打 `Deprecation` / `Sunset` 响应头 + 门户 changelog 公告 + **不少于 12 个月**的迁移窗口;窗口内 `/v1` 不下线、语义不动。
4. **内部面 = 公开契约的试验缓冲层**:新字段 / 新形状先在内部 S2S / 站点读面消化验证,形状稳定后再投影到公开面冻结。公开面永远是内部契约的**精选滞后投影**,不承载未经内部实战的实验形状——这样绝大多数迭代压力被内部面吸收,公开契约的破坏性变更趋近于零。
5. **新数据源 = 加键,新媒介 = 加面**:新增第四 / 第五源评分或外部锚,是在 `refs` / `scores` 等**键控对象**上加键(这正是把它们设计成键控对象而非并列标量字段的本意);新增媒介(manga / novel…)是加新面 `/v1/<medium>/*`。两者都是加性演进,天然不触碰既有契约。

---

## 10. OpenAPI 策略

v1 设计时"galgame 无 spec"的前提已过时——现状是**两个面都有 code-first spec**,工作量收敛为"公开投影":

- **galgame 面**:读面已 Huma 出谱(条件缓存端点为 spec-only 形态)。公开 `/v1` 投影 = 沿同一管线(`cmd/gen-openapi` 加一个 public 目标)产出**独立的公开 spec**(白名单端点 + `/v1` 前缀 + 公开 DTO),与内部 spec 解耦。
- **catalog 面**:服务自带 Huma spec(`/openapi.json`)。同法产出公开投影(白名单只读子集)。
- 产出 `api.nextmoe.dev/v1/catalog/openapi.json` 与 `…/v1/galgame/openapi.json` → 门户 Scalar 渲染 → 第三方据此生成 SDK(TS 优先,`@kungal/api-*` 发包纪律届时启用)。
- 公开 spec 纳入 `docs:verify` + oasdiff 破坏性门,升级为 **Tier-A 对外契约**(在 kungal-docs 登记)。
