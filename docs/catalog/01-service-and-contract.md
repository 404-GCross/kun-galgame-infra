# 01 — 服务定位与契约

> catalog 是**跨媒介身份/图谱注册层**:把多来源的作品/发行/人物/署名/厂牌收敛成一套带来源锚与分级信任的规范身份。产品站通过 S2S 三端点接入;人审经 admin 三桶治理;写路径按 per-client site 绑定授权。本篇是对外契约,数据结构以生成的 [openapi.yaml](./openapi.yaml) / [admin-openapi.yaml](./admin-openapi.yaml) 为准。

## 1. 服务定位:registry 层 vs body 层

catalog **只管身份、关系、来源锚**:

- **实体**:`work`(作品)/`release`(发行/SKU)/`credit_name`(署名名义,孤儿合法)/`person`(人)/`character`(角色)/`org`/`label`(厂牌/社团)。实体类型常量 `0=person 1=credit_name 2=org 3=label 4=character 5=work 6=release`。
- **来源锚** `catalog_external_ref`:把实体锚到外部来源的 id,按 `link_kind` 分级 `exact(0)` / `probable(1)` / `related(2)`;exact 有唯一约束(一个来源的一个外部 id 只精确锚一个同类实体)。
- **关系**:credit(**署名边**:work ↔ credit_name/label,"谁演了什么角色/担任什么职务")、**work_label 归属边**(work ↔ label,"哪个社团/发行方对作品负责";`kind`:0=circle/1=publisher/2=developer/3=brand)、**work_character 花名册边**(work ↔ character,"哪个角色出现在作品里";`kind`:0=unknown/1=main/2=secondary/3=appears,**0=unknown 是有意义值**——EG 无主配分型、Bangumi 低频尾型归此;`spoiler`:0=none/1=minor/2=major,VNDB 源、Bangumi/EG 恒 0)、redirect(合并后的旧→新 id)、alias(名义别名)。**署名 ≠ 归属 ≠ 出演**:credit 是个人署名,work_label 是组织责任,work_character 是出演事实(有配音的角色会同时在 credit 与 work_character 两表,语义不同);读面(§2.4/§2.10)负责合并展示,三者并存不互斥。

catalog **不存**产品展示体:简介、封面/截图字节、评分、点赞、收藏、NSFW 过滤——这些是**产品站(body 层)**各自持有的。产品站保留自己的富行,只把「这是**哪一个**作品/人物」的身份问题委托给 catalog。这条分界是硬约束:catalog 加展示字段 = 越界。

来源注册表(节选):`source` `2=vndb 3=bangumi 4=dlsite 5=erogamespace 1=user`;`medium` `1=galgame 5=asmr`;`content_rating` `0=all_ages 1=sensitive 2=r18`。完整注册表由 `cmd/migrate-catalog` 的 seed 落库。

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

