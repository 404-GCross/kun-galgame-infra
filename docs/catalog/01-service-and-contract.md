# 01 — 服务定位与契约

> catalog 是**跨媒介身份/图谱注册层**:把多来源的作品/发行/人物/署名/厂牌收敛成一套带来源锚与分级信任的规范身份。产品站通过 S2S 三端点接入;人审经 admin 三桶治理;写路径按 per-client site 绑定授权。本篇是对外契约,数据结构以生成的 [openapi.yaml](./openapi.yaml) / [admin-openapi.yaml](./admin-openapi.yaml) 为准。

## 1. 服务定位:registry 层 vs body 层

catalog **只管身份、关系、来源锚**:

- **实体**:`work`(作品)/`release`(发行/SKU)/`credit_name`(署名名义,孤儿合法)/`person`(人)/`character`(角色)/`org`/`label`(厂牌/社团)。实体类型常量 `0=person 1=credit_name 2=org 3=label 4=character 5=work 6=release 7=tag 8=engine`。**7/8 是纯数据位**(A2-0 wiki 注册表抢救波):它们只作为 `catalog_external_ref.entity_type` 出现,承载 wiki 的 tid/eid 地址簿;**无公开读面**——公开 lookup / resolve / redirects 词表不含之,维持既有四/七族。
- **来源锚** `catalog_external_ref`:把实体锚到外部来源的 id,按 `link_kind` 分级 `exact(0)` / `probable(1)` / `related(2)`;exact 有唯一约束(一个来源的一个外部 id 只精确锚一个同类实体)。
- **关系**:credit(**署名边**:work ↔ credit_name/label,"谁演了什么角色/担任什么职务")、**work_label 归属边**(work ↔ label,"哪个社团/发行方对作品负责";`kind`:0=circle/1=publisher/2=developer/3=brand)、**work_character 花名册边**(work ↔ character,"哪个角色出现在作品里";`kind`:0=unknown/1=main/2=secondary/3=appears,**0=unknown 是有意义值**——EG 无主配分型、Bangumi 低频尾型归此;`spoiler`:0=none/1=minor/2=major,VNDB 源、Bangumi/EG 恒 0)、redirect(合并后的旧→新 id)、alias(名义别名)。**署名 ≠ 归属 ≠ 出演**:credit 是个人署名,work_label 是组织责任,work_character 是出演事实(有配音的角色会同时在 credit 与 work_character 两表,语义不同);读面(§2.4/§2.10)负责合并展示,三者并存不互斥。

catalog **不存**产品展示体:简介、封面/截图字节、评分、点赞、收藏、NSFW 过滤——这些是**产品站(body 层)**各自持有的。产品站保留自己的富行,只把「这是**哪一个**作品/人物」的身份问题委托给 catalog。这条分界是硬约束:catalog 加展示字段 = 越界。

来源注册表(节选):`source` `2=vndb 3=bangumi 4=dlsite 5=erogamespace 1=user`;`medium` `1=galgame 5=asmr`;`content_rating` `0=all_ages 1=sensitive 2=r18`。完整注册表由 `cmd/migrate-catalog` 的 seed 落库。

