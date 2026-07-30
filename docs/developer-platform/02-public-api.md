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
>
> **⚠️ 弃用(doc 106,2026-07-28)**:`/v1/galgame/*` 是 kungal 产品读面(wiki body 投影),**非** canonical 数据 API。数据消费者请迁移到 `/v1/catalog/*`(端点/字段映射见 `refs/proj/106` §6)。全端点带 `Deprecation` + `Sunset: Sat, 31 Oct 2026 00:00:00 GMT` + `Link: <…/v1/catalog>; rel="successor-version"` 响应头,spec 内标 `deprecated`。绞杀式退场 90 天窗(平台内测、零第三方 key,故 §3.5 的 12 个月条款不适用)。

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
| `GET /v1/catalog/works/{id}` | `catalog:read` | 注册行:display_name / titles / medium / 分级 / 外部锚(来源白名单过滤,见 [06 §11](./06-security-compliance.md))/ **认领指针**(→ 内容面路由,见 [01 §3.3](./01-design.md))+ **全量聚合 facet**(wave 104 加法扩容:popularity/ratings/tags/playtimes/series/platforms/intro/covers/screenshots/characters/labels/releases——source 键归因、CDN 完整 URL、字符串词表);**R18 调用方自控**:`nsfw=1` 出 r18 作品与 r18 关系端(works/lookup/names/characters/labels 同参;characters 另有 `spoilers=0-2` + sexual traits 随 nsfw),缺省隐藏与 Phase-1 逐字节一致;`updated` 恒在(doc 106);`releases[]` 每行带 `id`+`refs[]`,`tags[]` 每行带 canonical `canonical_id/tier/kind`(doc 106,未映射省略)。**A2-1e 加法**:`created`(RFC3339,注册行**进入 catalog 的时刻**——既不是发售日也不是产品侧创建时间)、`engines[]`(`{id,name}`,恒出空为 `[]`)、`links[]`(非身份外链,见 §3.2.2)、`labels[]` 每行 `lang`、`tags[]` 的**安全轴** `spoiler`/`sexual` + `spoilers=0\|1\|2` 参数(见 §3.2.3)。**A2-R1 修复**:`titles[]` 对**认领作品**来自 wiki 桥(四名称列 + 别名,见 §3.2.5)——此前认领作品的中文名/别名整体缺席;`labels[]`/`engines[]` 每行恒带 `work_count`、`tags[]` 映射行带 `work_count`(nsfw 感知,见 §3.2.6) |
| `GET /v1/catalog/works` | `catalog:read` | **作品浏览/列表(doc 106 G1,keyset)**:过滤 `content_rating`/`claimed`/`label_id`/`tag_id`(canonical)/`series_id`/**`engine_id`(A2-1b 第九过滤器,经 `catalog_work_engine`)**/`platform`/`released_after\|before`/`ids`(≤100);`sort=id\|updated`;item = 轻 brief(+`release_date`/`olang`/`cover` 单图/`updated`);`nsfw` 同参;`next_cursor` 末页 null。**`include=` 富 brief 块(A2-1a 加法波)**:词表 `names,intros,labels,ratings,covers`(逗号分隔,**未知 token 静默忽略**,§3.5 条款 2);每块按页内 work id **批量加载**(无 N+1),未点名即整块缺席——**缺省(无 `include=`)响应与本波前逐字节相同**。`names`/`intros` 走 D7 四键投影(见 §3.2.1 表①),`labels`/`ratings` 与详情面同形同口径(评分保持源原生分制,不聚合),`covers` 出 `{portrait, banner}` 两槽、每槽带 `width/height/thumbhash`(见 §3.2.1);`ids=` + `include=` 即批量富取(两梯队的 batch 替代面)。**A2-1e**:`include=` 词表加 `refs`(该作品的 **exact 身份锚**,与详情面 `refs[]` 同构——work 级 ∪ release 级去重,exact-only 红线不破),`tag_id` 收**逗号分隔多值 AND**(≤10,见下)。**A2-R1 修复**:`names` 块对**认领作品**来自 wiki 桥(见 §3.2.5);`labels` 块每行恒带 `work_count`(与详情面同数,见 §3.2.6) |
| `GET /v1/catalog/changes` | `catalog:read` | **增量同步流(doc 106 G2,keyset)**:`{entity_type=work, cursor, limit}` → `[{entity_type, id, updated}]`;`next_cursor` 恒在(续轮询新行);**无 nsfw 门**(id+时间戳=身份非内容,详情跟查再门控)。**删除不经此流**——行离开 LIVE 集(软删/降级/退出 galgame 媒介)后只是从流中**静默消失**,不发 tombstone;**合并型消亡由 `GET /v1/catalog/redirects` 覆盖**(旧 id → canonical id),**镜像型消费者应周期性全量对账**(`works?sort=id` keyset 扫 id 全集,与本地镜像取差集即失效行)。`op` 字段登记为将来的加法扩展位(现不下发,消费端须按 §3.5 条款 2 忽略未知字段)。**流有意滞后 ~5 秒**(2026-07-28 cleanup 波):`updated_at` 是**语句时间**而非提交时间,不设滞后则长事务可能提交出一行 `updated_at` 已落在消费者水位之后的记录 → 该行被**永久跳过**;拒发 5 秒内的新行,使提交耗时 ≤5s 的在途事务不可能被漏掉 |
| `GET /v1/catalog/works/{id}/credits` | `catalog:read` | 该作品的 credits(名义/角色/role) |
| `GET /v1/catalog/works/{id}/relations` | `catalog:read` | 跨媒介关系(改编/续作/同世界观…,单行双向渲染) |
| `GET /v1/catalog/names/{id}`(+ `…/credits`) | `catalog:read` | 名义(credited identity;{id}=credit_name id,携 person_id+公开 sibling 名义)——**hidden 名义链接不出现在公开聚合**(既有可见性政策)。v2.1 实施时由 persons/{id} 更名:实体层 credits 指向名义而非 person,公开词表与 resolve/redirects 的 "name" 键统一 |
| `GET /v1/catalog/characters/{id}` | `catalog:read` | 角色(含出演,spoiler 级字段) |
| `GET /v1/catalog/labels` | `catalog:read` | **厂牌浏览/列表(A2-1b,keyset id ASC)**:过滤 `kind=`(封闭词表 `game_brand\|bunko\|publisher\|anime_studio\|doujin_circle\|group`,非法 token 400);item = `{id, display_name, kind, work_count}`;**合并走掉的厂牌不出列表**(merge 软删源行 + 写 redirect,旧 id 仍由 `/v1/catalog/redirects` 覆盖)。**A2-1e**:信封加 `total`(见下) |
| `GET /v1/catalog/labels/{id}`(+ `…/works`) | `catalog:read` | 厂牌/文库/社团;恒带 `intros[]`(多语言简介,按语言归并、`source`=来源键)与 `links[]`(官网/twitter/ci-en 外链,`{source,url}`;身份锚 exact/probable 永不入 `links`),无供给则为 `[]`;`refs[]` exact 身份锚(doc 106)。**A2-1e 加法**:`aliases[]`(别名扁平字符串,**排除 display_name**、跨语言同拼写去重,恒出)、`lang`(display_name 自身的 BCP-47 标签,未记录则省略)、`work_count`(**nsfw 感知**,与 `labels` 列表行同一聚合——详情页与来路列表永不打架) |
| `GET /v1/catalog/tags` | `catalog:read` | **规范 tag 浏览/列表(A2-1b,keyset id ASC)**:过滤 `tier=`(`core\|longtail\|hidden`)、`kind=`(`content\|meta`),两者皆封闭词表、非法 token 400;item = `{id, name, tier, kind, work_count, sexual}`(**A2-1f** 补 `sexual`,见 §3.2.4)。**A2-1e**:信封加 `total`(见下) |
| `GET /v1/catalog/tags/{id}` | `catalog:read` | **规范 tag(doc 106 G5)**:`{id, name, tier, kind}`(跨源规范词表 catalog_tag);**恒带 `intros[]`(A2-1b 加法)**——多语言简介,shape 与 `labels/{id}` 的 `intros[]` 一致(`{lang, intro, source}`、按语言归并低 source_id 胜出、`source`=公开来源键),无供给则为 `[]`;`include=works` 附带该规范 tag 下的作品(经 catalog_tag_source_map ⋈ catalog_work_tag,nsfw 门),按 `limit`/`offset` 翻页,**满页时**带 `next_offset`(= `offset+limit`),不满页则省略 = 到底。**A2-1e 加法**:`work_count`(**nsfw 感知**,与 `tags` 列表行同一聚合);**A2-1f 加法**:`sexual`(tag 级性内容轴,与列表行同一派生,见 §3.2.4) |
| `GET /v1/catalog/engines` | `catalog:read` | **引擎浏览/列表(A2-1b,keyset id ASC)**:无过滤;item = `{id, name, work_count, description, aliases}`(**A2-1e** 补齐后两键——引擎 facet 只有几百行、消费端一页渲染完,再为一行简介发第二趟请求是纯浪费)。VNDB 不发布引擎数据,该 facet 的唯一副本是 wiki 手工整理并由数据层退役波迁入的行。**A2-1e**:信封加 `total`(见下) |
| `GET /v1/catalog/engines/{id}` | `catalog:read` | **引擎条目(A2-1b)**:`{id, name, work_count, description, aliases, refs[]}`(后两键 A2-1e 补);`refs[]` 同 names/characters/labels 的 exact-only 身份锚(doc 106 G4),A2-0 落的 wiki eid 即在此浮出。非法 id 400、无此行 404 |
| `GET /v1/catalog/calendar` | `catalog:read` | **发售日历 · 月桶(A2-1c,keyset date ASC + id ASC)**:`month=YYYY-MM`(非法 400;**缺省 = 当前 Asia/Tokyo 月**,响应回显 `month`);收录**最早带年份 release 落在该月**的作品——**day 精度与 month 精度同桶**(month 精度排在该月月首,**不臆造 1 号**);item = works 列表行**逐字**(`PublicWorkListItem`,`include=` 五词表全支持),`nsfw` 同参;新增 `olang=` 人口过滤(见下);`count` = 整桶行数(非本页),`next_cursor` 末页 null;带**桶级 ETag**(见下);**A2-1e**:恒带 `meta{}` 导航框(见下) |
| `GET /v1/catalog/calendar/pending` | `catalog:read` | **发售日历 · 月份未定桶(A2-1c,keyset id ASC)**:`year=YYYY`(非法 400;缺省 = 当前 JST 年,响应回显 `year`);收录**最早 release 只精确到年**的作品——它们**刻意不出现在该年的任何月桶**里。人口/item/`olang`/ETag 语义与月桶逐字一致;`meta` 只带 `today`(非月寻址,无月界与前后翻) |
| `GET /v1/catalog/calendar/tba` | `catalog:read` | **发售日历 · TBA 桶(A2-1c,全局,keyset id ASC)**:有 release 行但**无一行带年份**的作品(已官宣、日期未定)。**无 release 行 = unknown,不进任何桶**——"没有 release"是"没有官宣",不是"日期待定" |
| `GET /v1/catalog/works/search` | `catalog:read` | **作品产品搜索(A2-1d,doc 126 D5;page/limit 分页)**:自由文本 `q=` 命中作品的**全部索引标题/别名**(含 search hint,仅供检索永不下发);**`search_intro=1` 另放宽到作品简介**(A2-1f,见 §3.2.4);过滤 `tag_id`(**多值 AND**,同列表)/`label_id`/`engine_id`/`series_id`/`released_after\|before`/`olang`/`content_rating`/`claimed`/`nsfw`——**与 works 列表同名参数逐字同义**(`released_*` 同样锚在**最早带年份 release** 的组合序数上,与列表 `release_date`、日历分桶三者同源)。`sort=relevance\|released_desc\|released_asc\|updated\|popularity`(缺省 relevance;**空 q 时 relevance 退化为 popularity** 即浏览序;`released_*` 两个方向都把**无日期作品排在最后**;`popularity` = 跨源信号 `log1p(max(bangumi collect 架, DLsite 下载数))`,**替代弃用面的 `view`**——那是 wiki 浏览量,catalog 无对应物,故 `sort=view` 是 400)。`facets=` 封闭词表 `content_rating,olang,claimed,tag_id,label_id,engine_id,series_id,source`(**非法 token 400**;外层键 = 可直接回传的**过滤参数名**,非索引字段名;`content_rating` 分布按公开字符串键计数不出枚举整数;每 facet 至多 100 个值)。`include=` 六词表全支持。**item = works 列表行逐字**(`PublicWorkListItem`,按 id 回库水化;**Meili 文档字段永不出 wire**)。`page` 缺省 1、非正/非数字 400,`limit` 1-100 缺省 20(超限截顶、非正/非数字 400)。**`q` 恰为 VNDB 作品 id(`v19658`)时短路**为该 id 的 exact 锚精查(全文会前缀串味:`v1965` 亦命中 `v19650`),仍套用调用方全部过滤器,**无解 = 空信封而非 404**。**`total`/`facets`/`items` 同门过滤**:翻完 `total` 页恰好收满 `total` 行,sfw 调用方的 `total` **已扣除**其永远拿不到的 r18 作品——**与弃用面 `content_limit` 陷阱(总数不过滤、items 过滤、sfw 翻页丢行)明令相反**。**A2-R1 区 C 加法**:`claim_state=`(封闭词表 `none\|live\|draft\|hidden`,逗号分隔 IN 语义,非法 token 400,不传=不闸)——产品站搜索务必传 `claim_state=live` 以排除未发布/未认领行,见 §3.2.7 |
| `GET /v1/catalog/search` | `catalog:read` | **实体自动补全**(`type=names\|characters\|labels\|works\|tags`,五索引;**`tags` 为 A2-1d 加法**,hit 镜像 labels 惯例并附 `tier`/`kind`,其余四族 hit shape **逐字节冻结**)。至多 20 条扁平 hit、无过滤无分页 —— picker / 跳转框用面;**作品结果页(过滤/facets/排序/翻页/完整列表行)走 `GET /v1/catalog/works/search`** |
| `POST /v1/catalog/resolve` | `catalog:read` | 批量旧 ID → canonical(redirect 压平语义与内部一致) |
| `GET /v1/catalog/lookup` + `POST …/lookup/batch` | `catalog:read` | **外部 id 反查(killer,doc 19 §3.1,Phase 1)**:`?source=vndb&external_id=v19658` → work + `claimed_by` 指针;批量 ≤100。背书 = 四源 exact 锚(在产)。**`type=work\|name\|character\|label`(缺省 `work`,加法扩展)**:同一反查面按实体族分流——`work` 语义逐字不变(含 release 锚回落到属主 work),其余三族取**该族** exact 锚后委派各自详情投影(重块关闭),命中只填对应块 `name` / `character` / `label`,`work` / `claimed_by` 留空;批量每对可各带 `type`,响应回显**归一后**的 token(缺省对回显 `work`) |
| `GET /v1/catalog/redirects` | `catalog:read` | id 收敛事件 keyset 流(内部 S2S 面公开化,doc 19 §3.3) |

