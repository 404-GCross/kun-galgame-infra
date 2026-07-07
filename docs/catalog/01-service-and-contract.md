# 01 — 服务定位与契约

> catalog 是**跨媒介身份/图谱注册层**:把多来源的作品/发行/人物/署名/厂牌收敛成一套带来源锚与分级信任的规范身份。产品站通过 S2S 三端点接入;人审经 admin 三桶治理;写路径按 per-client site 绑定授权。本篇是对外契约,数据结构以生成的 [openapi.yaml](./openapi.yaml) / [admin-openapi.yaml](./admin-openapi.yaml) 为准。

## 1. 服务定位:registry 层 vs body 层

catalog **只管身份、关系、来源锚**:

- **实体**:`work`(作品)/`release`(发行/SKU)/`credit_name`(署名名义,孤儿合法)/`person`(人)/`character`(角色)/`org`/`label`(厂牌/社团)。实体类型常量 `0=person 1=credit_name 2=org 3=label 4=character 5=work 6=release`。
- **来源锚** `catalog_external_ref`:把实体锚到外部来源的 id,按 `link_kind` 分级 `exact(0)` / `probable(1)` / `related(2)`;exact 有唯一约束(一个来源的一个外部 id 只精确锚一个同类实体)。
- **关系**:credit(**署名边**:work ↔ credit_name/label,"谁演了什么角色/担任什么职务")、**work_label 归属边**(work ↔ label,"哪个社团/发行方对作品负责";`kind`:0=circle/1=publisher/2=developer/3=brand)、redirect(合并后的旧→新 id)、alias(名义别名)。**署名 ≠ 归属**:credit 是个人署名,work_label 是组织责任,两者并存不互斥。

catalog **不存**产品展示体:简介、封面/截图字节、评分、点赞、收藏、NSFW 过滤——这些是**产品站(body 层)**各自持有的。产品站保留自己的富行,只把「这是**哪一个**作品/人物」的身份问题委托给 catalog。这条分界是硬约束:catalog 加展示字段 = 越界。

来源注册表(节选):`source` `2=vndb 3=bangumi 4=dlsite 5=erogamespace 1=user`;`medium` `1=galgame 5=asmr`;`content_rating` `0=all_ages 1=sensitive 2=r18`。完整注册表由 `cmd/migrate-catalog` 的 seed 落库。

## 2. S2S 端点(Basic client 认证,前缀 `/api/v1/catalog`)

写/运维面:resolve(2.1)· redirects feed(2.2)· claim(2.3,带 site 绑定)。读面(D-01,2.4-2.6):by-anchor · credits · entity search。内部浏览器(D-02,2.7):stats · works/{id} · labels/{id}/works。

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

- 请求:`{ medium_id, site, product_work_id, display_name, olang?, content_rating?, anchors?[{source_id, external_id}] }`。
- 语义:若 `anchors` 里有 **exact** 命中已存在的注册行 → **认领那一个已存在身份,绝不铸第二个**(幂等);否则铸新行。响应 `{ work_id, created }`,`created=true` 表示新铸。
- **幂等**:同一 (锚 / product_work_id) 重复 claim 收敛到同一 `work_id`。
- **冲突 409**:当认领会违反身份唯一性(例如两个不同产品行争抢同一锚)时返回 409 problem(house 信封,`code`/`message`)。
- ⚠️ **site 绑定要求(写端点独有)**:见 §4。

### 2.4 `GET /catalog/works/by-anchor?source=&external_id=` — 锚反查读穿(只读)

产品站拿一个外源 id 读穿到 catalog 作品。`source` 对 `catalog_source` 注册表校验(即白名单:dlsite/vndb/bangumi/erogamespace/…);命中 **work 级或 release 级锚**均可(release 锚回溯其 work;`exact` 优先、work 级优先破平);未命中 **404**。

- 响应 `data`:`work`(id/medium/display_name/olang/content_rating/status/**site 认领态**)+ `titles`(official/alias/abbreviation/search_hint)+ `releases`(每个含 kind/模糊日期/各自 `anchors`)+ **`labels`(经 work_label 归属边,含 label 自身 kind + 归属 kind)**。
- 用途:letmoe 音声读穿页(镜像其 wiki 读穿),社团归属由此获得。

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

> **读面无 site 绑定**(16 语义:绑定只作用于写端点 claim);读端点仍走 Basic S2S(无凭据 401)。

## 3. Admin 三桶(Bearer JWT + admin 角色,前缀 `/api/v1/admin/catalog`)

人审治理面,把「机器不敢自动终判」的三类东西交给人:

| 桶 | 端点 | 是什么 |
|----|------|--------|
| **candidates** | `GET /candidates` · `POST /candidates/decide` | 匹配候选(如共享 twitter/pixiv handle 的名义对)——判「同一人/不是」 |
| **proposals** | `GET /proposals` · `POST /proposals/{id}/{action}` | 合并提案(把两个实体判为同一个)——approve/reject |
| **refs** | `GET /refs/probable` · `POST /refs/confirm` · `POST /refs/reject` | probable(1)级来源锚——升为 exact 或驳回 |

admin 面走 oauth 的共享 JWT 中间件 + `RequireRole("admin")`,与 galgame admin 面同构;**不经 site 绑定列**(它是运营人审,不是产品 S2S)。

## 4. 鉴权形态

- **S2S face(`/api/v1/catalog/*`)**:`Authorization: Basic <b64(client_id:client_secret)>`,对 `oauth_clients` 注册表校验。任何有效一等 client 可**认证**;但——
- **写路径 per-client site 绑定**:`oauth_clients.catalog_site`(可空 text,size 64,无唯一约束——一站可多 client)。`POST /catalog/works/claim` 要求认证 client 的 `catalog_site` **非空**且 **== 请求体 `site`**,否则 **403**(未绑定或站点不匹配的信息写在 message)。未绑定的 client 根本不能 claim。**只读端点(resolve / redirects)不受此限。** 绑定值写法:直接 SQL 设 `oauth_clients.catalog_site`(无管理 UI,后续小步补 oauth admin 编辑面)。
- **admin face(`/api/v1/admin/catalog/*`)**:Bearer JWT(accept-both verifier)+ admin 角色,与 site 绑定列无关。
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