⚠️ **`content_rating` 是年龄轴,不是展示轴**(A2-R5,doc 106 §38 事故)。这一列答的是「**游戏本体**是什么分级」;「我要**渲染的素材**(封面/截图/简介)能不能摆上公开页」是**另一个问题**,由**编辑展示轴**回答——公开面 `claimed_by.content_limit`(词表 `sfw|nsfw`),认领作品读 **`catalog_work.display_nsfw`**(人工编辑判定;**W1-pre 本体化**前是读时桥接 wiki 正文的 `galgame.content_limit`,refs/proj/140 §5b 把该判定物化成 registry 自己的列,wiki 表族退役后由 catalog 编辑面持有),未认领行按年龄轴回落(该列对未认领行恒 false 且从不被读)。生产实测两轴在 5,568 部作品上不一致(r18 游戏 × 编辑判定 sfw),**互不是对方的放宽或收紧**;把年龄轴当展示门正是那次 SEO 塌缩的根因。语义源 = `model.DisplayLimitKey`,契约与闸参见 [developer-platform/02 §3.2.8](../developer-platform/02-public-api.md#328-编辑展示轴-content_limit闸a2-r5)。

## 2. S2S 端点(Basic client 认证,前缀 `/api/v1/catalog`)

写/运维面:resolve(2.1)· redirects feed(2.2)· claim(2.3,带 site 绑定)。读面(D-01,2.4-2.6):by-anchor · credits · entity search。内部浏览器(D-02,2.7):stats · works/{id} · labels/{id}/works。产品建游面(2.8):works/search。实体读面(2.9-2.11):names/{id}/works · characters/{id}/works · characters/{id}。

### 2.1 `POST /catalog/resolve` — 批量 id 规范化(只读)

一次解析同一 `entity_type` 下最多 1000 个 id 到其规范 id(跟随 redirect)。

- 请求:`{ "entity_type": 5, "ids": [12, 34, …] }`
- 响应:`{ "mappings": {"12": 12, "34": 99}, "redirected": [34] }`——`mappings` 是「旧 id(字符串键)→ 规范 id」,未被 redirect 的 id 映射到自身;`redirected` 是其中发生过跳转的子集。
- 用途:产品站在展示/入库前把手里的 catalog id 归一到当前规范 id。**不受 site 绑定限制。**

### 2.2 `GET /catalog/redirects` — redirect keyset feed(只读)

按 `merged_at` 升序的 keyset 分页,吐出所有「旧 id → 新 id」的合并边,供产品站的清理 cron 增量消费(把本地存的旧 catalog id 批量改写到新 id)。

- 响应:`{ "items": [{entity_type, old_id, current_id, merged_at}], "next_cursor": "…" }`。
- 客户端持久化 `next_cursor`,下次轮询回传;`next_cursor` 为空表示当前页未满(已追平)。**不受 site 绑定限制。**

### 2.3 `POST /catalog/works/claim` — 作品认领/注册(写)

把一个产品侧作品行认领到一个 catalog work 身份。

- 请求:`{ medium_id, site, product_work_id, display_name, olang?, content_rating?, anchors?[{source_id, external_id, level?}] }`。`level ∈ {work, release}`,缺省 `work`。
- **锚的层级(R3/R5)**:SKU 性质的外部 id(**DLsite workno**、VNDB release)锚 **release**,作品性质的(Bangumi subject、VNDB vn)锚 **work**。同人站以 dlsite_id 认领时须传 `level:"release"`。
- 语义:锚查找**跨 work/release 两级**——release 级锚经 `catalog_release.work_id` 回溯其 owning work。三分支:
  1. **锚命中未认领 work**(site=NULL)→ 认领成功(填 site/product_work_id、stub→live),该 work 既有身份资产(EG ref / credits / labels / releases)**全数继承**;
  2. **锚命中已被他站认领的 work** → **409 冲突**,响应带**结构化归属**(见下),调用方只记 link 不抢占;
  3. **锚无命中** → 铸新 work;其中 `level:"release"` 的锚落在**新建的 release** 上(绝不落 work),从而与后续按 release 锚去重的导入**天然同一身份**,不产生分裂。`work` 级锚落 work。
- 响应 `{ work_id, created }`,`created=true` 表示新铸。
- **幂等**:同一 (site / product_work_id) 重复 claim 收敛到同一 `work_id`。
- **冲突 409**:house 信封 `{code, message, data}`,`data = { work_id, owning_site, owning_product_work_id? }`——`work_id` 是锚已解析到的 catalog work,`owning_site`/`owning_product_work_id` 是占坑方。绝不抢占他站身份。
- ⚠️ **site 绑定要求(写端点独有)**:见 §4。

### 2.4 `GET /catalog/works/by-anchor?source=&external_id=` — 锚反查读穿(只读)

产品站拿一个外源 id 读穿到 catalog 作品。`source` 对 `catalog_source` 注册表校验(即白名单:dlsite/vndb/bangumi/erogamespace/…);命中 **work 级或 release 级锚**均可(release 锚回溯其 work;`exact` 优先、work 级优先破平);未命中 **404**。

- 响应 `data`:`work`(id/medium/display_name/olang/content_rating/status/**site 认领态**)+ `titles`(official/alias/abbreviation/search_hint)+ `releases`(每个含 kind/模糊日期/**主平台码 `platform` + 全平台数组 `platforms`(step 96,空则省略)**/各自 `anchors`)+ **`labels`(经 work_label 归属边,含 label 自身 kind + 归属 kind)**+ **`refs`**+ **`characters`(花名册投影)**+ **`intro`(多语言简介,媒体聚合读面)**+ **`covers`(封面集,含竖版,媒体聚合读面)**+ **`screenshots`(截图集,媒体聚合读面)**+ **`ratings`(评分集,源原生刻度,媒体聚合读面)**+ **`tags`(内容标签集,原样 folksonomy,媒体聚合读面)**+ **`popularity`(热度计数集,源原样计数,媒体聚合读面)**+ **`playtimes`(时长估计集,分钟归一,媒体聚合读面)**+ **`series`(系列归属,step 94)**+ **`platforms`(作品级平台集,step 96)**。
- **`refs` 块(消费面)**:把本作品**全部 exact 锚**(work 级 + release 级)拍平成一张表,每条 `{ source, external_id, level(work|release), release_id? }`(`release_id` 仅 release 级)。用途:渲染 DLsite/EG 外链、展示跨源身份链。**只出 exact 档**——`probable` 是审核泳道内部态、`related` 是非身份链接,均不入 `refs`。**`relations` 块(wave 104)**:work↔work 关系边(sequel_of/fandisc_of/remake_of/collects/shares_character…)按本作品视角单行双向渲染(对向短语按 is_symmetric 解析),对端身份字段随行(id/display_name/medium/content_rating/site);S2S 详情**恒带**(r18 端原样,内部面不设门),公开面 include=relations 且 **r18 端由 `nsfw` 参数门控**(wave 104 调用方自控)。
- **`characters` 块(花名册,step 46;spoiler step 47)**:`work_character` 出演边 **∪** VA credit(`catalog_credit.character_id`)的**并集**——每条 `{ character_id, display_name, latin, gender, kind, spoiler, image_hash, va[{credit_name_id, name}] }`。合并语义:有出演边者 `kind`/`spoiler` 从边取;**credit-only 角色**(仅有 VA credit、无出演边,如 VNDB 路 credits)也出,`kind=0`/`spoiler=0`;`va` 列出为该角色配音的**全部**名义(同名义多 credit 去重)。**`spoiler`**(0=none/1=minor/2=major,VNDB `chars_vns.spoil`;Bangumi/EG 边恒 0)= 该角色在此作出场的剧透档;排序:主角优先(kind 有序 main→secondary→appears→unknown)后按 display_name;`va` 内按 name。**链接可见性铁则**:`va` 只暴露 credit_name 名义本身(名义永远可见),**不做任何 person 展开**(person 聚合才受政策辖制)。空花名册序列化 `[]`;角色 `image_hash` 在 step 48 VNDB 立绘波前恒为空(缺省)。
- **`intro` 块(多语言简介,媒体聚合读面 step 52;治理契约 refs/proj/51,**读面于 W1-pre 本体化** refs/proj/140)**:统一形状 `[{ lang, intro, source_id, machine? }]`,一语言一元素。**一条原生泳道,认领与无体作品同规**:恒读 `catalog_work_intro`,一语言多源时**先按 `provenance` 升序(源行 0 优先于机翻行 1)再按 `source_id` 升序**取一(user > vndb > …),元素按 lang 排序。**桥接已退役**:认领作品的简介曾在读时从 galgame body 的四语言列 pivot 出来(bridge-not-copy),W1-pre 的镜面步 `wikirescue` **步 q** 把其中 **ja/en 两语言逐字物化**进 `catalog_work_intro`(`source_id`=galgame_wiki(12)=复刻桥归因、`provenance=0`;空白判定按 trim、存的是**原始列值**),读时桥随即删除——线上字节不动(全认领人口 SQL 对拍 0 差异)。**`zh_cn`/`zh_tw` 不物化**(2026-07-29 用户拍板①:wiki 的 zh 正文本身是无上游的译文,终档 dump 封存,后续 intromt 波以 ja 原文直翻回填)——认领作品的中文简介因此**在翻转时消失,这是有意变更**(实测 21,303 行 / 19,700 部作品),非缺陷。镜面步每日跟随 wiki 侧编辑直至 W1 表族退役,其后 catalog 编辑面持有该列。`source_id` 引用 `catalog_source` 注册表(归因,§8.C)。批量:一批 `catalog_work_intro`,非逐 work。空序列化 `[]`。
  - **`machine` 标记(机翻 step 75,refs/proj/75)**:`catalog_work_intro` 的机翻行(`provenance=1`)在读面元素上带 `machine: true`,源/桥接行**省略**该字段——机翻文本**永不冒充源数据**,消费端可渲染「机翻」徽标。机翻行**填缺语言**落地(ja→zh-Hans):仅当该 work **无任何 `zh-Hans/zh-Hant` 源行**(`provenance=0`)时写;它沿用源 ja 行的 `source_id`(归因=译自该源),另记 `src_hash=sha256(源 ja 文本)`(源文变则重译)与 `mt_model`(问责)。新源/人工 zh 落地后,上面的 provenance 升序令读面**自动优先源行**、机翻行退居遮蔽(shadow-never-delete)。**两条人口泳道**(`cmd/intro-mt --population`):`bodyless`(catalog 原生作品,step 75 试点,top-5k 已饱和)与 `claimed`(wiki 面作品——拍板①弃 wiki zh 后,以步 q 物化的 ja 行(source_id=galgame_wiki)直翻回填,机翻行同键归因 galgame_wiki);零 ja 行的作品(正文只在 wiki zh 列者)不在候选内,待假名归位波补 ja 后由同一 fill-missing 幂等自然收编。源料:step 52 VNDB `vn.description`(en)、step 55 DLsite、step 57 Bangumi summary(原样存,不清洗)、步 q wiki ja 物化行。
- **`covers` 块(封面集,含竖版,媒体聚合读面 step 53;治理契约 refs/proj/51)**:统一形状 `[{ image_hash, kind, portrait_pinned, sort_order, sexual, violence, source_id }]`,一封面一元素,claimed/bodyless 归并后消费者无感来源;按 `(sort_order, image_hash)` 排序。**竖版**由 `portrait_pinned=true` 挑出——kungal/moyu 重构读此值即得竖版封面。**桥接归并**:**claimed 作品**(`site='galgame_wiki'`)封面**读时桥接**自 `galgame_cover`(kind/portrait_pinned/sexual/violence 原样),`source_id` 由 `galgame_cover.source` 文本映射到 `catalog_source`:`''`→**galgame_wiki**(12,wiki 用户上传=首方 body)、`vndb`→vndb(2)、`bangumi`→bangumi(3)、`upscale`→**upscale**(13,本波新增的首方 AI 放大衍生源);**绝不复制**进 catalog 原生行,桥接读**不重传字节**(galgame 封面永住 galgame_wiki image scope)。**bodyless 作品**(`site=''`)读 `catalog_work_cover` 原生行(字节住 catalog image scope)。**严格 XOR**:claimed 只读桥接;galgame 封面空 = `covers` 空 `[]`,**不回落**原生行(即便存在被遮蔽的原生行也不读——shadow-never-delete §8.B)。批量:claimed 一批 `galgame_cover` + bodyless 一批 `catalog_work_cover`,非逐 work。空序列化 `[]`。**字节纪律 §4**:catalog scope 图(bodyless 封面+立绘)由 `catalog-image-refping` 保活,其全集含 `catalog_work_cover` **全部行(含被遮蔽行)**,漏计遮蔽行 = GC 吃活图(66k 同类);galgame scope 图(claimed 桥接封面)由 `galgame-image-refping` 独立保活。**bodyless 封面数据**:EG(bodyless 主锚 5,844)无任何封面字节(纯统计站,零图列),DLsite(6,102 release 锚)`product_json` 虽带封面 URL 但需从外部 CDN 抓 ~6k 图另立字节波——本波只交付 schema+读面+refping 管线,bodyless 封面数据留待「确认更好 bodyless 封面源」后续波(诚实,不硬凑)。 **桥接余额(W1-pre 收官后)**:封面与截图是**最后两条读时桥**——标题/简介/tag+安全轴/评分/热度/展示轴六面已于 refs/proj/140 全部本体化,这两条按 W2 的既定交接机制留给 W1:原生行 W2 已铺好,桥退即翻转。
- **`screenshots` 块(截图集,媒体聚合读面 step 54 + 125;治理契约 refs/proj/51 + refs/proj/125)**:统一形状 `[{ image_hash, caption, sort_order, sexual, violence, source_id }]`,一截图一元素,桥接 ∪ 原生归并后消费者无感来源;每泳道内按 `(sort_order, image_hash)` 排序。与 `covers` 同构,差异:截图带 `caption`、无 `kind`/`portrait_pinned`。**双泳道归并((facet, source) XOR,125 修正,取代旧的整 facet XOR;与 tags/popularity 同规)**:对 screenshot facet,**每 (work, source) 只有一条泳道**——**wiki 桥接泳道**(`galgame_wiki`=用户上传、`vndb`=sync)恒**读时桥接**、绝不物化;**catalog 原生泳道**(`dlsite`)恒读 `catalog_work_screenshot` 原生行;两泳道各带 `source_id` 归因,claimed 读面 = 桥接 ∪ 原生。**wiki 桥接泳道(仅 claimed)**:**claimed 作品**(`site='galgame_wiki'`)截图读时桥接自 `galgame_screenshot`(caption/sexual/violence/sort_order 原样),`source_id` 由 `galgame_screenshot.source` 文本映射到 `catalog_source`:`''`→**galgame_wiki**(12,wiki 用户上传=首方 body)、`vndb`→vndb(2)(截图源域仅此两值;未知值兜底 galgame_wiki);**绝不复制**进 catalog 原生行,桥接读**不重传字节**(galgame 截图永住 galgame_wiki image scope)。**catalog 原生泳道(所有作品,claimed 与 bodyless 一视同仁)**:读 `catalog_work_screenshot` 原生行(UNIQUE (work_id, image_hash);字节住 catalog image scope;55 回填 bodyless + **125 回填 claimed**:DLsite exact **release** 锚 × 本地镜像 `image_samples[]`)。**125 放开 claimed**——dlsite 是 catalog 原生源,claimed 的 DLsite 商店样图不再因认领态隐没。**写侧靶向(125 拍死)**:claimed 原生截图行**只允许 `source=dlsite`**,且只写**桥接空 + 原生空**的 claimed 作品(既有 wiki 截图的作品不补商店样图——样图是「什么都没有」的兜底,不是真截图的补充);intro/cover facet 对 claimed **照旧整 facet XOR 拒写**(读时桥接)。**读面序**:桥接行在前(保持既有内部序),原生行按 `sort_order` 续后——两泳道分别有序、不混排(不同源的 sort_order 不可比,混排等于伪造顺序)。旧「claimed 只读桥接、不回落原生」**整条作废**:claimed 的原生行是读面一等公民。**两泳道按 `(work_id, image_hash)` 去重,桥接行领先并获胜**(128 裁定,取代 125 的「不去重」):**绝不按 source 过滤**——**wiki 退役抢救**(`internal/jobs/wikirescue`)把 `galgame_screenshot` 物化进 `catalog_work_screenshot`(`source_id=galgame_wiki`)以熬过 galgame 表族折叠,把 wiki 源从原生泳道滤掉等于抹掉抢救成果;去重消掉的是**同一张图两现**——桥接与抢救行并存的窗口内同一 image_hash 会两泳道各有一行,而那是**一张截图**,桥接行(现役 body)获胜,桥接退役后原生行即全部真相、原样浮出。另两个写手不产生重复:125 回填器只写**桥接空**的 claimed 作品;bodyless 作品带着 step-54 原生截图**被后来的认领就地收编**(§8.B shadow-never-delete)时,那些是**不同的图(不同 hash)**,与 wiki 体截图同列正是 125 所求。批量:claimed 一批 `galgame_screenshot` + 全部作品一批 `catalog_work_screenshot`,非逐 work。空序列化 `[]`。**字节纪律 §4**:catalog scope 图(**全部** `catalog_work_screenshot` 行 = bodyless + claimed 原生截图,加封面+立绘)由 `catalog-image-refping` 保活,其全集**不设认领态过滤**,含**被遮蔽行**,漏计 = GC 吃活图(66k 同类);galgame scope 图(claimed **桥接**截图)由 `galgame-image-refping` 独立保活——两把 refping 按**字节所在 scope** 分工,不按认领态分工。**W2 图字节退役后**(数据层退役轨,`internal/jobs/wikirescue` 步骤 m/n/o):wiki 的封面/截图行整表投影进 catalog 原生表,且这些哈希在 `image_site_usage` 上**增记一条 `site='catalog'` 归属行**(只增不改、绝不动 `galgame_wiki` 行),于是 `catalog-image-refping` 自然接管保活;字节本身一张图全站一行、归属才分站,故「加一个 owner」不搬字节、不重传、不碰 galgame_wiki image key。 **桥接余额(W1-pre 收官后)**:封面与截图是**最后两条读时桥**——标题/简介/tag+安全轴/评分/热度/展示轴六面已于 refs/proj/140 全部本体化,这两条按 W2 的既定交接机制留给 W1:原生行 W2 已铺好,桥退即翻转。
- **`ratings` 块(评分集,媒体聚合读面 step 58a;治理契约 refs/proj/58 Facet A,**读面于 W1-pre 本体化** refs/proj/140)**:统一形状 `[{ source_id, score, vote_count, rank? }]`,**一源至多一元素**,按 `source_id` 升序(vndb=2 → bangumi=3 → dlsite=4 → erogamespace=5)。**score 恒为源原生刻度,绝不归一**(58 拍板):`vndb` = 1-10 **均值**、`bangumi` = 0-10 **均值**、`dlsite` = 0-5 **星均值**(两位小数)、`erogamespace` = 0-100 **中央值**——语义不同,消费端按 `source_id` 分源渲染。`rank` = 源内排名,源无此概念(VNDB meta/DLsite/EG)或该作未入榜(Bangumi rank 0)时缺省。**一条原生泳道,认领与无体作品同规**:恒读 `catalog_work_rating`(UNIQUE (work_id, source_id))。**四张 meta 表的读时桥已退役**:认领作品的评分曾在读时从 `galgame_{vndb,bangumi,dlsite,eg}_meta` 桥出,W1-pre 把四条泳道逐字物化——**vndb 归镜面步 q/s 家族的步 s**(`score = rating/10`:meta 存的是 kana 线上刻度 10-100,÷10 得 VNDB 自身展示的 1-10 刻度,**同源解码非跨源归一**;以 float64 计算后按最短往返存,读回即桥的同一个 double),**bangumi/dlsite/eg 归一次性收编步 t**,此后由 `workratings` importer 持久保鲜(本波拆掉了它的 claim 闸,认领与无体作品在其眼中平权);**步 t 不进每日链、W1 终扫也不跑**(importer 的值更新鲜,回写 meta 旧值=倒退)。**「无分」恒为缺席、绝不假 0**——物化时逐字复刻桥的过滤:vndb `rating IS NULL`、bangumi `score<=0`(未评分)、dlsite `rate_average_star IS NULL` 或 `rate_count<=0`(不足 ~5 票不公开,星数与票数恒同现同缺)、EG `median IS NULL`(EG 的 0 是真实低分,NULL 才是无分)——一条都不出行。全认领人口 SQL 对拍 0 差异。批量:一批 `catalog_work_rating`,非逐 work。空序列化 `[]`。纯 DB 零字节——无 refping 事。
- **`tags` 块(内容标签集,媒体聚合读面 step 58b + T2;治理契约 refs/proj/58 Facet B + refs/proj/70 §3/§8,**读面于 W1-pre 本体化** refs/proj/140)**:统一形状 `[{ name, count?, source_id, spoiler, sexual, canonical_id?, tier?, kind? }]`,一 tag 名一源一元素,按 **`(count DESC, name ASC, source_id ASC)`** 排序(高票在前;`name` 为**字节序**——该序原由 Go 排序产生,下沉进 SQL 时显式 `COLLATE "C"` 才不被库 collation 悄悄改掉;`source_id` 破 (count, name) 平)。**name 恒为源原文,原生层不做词表映射/规范化**(58 拍板;仅 trim 首尾空白)——规范层(step 74)在其上加**加法覆盖**(见下),原生 name/count/source_id 永不改写。`count` = 该 tag 在源上的票数,源无票数概念时**缺省**(整个 wiki tag 层无票数;bangumi moderated meta_tags 亦 count=0)。**内容标签与 `labels` 严格分离**:`labels` 是归属词汇(组织责任,brand/publisher/circle/group),**tags 绝不入 `catalog_label`**(词汇红线)。**一条原生泳道,认领与无体作品同规**:恒读 `catalog_work_tag`(UNIQUE (work_id, name, source_id))。**wiki 桥接泳道已退役**:认领作品的 tag 曾在读时从 `galgame_tag_relation ⋈ galgame_tag` 桥出,W1-pre 的镜面步 **步 r** 把 92 万条边逐字物化——`name`=`galgame_tag.name`(**wiki 策展的展示名**,VNDB sync 经 tagMap 英→中本地化,未映射者保留英文原名,用户自建者原样:这批 zh 策展名只存在于此,是这条泳道值得物化而非从 VNDB 重推的原因)、`source_id` 按边自身 `source` 映射(`''`→**galgame_wiki**(12,用户自建)、`vndb`→vndb(2,sync;未知值兜底 galgame_wiki)、`count=0`;镜面步每日跟随 wiki 编辑直至 W1。bgm/dlsite 原生行是 **step 88 / T1 两波 importer 的财产**,镜面步的属权范围把它们整体排除——**一行一个持久写手**是本波红线。**安全轴(A2-1e/R8,W1-pre 落成真列)**:`catalog_work_tag.spoiler`(**逐边**:0=无 / 1=轻 / 2=重,物化时按 `LEAST(GREATEST(spoiler_level,0),2)` clamp——wiki 列是无 CHECK 的 bigint,公开契约只承诺三值词表)与 `catalog_work_tag.sexual`(**逐 tag**:`galgame_tag.category='sexual'`);两列 **NOT NULL 无 default**,folksonomy 行(bangumi/dlsite)由其 importer 显式写 **0/false**——那是「**此源不发布安全轴**」的诚实取值,不是数据库默认值(覆盖不对称是真实的:安全轴只存在于 VNDB 派生词表)。**spoiler 天花板下沉进原生查询**:调用方给上限(`spoiler <= ?`),**S2S 面恒 0**(= 前 A2-1e 时代逐字节同的行为)、公开面由 `?spoilers=0|1|2` 控,只有显式要才看得见剧透边。批量:一批 `catalog_work_tag`,非逐 work。空序列化 `[]`。纯 DB 零字节——无 refping 事。
  - **规范层加法覆盖(step 74,加法零破坏;治理契约 refs/proj/70+74)**:每元素可选带 `canonical_id?`(`catalog_tag.id`)/ `tier?`(`0`=core 核心 / `1`=longtail 长尾 / `2`=hidden 隐藏)/ `kind?`(`0`=content 内容 / `1`=meta 平台属性)。命中规则:按 `(source_id, name)` 批量查 `catalog_tag_source_map`——**桥接泳道与原生泳道皆覆盖**,claimed 的 vndb 桥接 tag(`source_id=vndb`、`name=galgame_tag.name`)与 bodyless 的 vndb tag 命中同一 map 键;命中则带三字段,**未映射则三字段全省**(原样照旧展示)。`source_name` 键:vndb=`galgame_tag.name`、bangumi/dlsite=`catalog_work_tag.name`。**消费端展示策略**(非删数据):`tier` 分层折叠(核心默展 / 长尾折叠 / 隐藏不展)、`kind=meta` 移出内容标签云进属性过滤 facet。本波(70a 确定性切片)只建**跨源(≥2 源)NFKC 精确同名组**(一律 `tier=core`,手钉 meta 名单 `kind=meta`);单源 canonical 名 + LLM 跨源配对 + tier 终标留待 70b,故单源 tag 本波恒未映射。
- **`popularity` 块(热度计数集,媒体聚合读面 step 62;治理契约 refs/proj/62,**读面于 W1-pre 本体化** refs/proj/140)**:统一形状 `[{ source_id, metric, value }]`,**一 (source, metric) 至多一元素**,按 `(source_id, metric)` 升序。**`metric` 词表(可扩,加常量即扩)**:`0`=downloads(购买/下载数,DLsite `dl_count`)、`1`=wishlist(收藏数,`wishlist_count`)、`2`=reviews(书面评论数,`review_count`);`10`=bgm_wish、`11`=bgm_collect(dump 键 "done")、`12`=bgm_doing、`13`=bgm_on_hold、`14`=bgm_dropped(Bangumi favorite 五桶);未来源追加新常量,**绝不改号**。**`value` 恒为源原样计数,绝不跨源相加**(DLsite 一笔销售与 Bangumi 一次收藏是不同单位)——消费端按 `(source_id, metric)` 分源渲染,与评分刻度规则同构。**空值语义**:源未公开的计数**无元素**(DLsite 商业作不公开 `dl_count`——缺席 ≠ 0),源公开的 0 是真实值、**有元素**(刷新回路保持其现势)。**一条原生泳道,认领与无体作品同规**:恒读 `catalog_work_popularity`(UNIQUE (work_id, source_id, metric))。**dlsite 桥接泳道已退役**:认领作品的 dlsite 三计数曾在读时从 `galgame_dlsite_meta` 的三列 pivot 出来(NULL 列无行、公开的 0 出行),W1-pre 的一次性收编 **步 t** 按同一语义物化,此后由 `workratings` importer 持久保鲜(claim 闸已拆);bgm 五桶自 T2b/102 起本就认领无体同走原生。**易变值刷新**:写路径均为值变才更的 `ON CONFLICT DO UPDATE`——镜像刷新后重跑即刷新。批量:一批 `catalog_work_popularity`,非逐 work。空序列化 `[]`。纯 DB 零字节——无 refping 事。
- **`playtimes` 块(时长估计集,媒体聚合读面 step 91;治理契约 refs/proj/91)**:统一形状 `[{ source_id, minutes, vote_count }]`,**一源至多一元素**,按 `source_id` 升序。**`minutes` 为分钟归一**(摄取侧单位换算:EG 小时×60、vndb `c_length` 原生即分钟)——**估计语义仍源原生**(vndb=带票中位、erogamespace=社区中位),消费端按 `source_id` 分源渲染,不跨源平均。`vote_count`=支撑估计的用户报告数(vndb `c_lengthnum`;EG 不公布按作品票数→0,真实零)。**本 facet 无 claimed 桥接泳道**(wiki galgame 家族无时长字段)——**所有作品(claimed 与 bodyless 一视同仁)读 `catalog_work_playtime` 原生行**,是 (facet,source) XOR 的退化情形(桥接集恒空,仅原生泳道)。写路径=`cmd/backfill-work-playtime`(EG exact work 锚 × EG 镜像 / vndb exact work 锚 × `src_vndb.vn`),值变才更 `ON CONFLICT DO UPDATE`(镜像/dump 刷新→重跑即刷新);理智上限 1,000 小时,超限拒写(镜像存在 10,000h 级脏值)。批量:全部作品一批 `catalog_work_playtime`,非逐 work。空序列化 `[]`。纯 DB 零字节——无 refping 事。
- **`series` 块(系列归属,step 94;治理契约 refs/proj/94 选项 B)**:统一形状 `[{ id, name, source_id, member_count }]`,按 series id 升序。**系列是一等实体**(`catalog_series` + `catalog_series_member`,非两两边——O(n) 成员、系列名可寻址):dlsite 泳道首发(`series_id`/`series_name` 原样,UNIQUE (source_id, external_id)),未来 vndb/bgm 系列泳道同表加源。**物化门 = ≥2 个锚定 galgame 作品**(单员系列无读面价值,留镜像);`member_count`=系列内锚定作品总数。**catalog 原生 facet,无 claimed 桥接**(wiki 家族的 galgame_series 词表在其自有读面,永不桥入)。写路径=`cmd/import-work-series`(镜像即真相:系列改名就地更/成员 insert-if-absent+stale delete/跌破门槛整系列删)。空序列化 `[]`。纯 DB 零字节——无 refping 事。
- **`platforms` 块(作品级平台集,step 96;治理契约 refs/proj/96)**:统一形状 `[{ platform, source_id }]`,按 platform 升序。**码 = `catalog_platform` registry 键(vndb 码表 48 枚:win/and/ios/…)**,registry 提供显示名与弃用审计;列本身存 text 码(同 lang 列约定,不 FK)。**平台事实存在两个粒度**:vndb/dlsite 面在 **release 级**(`releases[].platform` 主码 + `releases[].platforms` 全数组——移植是 release 级事实);bgm 面在 **work 级**(Bangumi 把平台标在 subject 上,其无体作品多数无 release 行,1,875/13,293)——本块只出 work 级显式行,消费端按需并集两粒度。**catalog 原生 facet,无 claimed 桥接**。写路径=`cmd/import-work-platforms`(dlsite 泳道:镜像 `product_json.platform` pc→win/android→and/ios→ios,**smartphone/play 为观看器标志非 OS 移植,跳过**,仅 galgame 媒介存根;bgm 泳道:infobox `平台` 经映射表规范化,未映射值计数上报不猜)。幂等:dlsite 写守 platform 仍空,bgm 写 ON CONFLICT DO NOTHING。空序列化 `[]`。纯 DB 零字节——无 refping 事。
- **`refs` 与 `releases[].anchors` 的分工**(两视图并存,面向不同消费者):`refs` = **消费级摘要**(exact-only,产品站直接渲染,无需理解档位);`releases[].anchors` = **质检全景**(逐 release 的全部锚,**显式携带 `link_kind` 与 `matched_by`**,供内部数据浏览器等按档自筛)。产品站消费一律用 `refs`;`anchors` 中出现非 exact 档位时消费端**必须**按 `link_kind` 自筛,不得当身份使用。
- 用途:letmoe 音声读穿页(镜像其 wiki 读穿),社团归属由此获得。`GET /catalog/works/{id}`(§2.7)返回**同一 bundle**(含 `refs`)。

### 2.5 `GET /catalog/works/{id}/credits` — 作品署名(只读)

按 role 分组的署名列表。每组 = role(id/key/name)+ 条目;条目 = 名义(id + lang 分桶名 + latin)+ 可选 character(id+名,VA 用)+ note + source key。**孤儿名义原样出**(person 层未建,如实);排序 role_id 权重 + 源 + 名义 id。

### 2.6 `GET /catalog/search/entities?q=&type=&locale=&limit=` — 实体搜索(只读)

- `type` ∈ `names|characters|labels`(单选;非法 → Huma enum 校验 422);
- **`locale` ∈ `zh|ja|en` → 服务端映射 Meili 查询语言**(`zh→cmn`/`ja→jpn`/`en→默认管线`;不变量 2:消费者只传粗粒度 UI locale,**服务端钉查询语言,绝不透传任意 Meili 参数**);
- `limit` cap 20;空 `q` → 按 popularity 返回热门。
- 响应条目:id(前缀 n/c/b)· entity_type · name(分桶取非空)· latin · sources · popularity · kind(label)· person_id(名义,缺省=孤儿)。

### 2.7 内部浏览器三端点(D-02,同 Basic S2S 读面)

供**内部数据浏览器**(wiki 前端 staff 专用,经 galgame 后端代理)用;仍是 Basic S2S 读面。

- `GET /catalog/stats`:仪表盘全部计数**单端点单往返**——works 矩阵(medium × 认领态 × status)、实体计数(**孤儿名义单列**,person=0 如实)、credits 按 source、归属边 by kind、**refs source × tier 交叉表**(身份质量一张表)、队列水位(candidates/proposals by status、probable refs、rejections)、**src_llm bid 判定**(same/different/unsure/deterministic;src_llm 缺表则该段空)、**新鲜度 = 各 source 锚 max(created_at)**(诚实近似,不加簿记)。⚠️ **本端点是内部遥测,不上公开面**:队列水位/LLM 判定/锚交叉表/新鲜度/孤儿与 claim 态矩阵描述的是「注册表如何被治理」。产品面要的「目录有多大」由 **公开面 `GET /v1/catalog/stats`(149b)** 单独回答——**另一套 DTO、另一组 SQL**(LIVE works 按 medium + 身份族存量,r18 计入),见 [developer-platform/02 §3.2](../developer-platform/02-public-api.md)。
- `GET /catalog/works/{id}`:与 2.4 by-anchor 同 bundle,入口换 catalog id;404 同义。
- `GET /catalog/labels/{id}/works`:厂牌反查(经归属边),返回 label 自身信息(`label`:id/名/kind)+ offset 分页作品列表(cap 50)+ total,页面直达即自足。**被合并掉的 label(软删除 + 留下 catalog_redirect)与不存在的 id 同义 → 404**;旧 id 的去向走 §2.1 resolve / §2.2 redirects。公开面 `GET /v1/catalog/labels/{id}` 在同一情形下更进一步:**301 + `Location` + 信封 `code=12` 且 `data.current_id` 给出幸存者 id**(绝不在旧 id 下 200 出幸存者内容)。

### 2.8 `GET /catalog/works/search?q=&medium_id=&limit=` — 作品标题搜索(只读)

产品站「上游优先建游」的选择器面(letmoe step 18):staff 在建游首屏一个输入框里搜上游是否已有此作品,搜到即一键读穿建行,无需填全量信息。

- `q`:标题子串(**NFKC 折叠**——复用 `catalog_work_title.title_norm` 生成列 `lower(normalize(title, NFKC))`,与导入期折叠字节一致;查询侧同法折叠后 `LIKE '%…%'`;空 `q` → Huma minLength 422)。
- `medium_id`:可选 medium 过滤(**-1 = 全部**,Huma 无法表达可空标量故用哨兵;letmoe 建游默认传 `1`=galgame 收窄结果);`limit` cap 50(默认 20)。
- **v1 无 trgm 索引**:纯 `ILIKE` 扫 ~19 万 title 行,staff 低频可接受;调用量升再对 `title_norm` 加 `pg_trgm` GIN 索引(记录,量升触发)。
- 响应 `items[]` = 轻 brief:`work_id` · `display_name` · `medium_id` · `content_rating` · `status` · **`site`(认领态,空=未认领)** · **`dlsite_id`(首个 DLsite workno 锚,无则缺省)**。`merged`(status=2)墓碑不出面。选中后产品站按需再走 §2.4 by-anchor / §2.7 works/{id} 取全量 bundle。

### 2.9 `GET /catalog/names/{id}/works` — 名义反查(只读)

名义(署名)反查:这个名义参与了哪些作品。用途 = letmoe 实体页(人物/名义页,step 20)的数据源之一——「这个制作方/声优/脚本还做了哪些」硬需求①,页面直达即自足。

- **名义自述**(`name`):`id` + **lang 分桶名**(`ja`/`zh`/`other` 三桶,名义只落其一——search 不变量 1)+ `latin` + `person_id` + **`siblings`(同一 person 的其他名义,各 `id`/分桶名/latin)**。`person_id` 与 `siblings` 一并给,消费方的人物页免二次查其余名义。
  - ⚠️ **link-visibility 铁则**(`model.LinkVisibility`,search/doc.go 把「人物页装配时过滤」明确指向此端点):credit_name→person 的**隐藏链接从不进入「同一人」聚合**。故 ①被查名义自身链接为隐藏时,`person_id` 与 `siblings` 一律不出(该名义呈现为独立身份);②`siblings` 恒只含**公开链接**的兄弟名义。
- **作品列表**(`items`,offset 分页,cap 50):每条 = `work`(轻 brief:`work_id`/`display_name`/`medium_id`/`content_rating`/`status`/`site` 认领态)+ **`roles`**(该名义在此作担任的**全部** role,每条 `role_id`/`role_key`/`role_name` + 若配音则 `character_id`/`character` 具名)。`total` = 该名义参与的**去重作品数**。
- 反查走 `idx_catalog_credit_credit_name_id`(既有索引,无需新建)。缺失 id → 404(与 by-anchor/works/{id} 同义)。

### 2.10 `GET /catalog/characters/{id}/works` — 角色反查(只读)

角色反查:这个角色出现在哪些作品、由谁配音。用途同 2.9(letmoe 角色页,step 20)。step 46 起为**并集**:出演边(`catalog_work_character`)∪ 配音(`catalog_credit`)。

- **角色自述**(`character`):`id` + **lang 分桶名** + `latin`。
- **作品列表**(`items`,offset 分页,cap 50):每条 = `work`(同 2.9 轻 brief)+ **`kind`**(出演边强度:0=unknown/1=main/2=secondary/3=appears;**仅经 credit 命中而无出演边时为 0**)+ **`spoiler`**(出演剧透档:0=none/1=minor/2=major,VNDB 源;Bangumi/EG 及仅 credit 命中时为 0,step 47)+ **`voiced`**(bool,该角色在此作是否有 VA credit——即使 `kind=0` 也能区分「纯出演」与「有配音」)+ **`voices`**(经 credits 的 `character_id` 关联,为该角色配音的名义,各 `credit_name_id`/`name`/`lang`/`latin`;同名义同作多条 credit 去重为一;纯出演作序列化 `[]`)。`total` = 该角色出现的**去重作品数(并集)**。
- 反查走 `uq_catalog_work_character`(character_id 成员)+ `idx_catalog_credit_character_id`(既有索引,无需新建)。缺失 id → 404。

### 2.11 `GET /catalog/characters/{id}` — 角色详情(只读,step 46)

角色实体自述,letmoe/kungal 角色页直达即自足(§2.4 的 `characters` 投影给的是 work 内花名册摘要,本端点给的是角色本体)。

- 响应 `data`:`id` · `display_name` · `latin` · `lang` · **`gender`**(1=male/2=female/3=other;缺省=unknown,无 0 值)· `description` · **`instance_of`**(跨宇宙变体的基底角色 id,VNDB instance_of;非变体则缺省)· **`image_hash`**(立绘内容哈希,step 47 VNDB 波前恒缺省)· **典型集物理属性**(step 81 字段 PR C2,真列平铺,均缺省=unknown、无有意义 0 值):`birthday_month`/`birthday_day`(月/日;年份不上列——见 `extra`)· `blood_type`(1=A/2=B/3=AB/4=O)· `height_cm` · `weight_kg` · `bust_cm`/`waist_cm`/`hip_cm` · `cup`(逐字大写罩杯 token)· **`extra`**(受治理长尾,源命名空间 `{"bgm": {…}}`:未晋升为真列的 Bangumi infobox 字段——星座/年龄/属性/趣味…,以及被剔出真列的生日年份/越界测量值原样保留;值为字符串或字符串数组;无长尾则缺省)· **`attr_sources`**(真列属性的来源归因 `{列名 → source key}`,如 `{"gender":"vndb","blood_type":"bangumi"}`,取自各属性列 field_provenance 的最新 writer;无填充属性则缺省)· **`aliases`**(书写变体,各 `id`/`name`/`latin`/`lang`/`kind`(0=translation/1=spelling_variant/2=search_hint)/`is_primary_for_locale`;无别名序列化 `[]`)· **`intros`**(多语言角色简介,step 65 字段 PR C1:统一形状 `[{ lang, intro, source_id }]`,与 §2.4 work `intro` 块同形;一语言一元素,同语言多源按 `source_id` 升序取一;**角色是 catalog 原生实体,无 claimed/bodyless 之分、无桥接**——恒读 `catalog_character_intro` 原生行;源料 = Bangumi 角色 summary(假名启发式 ja/zh-Hans)+ VNDB 角色 description(en,`[spoiler]` 段**整段剔除**后落库);无简介序列化 `[]`)· **`traits`**(VNDB trait 集,step 93:统一形状 `[{ id, name, group_tid, group_name?, sexual?, spoiler_level, lie? }]`,按 `(group_tid, gorder, name)` 排序令组内连续;`?spoilers=0|1|2` 封顶 spoiler_level(**默认 0——不显式要就永不泄剧透**);`name`/`group_name` 为 VNDB 英文原样(zh 本地化=未来波,alias/description 已入库作原料);`sexual` 标志随行——catalog 是 R18 面,消费端自行门控(step 81 BWH/cup 先例);`lie`=VNDB「表面为真实为谎」语义原样;词表单源(vndb_tid UNIQUE 即 source map,无 map 表);无 trait 序列化 `[]`)。
  - 属性来源与 survivorship(step 81):真列由 `internal/jobs/charattrs` 从 VNDB 类型列(优先)与 Bangumi infobox(补缺)回填;同字段两源都有 → VNDB 胜(类型列 > 自由文本正则);人工编辑(`field_provenance` writer=`user`)永不被回填覆盖。
- 缺失 id → 404(与 labels/{id}/works、by-anchor 同义)。

> **读面无 site 绑定**(16 语义:绑定只作用于写端点 claim);读端点仍走 Basic S2S(无凭据 401)。

### 2.12 `POST /catalog/edit/images` — 编辑面字节上传(写,multipart;wave 169)

编辑面 covers/screenshots 只收**已存在的 image hash**(行集编辑与字节存在分离,见 editspec/work_media.go);本端点即字节那一半:产品站 BFF 把用户选的封面/截图 multipart 转发到这里,catalog 以**自己的图床身份(site_key `catalog`)**上传并返回 hash,随后编辑提案携带该 hash 提交行集。字节落 catalog scope → 每日 catalog refping 自动保活,galgame_wiki 图床 key 永不经手(03 §2)。

- 请求:multipart `file`(必填)· `preset`(闭集 `galgame_banner`=封面 / `galgame_screenshot`)· `actor_uid`(可选,断言的终端用户 id,记入图床 `first_uploader_sub` 为 `kungal:{uid}`)。
- 响应 `data`:图床 UploadResult 原样(`hash`/`url`/`variant_urls`/`width`/`height`/`thumbhash`/`size_bytes`/`deduplicated`)。
- 错误:preset 不在闭集/缺文件 → 400;配额超限/审核拒绝 → 400(message 区分);图床身份未配置 → 503;图床异常 → 502。
- **注意:本端点是纯 Fiber 路由,不在生成的 openapi.yaml 里**(multipart 不入 Huma 面);鉴权同前缀 Basic S2S。

## 3. Admin 三桶(Bearer JWT + **ren 超管角色**,前缀 `/api/v1/admin/catalog`)

人审治理面,把「机器不敢自动终判」的三类东西交给人:

| 桶 | 端点 | 是什么 |
|----|------|--------|
| **candidates** | `GET /candidates` · `POST /candidates/decide` | 匹配候选(如共享 twitter/pixiv handle 的名义对)——判「同一人/不是」 |
| **proposals** | `GET /proposals` · `POST /proposals/{id}/{action}` | 合并提案(把两个实体判为同一个)——approve/reject |
| **refs** | `GET /refs/probable` · `POST /refs/confirm` · `POST /refs/reject` | probable(1)级来源锚——升为 exact 或驳回 |

admin 面走 oauth 的共享 JWT 中间件 + `RequireRole("ren")`(**超管专属**——目录人审是高权限运营面,普通 admin 不放行);**不经 site 绑定列**(它是运营人审,不是产品 S2S)。

## 4. 鉴权形态

- **S2S face(`/api/v1/catalog/*`)**:`Authorization: Basic <b64(client_id:client_secret)>`,对 `oauth_clients` 注册表校验。任何有效一等 client 可**认证**;但——
- **写路径 per-client site 绑定**:`oauth_clients.catalog_site`(可空 text,size 64,无唯一约束——一站可多 client)。`POST /catalog/works/claim` 要求认证 client 的 `catalog_site` **非空**且 **== 请求体 `site`**,否则 **403**(未绑定或站点不匹配的信息写在 message)。未绑定的 client 根本不能 claim。**只读端点(resolve / redirects / by-anchor / credits / search)不受此限。** `site` 值即租户键(写入 `catalog_work.site`),**无白名单/注册表**——合法性只由「client 绑定值 == 请求 site」把关;新增消费站 = 给其 client 设 `catalog_site`,别无它步。
- **消费站开通(SQL,无管理 UI)**:直接设 `oauth_clients.catalog_site`。
  - galgame wiki(第一消费站):`UPDATE oauth_clients SET catalog_site='galgame_wiki' WHERE image_site_key='galgame_wiki' AND id <> 'galgame-wiki-admin';`
  - **letmoe(第二消费站,同人为主)**:`UPDATE oauth_clients SET catalog_site='letmoe' WHERE <letmoe client 定位>;`(dev = 本地主库执行即可复现;**prod = 用户 ops**,随 letmoe 上线 runbook 同批,核验 `SELECT id,catalog_site FROM oauth_clients WHERE catalog_site='letmoe'` 命中 letmoe 机密 client)。
- **admin face(`/api/v1/admin/catalog/*`)**:Bearer JWT(accept-both verifier)+ **ren 角色(超管专属)**,与 site 绑定列无关。
- **编辑引擎提案桥面（过渡参考，09-open-api-phase2 06b）**：catalog 进程另托一个 galgame-family 的**平台提案面** `/internal/edit/*`（create / mine / get-own / withdraw + schema/snapshot 只读投影），走 devapi 双凭证链——scope **`galgame:propose`**、计量 face **`galgame_internal_propose`**；actor 取自已验用户 JWT（plain：trust 0 / roles ∅ / 非 owner），租户由 key 的 `oauth_clients.catalog_site` 反查（请求**不收** site/actor 断言）。它是**纯 Fiber、不进本目录 spec**（`openapi.yaml` 仅含 S2S face）；编辑引擎的 S2S 面（`/api/v1/catalog/edit/*`，断言式 actor + 审核三件套）不变。**桥面不立独立契约文档**，第三方实际开放另议。
- `GET /openapi.json`(S2S spec)、`GET /healthz` 无鉴权。

## 5. 生成 spec

- S2S:`go run ./cmd/gen-openapi -catalog -o docs/catalog/openapi.yaml`(OpenAPI 3.1)。
- admin:`go run ./cmd/gen-openapi -catalog-admin -o docs/catalog/admin-openapi.yaml`。
- 契约以生成的 spec 为准(Huma code-first,DTO 即契约);本 markdown 是语义说明。

## 6. 运维注记

- **schema 迁移**:`cmd/migrate-catalog` 是 `kun_catalog` 的**唯一** schema 入口,幂等(AutoMigrate + `IF NOT EXISTS` 原始 SQL + 存在性守卫 seed)。生产随部署自动跑(compose `migrate-catalog` gate,catalog 服务 `depends_on: service_completed_successfully`);catalog 服务自身**不跑迁移**,只连接 + 就绪检查。
- ⚠️ **导入类 cmd 不随部署自动跑**:`reconcile-galgame-works` / `import-*` / `reindex-catalog` 等是手动运维工具(经 `tools` 镜像 + env-file),**部署不会触发**。跑完批量导入后需**手动** `reindex-catalog` 重建搜索索引(批量脚本不走写穿钩子)。
- **主库变更提醒**:`oauth_clients.catalog_site` 列落在**主库 `kun_galgame_infra`**(经 `cmd/migrate` AutoMigrate)——见工程侧变更时的迁移铁则。
- **服务拓扑**:catalog 内网端口 9281,产品后端经 `http://catalog:9281` 走 dokploy-network(无公开域名);web(oauth admin 前端)SSR 经 `NUXT_CATALOG_API_BASE_SSR=http://catalog:9281/api/v1`。