> 不进入公开路由:`/admin/*`、人审队列、merge/claim 等 S2S 写面、`/:gid/revert`、消息队列、site 管理等。
> catalog 面范围备注:`stub`(无锚且元数据不达标的未认领行)不进公开聚合——既有不变量,公开面直接继承;asmr/同人未认领波是否进 v1 投影,并入 [01 §15](./01-design.md) 再分发授权一起拍板(倾向:v1 先只放 galgame 可达闭包 + 跨媒介关系可达行,letmoe 上线时再扩)。
> **doc 106 加法(2026-07-28)**:`refs[]`(exact-only 身份锚)现同构出现在 names / characters / labels(此前仅 works 有);works 浏览列表 + changes 增量流 + 规范 tag 读面补齐了「可浏览 / 可增量同步 / release 与 tag 可寻址」四缺口。全部加法,spec-breaking 门背书;S2S/admin spec 逐字节不变。
>
> **A2-1b taxonomy 读面加法(2026-07-29)**:三条 keyset 列表道(`labels` / `tags` / `engines`)+ `engines/{id}` + works 的 `engine_id` 过滤 + `tags/{id}` 的 `intros[]` + 详情面 `screenshots[]` 的 `width/height/thumbhash`。**公开 lookup 词表不扩**——仍是 `work\|name\|character\|label` 四族;engine / tag 的 id 解析走各自 detail / list 面,不进 lookup(它们是分类词表,不是可反查的跨源身份族)。全部加法,oasdiff 零 breaking。
>
> **A2-1c 发售日历加法(2026-07-29)**:三个 keyset 桶(`calendar` / `calendar/pending` / `calendar/tba`)+ 桶级 ETag + 新 `olang=` 人口参数。**item 零新字段**——就是 works 列表行本身(`include=` 词表一并继承),所以日历行和浏览行用同一套渲染代码;日历也**不新增**任何数据源或精度字段,它只是把既有的作品级 `release_date` 按序数分桶(语义见下)。全部加法,oasdiff 零 breaking。
>
> **A2-1e 供给补全加法(2026-07-29)**:本波**不新增端点**,只把既有端点上「消费方已经在用、catalog 侧却没有出口」的供给补齐。清单:`claimed_by.state`(R7)、engines `description`/`aliases`、labels `aliases`/`lang` 与 `labels[].lang`、works 列表 `include=refs`、详情 `engines[]`/`links[]`/`created`、三条 taxonomy 列表的 `total` 与 `labels/{id}`+`tags/{id}` 的 `work_count`、`tag_id` 多值 AND、日历 `meta{}`、`tags[]` 安全轴 + `spoilers=`。全部加法,oasdiff pinned 1.21.0 零 breaking;**缺省响应逐字节不变**(唯一新增的请求参数 `spoilers` 默认 0 = 旧行为)。
>
> **`claimed_by.state` 语义(R7)**:`claimed_by` 从此恒带 `state`,词表 `live | draft | hidden` —— 这是 **catalog 自有的认领可见性词表**,不是任何产品的状态机(产品状态值永不进公开面)。
>
> | `state` | 含义 | 消费端 |
> |---|---|---|
> | `live` | 认领在产品面**公开可见** | 正常跟随指针,渲染认领徽章 |
> | `draft` | 存在但**尚未发布**(编辑态) | 不渲染产品内容;徽章可选,但不得当作已发布 |
> | `hidden` | 产品已**撤下**(封禁/退回) | **既不出徽章也不出内容** |
>
> - 这一位解决的是「`claimed_by` 是状态盲的」这个结构缺陷:没有它,下游按 `claimed_by` 再锚定会把产品已经撤下的词条在自己站上复活。
> - 投影由认领方的对账器维护(wiki:published→live、vndb-draft/pending→draft、banned/declined→hidden);**没有 draft/hidden 生命周期的认领方**(letmoe 等)不写这一列,读面渲染 `live`。
> - **词表外的值一律读作 `hidden`**——不认识的状态绝不对外发布。未认领行 `claimed_by` 仍是 `null`(不是一个带 state 的对象)。
>
> **taxonomy 三道的 `total` 语义**:等于**同一组过滤器**下的**整集**行数(不是本页、也不是游标之后的余量),所以把一条道翻到底收集到的行数恰好等于 `total`。它**不随 `nsfw` 变**——厂牌/tag/引擎行是身份而非内容,`nsfw` 在这三条道上只管每行的 `work_count`。
>
> **`tag_id` 多值(AND)**:`tag_id=7,12` = 该作品必须**同时**带映射到 7 和 12 的源 tag(facet 侧栏「再缩一个 tag」的语义),列表面与 `works/search` 逐字同义。**上限 10**;超限、非正整数、非数字一律 `400 tag_id must be up to 10 comma-separated positive integers`(**绝不静默丢过滤器**)。**单值行为与本波前逐字节相同**,重复 id 折叠。
>
> **日历 `meta{}` 导航框(R10)**:恒在。`today` = **Asia/Tokyo 当日**(`YYYY-MM-DD`,与缺省月/年同一时区),三个桶都有。月桶另有 `min_month`/`max_month`(**该调用方自己的人口门下**最早/最晚有成员的月)与 `has_prev`/`has_next`(由请求月对上述边界推导)。
>
> - **同门保证**:边界跑在 `nsfw` × `olang` 的**同一组门**下,所以「最新的非空月」= 「你自己能看到东西的最新月」——sfw 与 nsfw 调用方拿到不同边界是正确行为,不是不一致。
> - `has_next=false` 是**真的到头了**,不是「下个月恰好为空」;空月回跳直接用 `max_month`,不必逐月试探。
> - **人口为空**时 `min_month`/`max_month` **省略**(没有可跳转的月就不编一个),`has_prev`/`has_next` 仍明确给 `false`。
> - `pending`/`tba` **只带 `today`**:它们不是按月寻址的桶,前后翻箭头在那里没有指向。
> - **不进 ETag 键**:`meta` 完全由「桶级 ETag 已经折进去的人口键」加「写在 URL 里的请求月」决定,没有第三个自由度,所以缓存校验子不需要因它变化;它也在 `304` 短路**之后**才计算——命中缓存的请求依旧只付一次元查询。

