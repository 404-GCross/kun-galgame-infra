# canonical-api(/v1 数据 API 重设计)— pi 值守文件

**使命**:/v1 数据 API 的彻底重设计——现行 `/v1/galgame/*` 是 kungal 旧读面的投影;catalog 聚合数据才是真源,API 从数据正推设计,下游(kungal/moyu/letmoe)自行适配新结构。wiki-retirement 量级的专属工程。
**Claude memory 参考(只读)**:`media-aggregation-track.md`(数据侧语境)+ `open-api-and-wiki-retirement.md`(API 面语境)。
**底稿**:`refs/proj/106-canonical-api-redesign.md`(设计文档,本轨首件)。commit 前缀 `feat(api-v1)`。

## 用户裁决(开工令,已拍板方向)

设计原则六条:①实体为中心(works/persons/characters/labels/tags/releases 同构读面);②聚合值+per-source 溯源并存为一等结构;③refs[] 全实体同构;④claimed/bodyless 与 content_rating 显式化(默认过滤须是声明的行为);⑤命名去 wiki 化、统一 envelope、keyset 分页、include= 机制;⑥旧 /v1/galgame 面绞杀式弃用(明确窗口),下游迁移波次单列。

产出要求:完整字段形状草案(每实体一节,含 JSON 示例)、目标 OpenAPI 草案、旧→新映射表、破坏性变更清单、下游影响评估(kungal/moyu/letmoe)、实施波次划分(含 Wave 2 并行的文件所有权表)。**文档完成停下报批**——评审 = 督查 + glm-reviewer + opus-5 三方交叉,用户终审。

## 边界(勿碰)

- 本地 `kun_catalog` 只读;设计阶段零写库、零迁移。
- 数据聚合轨(refs/proj 台账 61 与波文档)、developer 轨(refs/plans/09)只读咨询,不改其文件。
- 编号纪律:粘贴文本的"doc 100"已被 producers 边波占用;本轨设计文档 = **106**(refs/proj 100-105 均已有主)。

## 时机语境

- developer 轨已记档:「/v1 预冻结窗继续敞开、平台持续内测至数据聚合轨收官」——破坏性重设计的窗口现在敞开,聚合轨收官后关闭。
- 数据聚合轨 103-105 波(2026-07-28)刚把公开面推到「全量开放」:works 详情 +12 facet 块、nsfw 参数六端点、characters traits、S2S relations、catalog_works Meili 标题搜索。设计必须以这个最新面为基线,不是 07-23 交接时点的旧面。

## pi 值守状态(就地更新,一行一钩子)

- 2026-07-28 接轨。_index.md 行已加;本文件建立;下一步=供给侧接地实测(现行两套面 galgame/catalog 的真实形状+库真实结构),然后起草 refs/proj/106 设计文档。
- 2026-07-28 **106 设计文档 DRAFT 完成,停下报批**(refs/proj/106-canonical-api-redesign.md)。供给侧全实测:实体人口(work 226,551/claimed 62,451/r18 89.6%/release 318,881/label 38,679/char 227,139/name 143,693/锚 882,383)+facet 覆盖(快照滞后项标注台账口径)+wiki 词表(official→label 已链 99.5%)+下游消费 rg 全仓(letmoe=list/batch/detail、moyu=lookup/detail/taxonomy 四族、forum=stats/officials/tags;零第三方 key=绞杀窗最低成本论证)。核心裁定:**canonical = 完成 /v1/catalog,不另起炉灶**(103-105 波后六原则①②④⑤骨架已在产);缺口七条(G1 works list/G2 changes/G3 release id+refs/G4 三实体 refs[]/G5 tag 规范层寻址/G6 updated+brief 扩展/G7 series 不做独立端点)→新端点 4+既有端点加法;/v1/galgame 定性=kungal 产品读面,摘牌走 §8 三梯队。旧→新映射表逐字段(id 空间 gid→work_id 双向桥已在产)、破坏性变更 4 条、W1 骨架+W2 三并行(文件所有权表,spec 只在收口波再生)+W3 下游。待拍板 4 项:弃用窗 90 天/changes 流 touch 纪律 vs max 视图/forum 词表页归属/W2 并行度。零写库零迁移零 commit(设计阶段);工作树仅 .pi/tracks 两文件已提交。
