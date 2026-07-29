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
- 2026-07-28 **用户「全部按推荐来」放行 → W1+W2 当日实现完结**(3 commit,未 push)。四拍板全按推荐:弃用窗 90 天(Sunset 2026-10-31)/changes 走 touch 纪律/forum 词表页数据换轨/W2 两会话。并行结构诚实调整:W2B(names/characters refs)体量小且与 W1 通用 refs loader 强耦合→并入 W1;实剩 W2A(works)+W2C(tags)单会话串行(交付物与两会话逐字节一致)。**W1 骨架** `9a5aa249`(14 文件+1215/-32):迁移两索引(work_tag(name,source_id)+work(updated_at,id))/DTO 加法(release id+refs、work updated、tag canonical 覆盖、三实体 refs[]、四新列表类型)/service(cursor codec+通用 entityRefs+works-list 富化 helper,release refs+tag 覆盖+updated+三实体 refs 接线)/三端点 handler+route+huma spec/galgame 面 spec Deprecated+运行时 Deprecation/Sunset/Link 头。**W2 查询** `78411a43`(3 文件+448/-49):WorksList keyset 八过滤器+sort id/updated、Changes(updated_at,id)ASC 恒 cursor 无 nsfw 门、TagDetail+include=works;三集成测试(sfw/nsfw 门/keyset 走页+终页/坏 cursor 拒/tag 附着)catalog service+handler 全包真跑绿 6.9s/10.3s(host=localhost+.pgpass 非假绿)。**收口** `538502ca`:spec 再生(catalog public+;galgame-public 全 op deprecated;S2S/admin 逐字节不变)+Tier-A 02 §3.2 三新行+galgame 弃用横幅。执行记录落 doc 106 §12。
- 2026-07-28 **剩余(待用户令)**:①**W3 下游迁移未开工**(letmoe→moyu→forum 三梯队+infra 摘牌半波,绞杀窗 90 天内按梯队做);②**三 commit 未 push**(push 归用户;部署经 migrate-catalog gate 自动跑索引,catalog 重部后三端点上线+galgame 弃用头生效);③**⚠️ changes 流 touch 纪律=数据聚合轨挂账**:facet 回填(cmd/import-*/backfill-*/enrich-*)不 touch work 行→changes 对纯 facet 更新失明,需聚合轨在导入器收官清单加 `UPDATE catalog_work SET updated_at=now() WHERE id IN(受影响)`(本轨只报不改 cmd/* 域);④**三方评审未派**(用户直接放行实现)——建议 push 前补一轮 glm-reviewer 对 W1+W2 diff 交叉验收。
- 2026-07-28 **交叉验收派发并通过:13/13 全 PASS,零阻断项**(doc 106 §13)。glm-reviewer(GLM-5.2)逐条 1-13 全 PASS + 督查(Fable)不同路径独立复核承重项双证:spec 逐字节不变(git diff 空)/测试真跑(service 0.84s+handler 11.3s,非 0.00Xs 假绿)/refs 硬红线(LinkKindExact=0)/弃用头路由组前置+五 method 全覆盖(Sunset 2026-10-31 与 @1785196800 自洽 95 天≥90)/build+vet 净/迁移 IF NOT EXISTS 幂等/Tier-A 文档逐条对齐。三条建议**全非阻断,不改代码**:①changes facet 失明=聚合轨挂账(已记 §12/§13);②`WorksListFilter.ReleasedBefor` 拼写少 e(内部未导出无 wire 影响,cleanup 波改);③`publicCursor.Updated` omitempty(不透明 cursor 内部字段,无害)。W1+W2 双模型交叉验收通过,可交用户 push;评审↔修改零轮(无 FAIL)。
- 2026-07-28 **cleanup 波完结**(doc 106 §14):用户批推荐方案→Opus 执行 `f4418994`(非法 id→400/limit clamp 高 400 低/ReleasedBefore 改名/changes 5s 水位滞后/删除信号 Tier-A 口径+参数区间表)→Fable 隔离 worktree 独立验收全绿(build/vet/两包真跑×2/spec 再生幂等/S2S 零漂移),未 push,零迁移。插曲:kun_catalog_test 共库+service 套件无 advisory 锁→并行轨同跑偶发假红,复跑定性。边界外记录:Redirects/Search limit 仍宽容(待小决策)。**剩余=W3 三梯队迁移+摘牌半波(待用户令)**;facet touch 纪律仍挂聚合轨。
- 2026-07-29 **W3 第一梯队 letmoe 迁移完结**(doc 106 §15):letmoe 自有 doc 26「彻底版」取代 §8 草案(galgameclient 整包退役、全走 S2S catalogclient、锚统一 work_id);Opus 执行 W2-W5(letmoe 四 commit `fa3c890`→`e93f5ef`,未 push),Fable 验收全绿(20 包+26 具名 PASS 零 skip/typecheck+build 双绿/迁移 024 仲裁配套/残迹独立复扫零运行时命中/两条 S2S 契约论断亲核为真)。判断调用报用户:official/tag 反查页退役(tag 恢复路径=infra S2S 加法 tags/{id}/works)、blur-up 降级(S2S 无 thumbhash)。部署序=push→migrate 024→紧接 backfill dry→apply(galgame:nsfw key)→删工具;计数归零一周判据部署后看。下一梯队=moyu。
- 2026-07-29 **外部 Fable 评审裁决落档 + 轨暂停在 letmoe**(doc 106 §16):评审 3"阻断"+「暂缓 push/106-R 修订波」被否——核心前提为假(五 commit 全在 origin/main 且已部署;正确框架=成本窗口关于消费者出现时而非 push 时)。提取**加法跟进队列 Q1-Q6**(Q1=`lookup?type=` 实体反查排队列头,moyu/forum 或第三方开放前;Q2=changes `op` 待真实镜像消费者;Q3-Q6 backlog 搭车)+否决留痕(404 隐私姿态/nsfw 收敛/medium 缺省勿再翻案);touch 纪律 CI 批评成立→缓解建议转聚合轨。**用户令「先做到 letmoe 这里」:moyu→forum 梯队+摘牌半波待令**;相邻轨知悉=galgame 家族表彻底退役轨(摘牌半波重启先对齐)+dmm 数据源决策轨。