> **A2-1f 供给微波(2026-07-29)**:`works/search` 加 `search_intro=`(简介检索,缺省关)+ `tags` 列表行与 `tags/{id}` 加 `sexual`。两项均为加法,缺省响应逐字节不变,oasdiff 零 breaking。语义见 §3.2.4。**部署注**:`search_intro` 需要跑一次 `reindex-catalog` 才有内容可匹配。

> **参数区间与越界语义(2026-07-28 cleanup 波)**:
>
> | 端点 | `limit` 区间 | 默认 |
> |---|---|---|
> | `GET /v1/catalog/works` | 1-100 | 20 |
> | `GET /v1/catalog/labels` / `…/tags` / `…/engines`(A2-1b 三条 taxonomy keyset 道) | 1-100 | 20 |
> | `GET /v1/catalog/calendar` / `…/calendar/pending` / `…/calendar/tba`(A2-1c 三个日历桶) | 1-100 | 20 |
> | `GET /v1/catalog/changes` | 1-500 | 100 |
> | offset 型子列表(`names/{id}?include=credits`、`characters/{id}` / `labels/{id}` / `tags/{id}?include=works`) | 1-50 | 50 |
>
> - **越上限 clamp 到上限**(不回落默认值):`limit=1000` 在 works 面即 `limit=100`,而不是悄悄退回 20。
> - **非正数 / 非数字 400**:`limit=0`、`limit=-1`、`limit=abc` 一律 `400 limit must be a positive integer`,不再静默取默认值。
> - **`label_id` / `tag_id` / `series_id` / `engine_id` 同理**:缺省/空 = 不过滤;一旦给值就必须是正整数,`abc` / `0` / `-5` / `1.5` 一律 400(旧行为把非法值退化成 0 → 过滤器**静默消失**、返回不过滤的首页,是最坏的一类失败)。
> - **游标不跨道**:每条 keyset 道(works `id` / works `updated` / changes / labels / tags / engines / calendar / calendar-pending / calendar-tba)的 `next_cursor` 只在本道有效,拿去另一条道一律 `400 malformed cursor`。
> - `offset` 保持宽松(负数归 0,不 400)。
>
> **lookup `type` 词表(2026-07-29 加法波)**:`work`(缺省)/ `name` / `character` / `label`,两个面(GET 单查 + POST 批量)同一套。
>
> - **非法 token 一律 400**(`type must be one of work, name, character, label`):`type` 是**我方封闭词表**,拼错即调用方错误;批量中**任一对**非法即整个请求 400,不把该槽悄悄降级成 miss。对照:**未知 `source` 仍是 miss/404**——来源是开放注册表,不该因为我们尚未收录某个站点就把调用判为错误。
> - **`external_id` 归一只发生在 `work` 面**:vndb 作品接受 `v19658` 或裸 `19658`;`name` / `character` / `label` 按注册表存法**逐字匹配**(vndb 角色 `c1234`、厂牌 `p129`、staff 是**裸数字**)——给非作品面补 `v` 前缀只会 100% miss。
> - **可见性继承各实体详情面**:命中后委派 `names/{id}` / `characters/{id}` / `labels/{id}` 的投影(`include` 重块关闭),因此 `nsfw` 语义与那三个端点逐字一致(例:character 身份不因 `nsfw=0` 隐藏,只掉 sexual traits;r18 隐藏仍只是 `work` 面的规则)。
> - **响应加法**:`PublicLookupData` / 批量 item 新增可选块 `name` / `character` / `label`(不命中即整块省略),`work` / `claimed_by` 字段语义不变;批量 item 另加恒在的 `type` 回显。spec-breaking 门(oasdiff)背书为非破坏。
>
> **taxonomy 三道的 `work_count` 语义(2026-07-29 A2-1b 落账)**:`labels` / `tags` / `engines` 每行的 `work_count` 是 **nsfw 感知**的——它等于**同一调用方**用 `works?label_id=` / `?tag_id=` / `?engine_id=` 翻页能真正拿到的行数。
>
> - sfw 调用方(缺省)的计数**剔除 r18**;`nsfw=1` 给全量。计数与成员列表**永不打架**——这是刻意反着写弃用面的 `official.galgame_count`(恒 0 却挂着非空成员列表)。
> - 统计口径 = works 列表的种群谓词逐字复用:LIVE + galgame 媒介 + 未软删,`stub` / 其它媒介 / 软删行一律不计。
> - **去重按作品**:一个作品对同一厂牌可有多条不同 `kind` 的归属边、可携带多个映射到同一规范 tag 的源 tag,计数只算一次。
> - 实现上是**页级批量 GROUP BY**(每页一条聚合查询),不是逐行 count。
>
> **发售日历三桶语义(2026-07-29 A2-1c 落账)**:三个桶按**同一个分类锚**切分——作品的**最早一条「带年份、未软删」release** 的组合序数(`y*10000 + m*100 + d`,月/日未知记 0)。这**正是** works 列表投影为 `release_date` 的那个数,所以「一行落在哪个桶」与「这一行印着什么日期」永不打架。
>
> | 该作品最早 release 的精度 | 组合序数示例 | 落桶 |
> |---|---|---|
> | day(`2024-06-14`) | `20240614` | `calendar?month=2024-06` |
> | month(`2024-06`) | `20240600` | `calendar?month=2024-06`(排在该月**月首**,**不补 1 号**) |
> | year(`2024`) | `20240000` | `calendar/pending?year=2024`——**该年任何月桶都不出** |
> | 有 release 行但无一行带年份 | — | `calendar/tba` |
> | **无 release 行**(unknown) | — | **不进任何桶** |
>
> - **月窗判据 = 序数区间 `[y*10000+m*100, +99]`**:day 精度(1-31)与 month 精度(d=0)同时落入,year 精度(`y*10000`)天然出界。
> - **移植/复刻按最早那次归桶**:2024-05 首发、2024-06 复刻的作品在**五月**桶,和它 `release_date=2024-05-02` 一致。
> - **JST 定界**:galgame 发售日是日本民用日期,故 `month` / `year` 缺省 = **Asia/Tokyo 当前**月/年(固定 +09:00,JST 无夏令时);服务端解析出的窗口**回显**在响应的 `month` / `year` 里——缺省调用方否则无从得知拿到的是哪一格。
> - **人口 = works 列表谓词逐字**(LIVE + galgame 媒介 + 未软删;`nsfw=1` 才出 r18)**+ `olang=` 原语言门**:缺省 = **`ja` + 全部 `zh*` 家族**(VNDB 系西方目录会淹没新作月表);`olang=all` 关闭该门;也可给逗号分隔的显式集合(`olang=ja,en`)。**`olang` 是开放词表**(存的是上游 BCP-47 拼写 `ja` / `zh-Hans` / `zh-Hant` / `en` / `ko` …,**不是**弃用 wiki 面的产品 locale 形态 `ja-jp` / `zh-cn`),故无人使用的值 = **空桶,不是 400**——对照我方封闭词表(`content_rating` / `kind` / `tier`)拼错即 400。works 列表本身**本波不加** `olang=` 参数。
> - **桶级 ETag**:`W/"cal-<桶键>-<人口键>-<count>-<max(updated_at) unix>"`,其中元查询(`count` + `max(updated_at)`)跑在**整个过滤集**上、**先于**任何分页加载,`If-None-Match` 命中即 `304` 短路(省掉整页 item 富化)。人口门(`nsfw` × `olang`)进键——两个不同人口的 count 可能偶然相等,不能共用校验子。`limit` / `cursor` / `include` **不**进键:ETag 只需在**同一 URL** 内唯一,而这三个参数都写在 query string 里。`max(updated_at)` 取自 `catalog_work`——facet 写入统一 touch 宿主作品(与 changes 流同一纪律),所以改 release 日期会推动校验子。
> - `count` 是**整桶**行数(本页至多 `limit` 行),由上面那次必跑的元查询顺带给出。
>
> **封面严格度:列表面 > 详情面(对 sfw 调用方)**——`works` 列表的单图 `cover` 对 sfw 调用方会**丢弃 `sexual≠0` 的封面**(挑不出合规图时 `cover` 为空串),而详情面 `covers[]` / `screenshots[]` **恒发全量**并逐行带 `sexual` / `violence` 旗标交由消费端自行取舍。列表 `include=covers` 的两槽同样吃这条 sfw 规则(见 §3.2.1)。