- 响应 `data`:`work`(id/medium/display_name/olang/content_rating/status/**site 认领态**)+ `titles`(official/alias/abbreviation/search_hint)+ `releases`(每个含 kind/模糊日期/各自 `anchors`)+ **`labels`(经 work_label 归属边,含 label 自身 kind + 归属 kind)**+ **`refs`**+ **`characters`(花名册投影)**+ **`intro`(多语言简介,媒体聚合读面)**+ **`covers`(封面集,含竖版,媒体聚合读面)**。
- **`refs` 块(消费面)**:把本作品**全部 exact 锚**(work 级 + release 级)拍平成一张表,每条 `{ source, external_id, level(work|release), release_id? }`(`release_id` 仅 release 级)。用途:渲染 DLsite/EG 外链、展示跨源身份链。**只出 exact 档**——`probable` 是审核泳道内部态、`related` 是非身份链接,均不入 `refs`(`relations`(work↔work 关系边)v1 亦不出面,随消费再加)。
- **`characters` 块(花名册,step 46;spoiler step 47)**:`work_character` 出演边 **∪** VA credit(`catalog_credit.character_id`)的**并集**——每条 `{ character_id, display_name, latin, gender, kind, spoiler, image_hash, va[{credit_name_id, name}] }`。合并语义:有出演边者 `kind`/`spoiler` 从边取;**credit-only 角色**(仅有 VA credit、无出演边,如 VNDB 路 credits)也出,`kind=0`/`spoiler=0`;`va` 列出为该角色配音的**全部**名义(同名义多 credit 去重)。**`spoiler`**(0=none/1=minor/2=major,VNDB `chars_vns.spoil`;Bangumi/EG 边恒 0)= 该角色在此作出场的剧透档;排序:主角优先(kind 有序 main→secondary→appears→unknown)后按 display_name;`va` 内按 name。**链接可见性铁则**:`va` 只暴露 credit_name 名义本身(名义永远可见),**不做任何 person 展开**(person 聚合才受政策辖制)。空花名册序列化 `[]`;角色 `image_hash` 在 step 48 VNDB 立绘波前恒为空(缺省)。
- **`intro` 块(多语言简介,媒体聚合读面 step 52;治理契约 refs/proj/51)**:统一形状 `[{ lang, intro, source_id }]`,一语言一元素,claimed/bodyless 归并后消费者无感来源。**桥接归并**:**claimed 作品**(`site='galgame_wiki'`)简介**读时桥接**自 galgame body 的四固定语言列 `galgame.intro_{en_us,ja_jp,zh_cn,zh_tw}`(列→行 pivot,lang=`en`/`ja`/`zh-Hans`/`zh-Hant`),`source_id`=**galgame_wiki**(catalog_source 12);**绝不复制**进 catalog 原生行(bridge-not-copy)。**bodyless 作品**(`site=''`)读 `catalog_work_intro` 原生行,一语言多源时按 `source_id` 升序取一(user > vndb > …)。**严格 XOR**:claimed 作品只读桥接;galgame 简介全空 = `intro` 空 `[]`,**不回落**原生行(即便存在被遮蔽的原生行也不读——shadow-never-delete)。`source_id` 引用 `catalog_source` 注册表(归因,§8.C)。批量:读面按 claimed/bodyless 分两批查(galgame 一批 pivot + catalog_work_intro 一批),非逐 work。空序列化 `[]`。本波源料仅 VNDB `vn.description`(英文,原样存,不清洗);DLsite/EG 简介留待后续波。
- **`covers` 块(封面集,含竖版,媒体聚合读面 step 53;治理契约 refs/proj/51)**:统一形状 `[{ image_hash, kind, portrait_pinned, sort_order, sexual, violence, source_id }]`,一封面一元素,claimed/bodyless 归并后消费者无感来源;按 `(sort_order, image_hash)` 排序。**竖版**由 `portrait_pinned=true` 挑出——kungal/moyu 重构读此值即得竖版封面。**桥接归并**:**claimed 作品**(`site='galgame_wiki'`)封面**读时桥接**自 `galgame_cover`(kind/portrait_pinned/sexual/violence 原样),`source_id` 由 `galgame_cover.source` 文本映射到 `catalog_source`:`''`→**galgame_wiki**(12,wiki 用户上传=首方 body)、`vndb`→vndb(2)、`bangumi`→bangumi(3)、`upscale`→**upscale**(13,本波新增的首方 AI 放大衍生源);**绝不复制**进 catalog 原生行,桥接读**不重传字节**(galgame 封面永住 galgame_wiki image scope)。**bodyless 作品**(`site=''`)读 `catalog_work_cover` 原生行(字节住 catalog image scope)。**严格 XOR**:claimed 只读桥接;galgame 封面空 = `covers` 空 `[]`,**不回落**原生行(即便存在被遮蔽的原生行也不读——shadow-never-delete §8.B)。批量:claimed 一批 `galgame_cover` + bodyless 一批 `catalog_work_cover`,非逐 work。空序列化 `[]`。**字节纪律 §4**:catalog scope 图(bodyless 封面+立绘)由 `catalog-image-refping` 保活,其全集含 `catalog_work_cover` **全部行(含被遮蔽行)**,漏计遮蔽行 = GC 吃活图(66k 同类);galgame scope 图(claimed 桥接封面)由 `galgame-image-refping` 独立保活。**bodyless 封面数据**:EG(bodyless 主锚 5,844)无任何封面字节(纯统计站,零图列),DLsite(6,102 release 锚)`product_json` 虽带封面 URL 但需从外部 CDN 抓 ~6k 图另立字节波——本波只交付 schema+读面+refping 管线,bodyless 封面数据留待「确认更好 bodyless 封面源」后续波(诚实,不硬凑)。
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

- `GET /catalog/stats`:仪表盘全部计数**单端点单往返**——works 矩阵(medium × 认领态 × status)、实体计数(**孤儿名义单列**,person=0 如实)、credits 按 source、归属边 by kind、**refs source × tier 交叉表**(身份质量一张表)、队列水位(candidates/proposals by status、probable refs、rejections)、**src_llm bid 判定**(same/different/unsure/deterministic;src_llm 缺表则该段空)、**新鲜度 = 各 source 锚 max(created_at)**(诚实近似,不加簿记)。
- `GET /catalog/works/{id}`:与 2.4 by-anchor 同 bundle,入口换 catalog id;404 同义。
- `GET /catalog/labels/{id}/works`:厂牌反查(经归属边),返回 label 自身信息(`label`:id/名/kind)+ offset 分页作品列表(cap 50)+ total,页面直达即自足。

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

- 响应 `data`:`id` · `display_name` · `latin` · `lang` · **`gender`**(1=male/2=female/3=other;缺省=unknown,无 0 值)· `description` · **`instance_of`**(跨宇宙变体的基底角色 id,VNDB instance_of;非变体则缺省)· **`image_hash`**(立绘内容哈希,step 47 VNDB 波前恒缺省)· **`aliases`**(书写变体,各 `id`/`name`/`latin`/`lang`/`kind`(0=translation/1=spelling_variant/2=search_hint)/`is_primary_for_locale`;无别名序列化 `[]`)。
- 缺失 id → 404(与 labels/{id}/works、by-anchor 同义)。

> **读面无 site 绑定**(16 语义:绑定只作用于写端点 claim);读端点仍走 Basic S2S(无凭据 401)。

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