### 3.2.1 D7 投影约定(2026-07-29 A2-1a 落账)

三张对照表,定义公开面在「多语言」「模糊日期」「机翻」三处的**投影口径**。它们描述的是既有数据的**呈现约定**,不新增任何数据源。

**① 语言标签 → 产品四键**(用于 `works?include=names,intros`)

catalog 内部按 BCP-47 存语言(`ja` / `zh-Hans` / `zh-Hant` / `en`、以及历史遗留的裸 `zh`);产品面(kungal / moyu / letmoe)统一渲染四个 locale 键。投影表:

| catalog 语言标签 | 产品键 | 备注 |
|---|---|---|
| `ja`、`ja-*` | `ja-jp` | |
| `zh-Hans`、裸 `zh`、其余 `zh*` 非 Hant | `zh-cn` | 产生裸 `zh` 的来源全部是简体,故并入 |
| `zh-Hant`、`zh-Hant-*`、`zh-TW`、`zh-HK` | `zh-tw` | |
| `en`、`en-*` | `en-us` | |
| **其它一切**(`ko` / `ru` / …) | **丢弃** | |

- **四键之外的语言在该块里丢弃**,不是丢失:详情面 `titles[]` / `intro[]` 恒发**完整**语言集合,富 brief 块只是渲染便利。
- **每键选唯一行**:`names` 取该键下 **kind 最低**的一行(`official`(0) > `alias`(1) > `abbreviation`(2)——与详情面 `titles[]` 的 `ORDER BY kind` 同一序),同 kind 再按行 id 升序定序;**`search_hint`(kind=3)永不公开**(既有硬规则,查询层即排除)。两个语言映射到同一键时(`zh-Hans` 与裸 `zh`),按上述定序取首行,结果稳定可重放。
- **认领作品的四键来自 wiki 桥**(A2-R1,见 §3.2.5):`ja-jp`/`en-us`/`zh-cn`/`zh-tw` 分别由 wiki 正文的四个名称列供给;wiki **别名无语言**,故进不了任何产品键——别名只出现在详情面 `titles[]`(`kind=alias`、`lang=""`)与搜索索引里。
- `intros` 的每语言归并在读面已完成(每语言最优来源胜出 + 机翻让位于源文,见表③),此块只做重新键控:按 lang 升序取首个落入该键的行。

**② release `date` ↔ 旧面 `release_date` + `release_precision`**

catalog 的 release 日期是**部分 ISO**:`YYYY` / `YYYY-MM` / `YYYY-MM-DD`,精度**由字符串长度自明**,不另发精度字段。与弃用的 `/v1/galgame` 面(`release_date` 是**归一化**的完整日期,精度另存 `release_precision`)对照:

| 旧面(`/v1/galgame`) | catalog `date` / `release_date` | 说明 |
|---|---|---|
| `release_date=2021-06-04`, `release_precision=day` | `"2021-06-04"` | 长度 10 |
| `release_date=2021-06-01`, `release_precision=month` | `"2021-06"` | 长度 7;**日不得臆造**,旧面归一化补的 `01` 是占位符,不要回读为 1 号 |
| `release_date=2021-01-01`, `release_precision=year` | `"2021"` | 长度 4;同上,月/日均为占位符 |
| `release_precision=tba` / `unknown` / `release_date=null` | **`null`** | 作品级 `release_date`(最早 release)与 release 级 `date` 同此口径 |

- 作品级 `release_date` = 该作品**最早**有年份的 release 的部分 ISO;无任何带年份的 release 即 `null`。
- 消费端解析建议:按长度分派(4 / 7 / 10),**不要**用 `Date.parse` 后取字段——那会把 `"2021"` 悄悄变成 1 月 1 日,正是本表要避免的失真。
- **日历三桶与本表同一个分类锚**(A2-1c):`calendar` / `calendar/pending` / `calendar/tba` 的桶籍就是由这里的作品级 `release_date` 决定的——长度 10 / 7 落月桶、长度 4 落 pending 桶、`null` 且有 release 行落 tba、`null` 且无 release 行不进任何桶。所以一行的**桶籍与它印出来的 `release_date` 永不打架**。

**③ intro `machine` 旗标语义**

| `machine` | 含义 | 消费端建议 |
|---|---|---|
| 缺席 / `false` | **源文**:来自 `source` 指名的上游站点原文 | 直接展示 |
| `true` | **机器翻译**(LLM,step 75 ja→zh-Hans 起):`source` 仍是**被翻译的那个源**,归因语义是"译自该源" | 展示时应标注「机翻」之类的提示,不与源文等同 |

- **机翻永不冒充源文**:某语言只要存在源文行,机翻行就在读面归并中落败、根本不出现;`machine=true` 只可能出现在"该语言没有任何源文"的语言上。
- 该旗标同时出现在详情面 `intro[]` 与列表 `include=intros` 的每个槽,语义逐字一致。

**④ `include=covers` 两槽判据**(与三表同批落账)

`covers` 出 `{portrait, banner}`,每槽 `{url, width, height, thumbhash, sexual, violence, source}` 或 `null`。

- **朝向来自真实尺寸,不来自 `kind`**:注册表的 `kind` 是样图分类词表(catalog 原生行全为 `main`;wiki 桥面另有 `""` / `dig` / `pkgfront` / `pkgback` / `pkgcontent` / `pkgmed` / `pkgside`),**没有一个词说明图片是横是竖**。判据沿用仓内既有的竖版定义 `height > width × 1.05`(`cmd/pin-portrait-covers` 的 U 轨切点)。
- `portrait` = `portrait_pinned` 行 → 否则首个尺寸判定为竖版的封面 → 否则该调用方可见的首图(按 `sort_order`, `image_hash` 序),**故有可见封面时恒非 null**。
- `banner` = 首个尺寸判定为横版的封面;**无(含 image_service 查询未接线时)即 `null`**,绝不猜。
- 只有一张可用封面时两槽可能指向同一图,这是预期。
- `width` / `height` / `thumbhash` 来自 image_service 的按需批量查询,**未知即三键一并省略**(消费端退回骨架屏);详情面 `covers[]` **与 `screenshots[]`** 每行同样带这三个可选键(A2-1a 加法,A2-1b 补齐 screenshots——两个粒度共用**同一次**批量查询,详情面对 image_service 仍只发一趟)。
- sfw 调用方在**两槽**都永不见 `sexual≠0` 的封面(与列表单图 `cover` 同一规则;`violence` 同样不入门槛)。

### 3.2.2 作品级 `links[]`(2026-07-29 A2-1e 落账)

`GET /v1/catalog/works/{id}` 的 `links[]` 是该作品的**非身份外部网页链接**——商店页 / 官网 / 社交页,形状 `{source, url}`,恒在(无供给为 `[]`)。

- **与 `refs[]` 互不相交,这是硬红线**:`refs[]` 是身份锚(「这个作品在上游叫什么 id」,exact-only),`links[]` 是网页地址。同一条 `catalog_external_ref` 行按 `link_kind` 分流,exact/probable 永不入 `links`、related 永不入 `refs`——与 `labels/{id}` 画的是同一条线。
- **无 user 归属,也没有标题**:这些字节来自 wiki 用户提交的链接表,退役波 W0 以**平台策展身份**收编(`user_id` 从未随迁),因此弃用面的「按作者封禁过滤链接」在这里**自然消失,是设计而非疏漏**;链接的用户自填标题同样**没有**被收编,所以 `links[]` 里**不会有** `label`/`title` 键——凭空造一个就是编造。消费端请用 `source` 键(或 URL 的 host)作为标签。
- **URL 模板只覆盖能确定的来源**:`web`(external_id **本身就是完整 URL**)、`twitter`、`cien`、`steam`、`pixiv`、`official_site`。**`dlsite` / `dmm` 刻意不出现在 `links[]`**:注册表只存裸商品号(`RJ…` / `d_186489`),而它们的商店 URL 分区依来源而异(dlsite maniax/home/pro/soft;dmm digital/dlsoft),任何单一模板都会对一部分行 404——猜一个地址比不给更糟。这两类锚仍然以数据形式可达(详情面的 refs / releases 侧),只是本面不为它们编造地址。

### 3.2.3 tag 安全轴(2026-07-29 A2-1e 落账)

`GET /v1/catalog/works/{id}` 的 `tags[]` 每行恒带两个安全轴键,外加一个新的请求参数:

| 键 / 参数 | 含义 |
|---|---|
| `spoiler` | **该 work-tag 边**的剧透级别:`0` 无 / `1` 轻微 / `2` 严重 |
| `sexual` | **该 tag 本身**属于性内容类别(bool) |
| `spoilers=0\|1\|2` | 请求参数,剧透**上限**;缺省 `0` |

- **缺省安全**:`spoilers` 缺省 0,响应里**一条剧透 tag 都没有**——完全忽略这个轴的消费端天然安全,本波前的字节也因此一字不变。要做「点击展开剧透标签」的交互就显式传 `spoilers=1|2`。
- **覆盖面必须照实说**:这条轴的上游只有 **VNDB 系词表**(剧透值与性内容类别都出自那里,也正是剧透 tag 的实际所在)。**Bangumi / DLsite 的 folksonomy 上游根本没有剧透与类别概念**,所以那些行渲染成 `0` / `false` —— 这表示**该来源没有这条轴**,**不是**「已确认安全」的断言。消费端若要做严格门控,应结合 tag 的来源 `source` 判断。
- 词表外的 `spoilers` 值退化为缺省 0(与 `characters/{id}` 的 `spoilers` 姿态一致),不是 400。
- **明确不做的事**:安全门**没有**降级成作品级 `content_rating`。用作品分级当 tag 剧透门是静默暴露——一个全年龄作品照样可以有严重剧透 tag。

### 3.2.4 简介检索与 tag 级 `sexual`(2026-07-29 A2-1f 落账)

**① `search_intro=`(works 搜索)**

`GET /v1/catalog/works/search` 的 `q=` 缺省**只匹配标题族**(标题 / 别名 / latin / search hint)。传 `search_intro=1` 后**额外**匹配作品**简介正文**。

- **缺省逐字节不变**:索引里现在**确实存了**简介字段,但本面对每个请求显式把可搜索属性钉在标题族上。所以 A2-1f 之前写好的调用方,结果集一行不变——放宽是**请求级 opt-in**,不是索引级的既成事实。
- **简介永远排在标题之后**:简介字段在索引可搜索列表里位列标题族之后,而 Meilisearch 的 `attribute` 排序规则按该顺序给权重——**标题命中永远压过简介命中**,不会出现「正文里提了一嘴的作品挤掉同名作品」。
- **每语言截断 2000 字**:简介是 1-10 KB 的 markdown,而「用户记得的那句话」几乎总在开头设定段。2000 字(CJK 下一字即一词)覆盖前若干段,同时把 ~22.6 万作品的索引增量压在可接受范围;更深处的短语检索不到,是这条取舍**明写的代价**。
- **语言分桶**:简介按语言分桶存(ja / zh / 其它),日文走 jpn、中文走 cmn 分词;简繁两种译文并入同一 zh 桶(**为召回而合并**,你记得哪个版本都能搜到)。
- **正文永不下发**:与索引里其它字段一样,命中后 item 仍是按 id 回库水化的 works 列表行,Meilisearch 文档字段不出 wire。
- 未识别的取值(拼错)= `false`,不是 400:这个开关只会**放宽**结果,退化到窄的一侧才是安全方向。
- ⚠️ **需要 reindex**:简介是索引内容而非查询逻辑,所以本参数在**跑过一次 `reindex-catalog` 之前**只会返回空(索引里还没有简介字节)。

**② tag 级 `sexual`**

`GET /v1/catalog/tags` 的行与 `GET /v1/catalog/tags/{id}` 恒带 `sexual`(bool):该 **tag 本身**属于性内容类别。

- **派生**:规范 tag 经 **A2-0 落的身份锚**(`entity_type=tag`、`source=galgame_wiki`、`link_kind=exact`、`external_id` = wiki tag id)对上 wiki 词表行,取其 VNDB 血统的类别(`cont→content` / `ero→sexual` / `tech→technical`),**只有 `sexual` 有安全含义,故只投影它**。走 id 锚而非名字匹配:两条路今天解出的集合一模一样(901 content / 357 sexual / 267 technical),但名字键会在任一侧改名时**静默失联**。
- **⚠️ 覆盖面 = 与 §3.2.3 同一条款**:只有**映射进该词表**的 tag 才有这条轴。纯 Bangumi / DLsite folksonomy 长出来的规范 tag 上游**没有类别概念**,渲染 `false` —— 这表示**「该 tag 没有这条轴」,不是「已确认安全」**。要做严格门控,请结合 tag 的来源判断,**不要**把 `sexual=false` 当作安全断言。
- 与作品详情 `tags[]` 上那个 **per-edge** 的 `spoiler` / `sexual`(§3.2.3)是两个粒度:那里描述的是「这个作品的这条 tag 边」,这里描述的是「这个 tag 本身」。**§3.2.3 的既有语义本波一字未动。**

### 3.2.5 认领作品的标题供给 = wiki 桥(2026-07-29 A2-R1 落账)

`titles[]`(详情)、`names`(列表 `include=names`)与作品搜索索引的标题族,对**认领作品**(`claimed_by.site=galgame_wiki`)读的是 **wiki 正文**,不是 catalog 自己的标题表——与 `intro[]` / `covers[]` / `screenshots[]` 同一条 bridge-not-copy 规则(读时桥接,不落副本)。

| 作品形态 | 标题来源 |
|---|---|
| **认领**(`site=galgame_wiki`) | wiki 正文的**四个名称列** → `official` 行(`ja` / `en` / `zh-Hans` / `zh-Hant`);wiki **别名表**每行 → `alias` 行,**`lang=""`** |
| **无正文**(未认领) | catalog 自己的标题行,逐字不变 |

- **严格 XOR**:认领作品**只**读桥,绝不回落到 catalog 标题行——即使它还留着历史行(与 `intro[]` 同律)。
- **别名不编造语言**:wiki 不记录别名的语言,故 `lang` 为空串。空 `lang` 落不进 §3.2.1 的四键投影,所以别名**只**出现在 `titles[]` 与搜索索引,不进 `names` 块——这正是想要的:别名可搜可列,但不占某个 locale 的名字槽位。
- **同名只出一次**:别名字符串与某个名称列**完全相同**时只渲染一次(取 `official` 那行)。
- **`latin`**:wiki 正文没有罗马音列,故桥接行不带 `latin`;catalog 原生行的 `latin` 一如既往。
- ⚠️ **搜索新鲜度**:桥接标题进入搜索索引由 `reindex-catalog` 承载(每日 cron + 上线即时跑一次),所以 wiki 侧改名到「按新名搜得到」之间存在**一次 reindex 的滞后**;详情面/列表面是**读时**桥接,无滞后。

### 3.2.6 作品记录上的 chip `work_count`(2026-07-29 A2-R1 落账)

A2-1b 给 **taxonomy 浏览道与其详情面**发了 nsfw 感知的 `work_count`,但真正被渲染出来的**作品记录上的 chip** 一直没有——于是下游在每个厂牌/tag/引擎 chip 旁边渲染出恒定的「+ 0」。本波补齐:

| 位置 | 键 | 恒出? | 口径 |
|---|---|---|---|
| `works/{id}` 的 `labels[]` | `work_count` | **恒出** | ≡ `works?label_id={id}` 同 nsfw 下的总数 ≡ `labels/{id}.work_count` |
| `works?include=labels` 的 `labels[]` | `work_count` | **恒出** | **与详情面同一次聚合、同一个数** |
| `works/{id}` 的 `engines[]` | `work_count` | **恒出** | ≡ `works?engine_id={id}` ≡ `engines/{id}.work_count` |
| `works/{id}` 的 `tags[]` | `work_count` | **仅映射行** | ≡ `works?tag_id={canonical_id}` ≡ `tags/{id}.work_count` |

- **nsfw 感知**:与 §3.2 的 taxonomy 不变量同一条——数字等于**这个调用方**点进去真正能翻到的行数,不是边表行数。
- **未映射 tag 无此键**:没有 `canonical_id` 就没有落地页,也就没有数可报(与该行 `canonical_id`/`tier`/`kind` 三键同一省略规则)。已映射的行**一定**带这个键,**包括值为 0**——所以 `work_count` 缺席只意味「这条 tag 没进规范词表」,永远不意味「0 部作品」。
- **`labels[]`/`engines[]` 恒出**:这两处每一行都是可寻址身份,`0` 是一个真实答案;缺键与「消费端解析失败」不可区分,而那正是弃用面那个永久「+ 0」的来源。
- ⚠️ **认领作品的桥接 tag 可能报出不含它自己的数**:`works?tag_id=` 经 `catalog_tag_source_map ⋈ catalog_work_tag` 找作品,而**认领作品的 wiki tag 是读时桥接的**(见 §3.2.5 同一条 bridge-not-copy 规则),不在那张边表里。这不是计数错误——数字承诺的**只有**「点进去会拿到多少」,它照实回答。
- **成本**:每个 facet 每次请求(或每页)**一条批量 GROUP BY**,不是每 chip 一次。

### 3.2.7 works 搜索的 `claim_state=` 闸(2026-07-31 A2-R1 区 C 落账)

> 事故驱动:两个产品站的搜索把**未发布(draft)**与**未认领**的注册行渲染进了结果页,因为 `works/search` 此前**没有任何 claim 态过滤供给**——`claimed` 只答「有没有产品站认领」,答不了「能不能给人看」。

`GET /v1/catalog/works/search` 新增 `claim_state=`:逗号分隔、**封闭词表 `none|live|draft|hidden`**(即 `claimed_by.state` 的四个取值,`none` = 未认领注册行),命中作品须**属于所列任一态**(IN 语义)。

- **非法 token = 响亮 400**(与 `sort`/`facets`/`content_rating` 同一姿态)。静默忽略会把「请帮我排除草稿」变成「200 + 满屏草稿」,正是本参数要终结的事故。
- **无缺省 = 不闸**:不传这个参数,结果集与本波前**逐字节一致**。
- **同一道门**:它编译进与 `total`/`facets`/`items` 共享的那一条 Meili 过滤表达式,所以翻完 `total` 页恰好收满 `total` 行——不会重演弃用面「总数不过滤、items 过滤」的陷阱。
- **投影与读面同一份定义**:索引里的 `claim_state` 与记录上的 `claimed_by.state` 走**同一个函数**(`model.ClaimStateKey`):`site` 为空或无 `product_work_id` → `none`;已认领而状态列为 NULL → `live`(零回归语义);0/1/2 → live/draft/hidden;词表外 → 保守的 `hidden`。所以 `claim_state=live` 选出的,恰好是详情页写着 `state: "live"` 的那些行。
- ⚠️ **新鲜度随索引**:claim 态变化由 `reindex-catalog`(每日 cron)带进索引,与其余索引 facet 同律——不是读时计算。
- **works 列表面(`GET /v1/catalog/works`)本波不加**该参数。
- **消费建议**:产品站渲染自家目录的搜索 lane 一律传 `claim_state=live`,并**删掉客户端事后过滤**——客户端过滤修不了 `total`,翻页照样丢行。

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
- 产出 `api.nextmoe.dev/v1/catalog/openapi.json` 与 `…/v1/galgame/openapi.json` → 门户 Scalar 渲染 → 第三方据此生成 SDK(TS 优先,`@kungal/api-*` 发包纪律届时启用)。**✅ 两 spec URL 已上线(2026-07-28)**:`cmd/catalog` 无鉴权在线服务——boot 时经 `cmd/gen-openapi` 同一 spec-only 管线构建一次(与仓内冻结 Tier-A YAML 恒等,CI 冻结门背书),JSON 渲染,`Cache-Control: public, max-age=3600`;精确 GET 路由先于 `/v1` 键控组注册,故这两条免 key,其余 `/v1/*` 照旧要 key。门户侧为自建文档体验(06c 已弃 Scalar);SDK 生成策略见 [08](./08-downstream-faces-and-sdk.md)。
- 公开 spec 纳入 `docs:verify` + oasdiff 破坏性门,升级为 **Tier-A 对外契约**(在 kungal-docs 登记)。
