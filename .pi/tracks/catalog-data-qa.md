# catalog 数据 QA 轨 — pi 值守文件

**使命**:对**已聚合**的 catalog_* 注册表做数据质量纠错——**只修错数据,不建新数据,不跑 merge**(检测+报告重复可以,执行合并归聚合轨)。
**Claude memory 参考(只读)**:`catalog-data-qa-track.md`。**底稿**:`refs/qa/`(章程 00-charter、两轮审计 01/02、deferred-items.tsv、worklists)。

## 边界(勿碰)

- 本地 `kun_catalog` **只读**(审计在这);写沙盒=`kun_catalog_rehearsal`。
- galgame_* 产品表只在明确授权的纠错内碰;聚合轨在飞件(媒体字节回填/人物解析/merge 执行)勿碰。

## 交接快照(2026-07-23)

- **纠错车道已基本打空**:P0 字符清洗+P1 错游戏 bid(183 修+94 降级+富集修复)全生产 LIVE;第二轮审计五类全净。
- **剩**:①**134 人审 = 用户件**(列着别催);②deferred 77+55 条——**可解锁路径已勘明**:bgm.tv 403 挡住的那批,用 bangumi infobox 里的 vndb 链接本地反查可回收部分(17/94 已证),一个 bgm.tv 可达的复审 pass 也能解;③undermerge 两张 worklist(credit_names 13,966 组/characters 1,080)是**给聚合轨的输入**,不是本轨活。
- QA 工具已 commit 未 push(用户推)。

## 纪律要点(本轨特有)

- 每笔纠错三件套:`catalog_match_rejection` 负知识记录(防 re-import 复加)→ 改 ref → 改 galgame.bid;守卫在"当前值==错值"上,幂等。
- 先 rehearsal 重置为 pristine 再演练,后 prod;变更日志 tsv 落 refs/qa/。

## pi 值守状态(就地更新,一行一钩子)

- 2026-07-25 deferred 回收波(refs/qa/05):17/94 反查独立复现;分类 3 promote / 7 merge-dup(交聚合轨)/ 7 继续 deferred。工具 `--promote` 模式已 commit(`e8b0aa7`,未 push)。rehearsal 全程通过(94 demote 重放→3 promote dry/apply/二遍零写→富集修复,全部 SQL 独立验证)。
- 2026-07-26 **prod apply 已完成(用户批准)**:PRE 基线→dry→3 PROMOTE+3 SET_BID→二遍零写→SQL 验证(0 dup)→富集修复 3 alias+1 intro+3 meta 验证全过;日志在 refs/qa/。台账:3→resolved_promoted、7→merge_dup_reported、7→deferred_no_live_subject。
- 2026-07-26 bgm token 到手(apps/api/.env 既有配置):7 条 class-C 线上也无 subject(死案);116 条全量 API 扫描→110 条有线上候选。
- 2026-07-26 **agent 波完成**:8+1 分诊 agent(批 2 双跑一致性完美)→110 条裁定 = 103 STILL_DEFERRED / 2 REPOINT / 5 MERGE_DUP;GLM-5.2 复核 6 PASS / 1 FAIL(3144 拒了,candidate 是 v6433 合集不是 v10620,退回 deferred)。**rehearsal 全过**:2 REPOINT(1629→493602、9183→215565)dry/apply/二遍零写+富集修复 2 alias+1 intro+2 meta+SQL 独立验证;额外揪出 9183 的 'ABANDONER THE SEVERED DREAMS' 污染 alias(来自错 subject infobox 别名,enrich 工具不覆盖),守卫 SQL=sql/21,rehearsal 已清净。4 MERGE_DUP 进 deferred-mergedup-4-wave2.tsv(累计 11 条交聚合轨)。产物:agent-wave-{verdicts-110,glm-review,live-scan-116}.tsv。prod 三件同日落地(见下行)。
- 2026-07-26 **wave-2 prod apply 完成(用户批准"按推荐执行")**:源码对 HEAD 复核+产物 flag 双验证→PRE 基线全过→repoint dry→2 REPOINT+2 UPDATE_BID→二遍零写→富集修复 2 alias+1 intro+2 meta→sql/21 清污染 alias(DELETE 1+UPDATE 1)→终态 SQL 全绿(0 dup,残留 0)。日志:deferred-repoint-prod / deferred-repoint-enrich-prod。台账:1629、9183→resolved_repointed。**deferred 车道收官:134 条全部有权威结论**(5 resolved / 11 merge_dup_reported 交聚合轨 / 7 无线上 subject / 110 确认线上无解 / 1 老 merge_dup);剩余解锁只等 dump 更新或 bangumi 社区建条目,扫描可随时廉价重跑。
- **共享工作树事故 x3**:main.go 的 96 行 promote 代码被别的会话反复 revert(prod 二进制曾因此缺 flag,零写无损),轨文件状态行同样被打回;均 `git restore --source=HEAD` 恢复。教训=从共享树出二进制前要验证产物的 flag 而不是只看源码;轨文件以 git 历史为真源。
- 134 人审 = 用户件,列着未催。undermerge 两张 worklist = 聚合轨输入,未动。
- **二次交接快照(2026-07-31,Claude Fable 留)**:无新债——聚合轨 97 波已把 merge-dup 11 例全部执行完毕(QA 移交两张单清零);134 人审仍=用户件列着别催;undermerge 两张 worklist 已被聚合轨 98 波以更宽 fold 语义超集处理(24,010 对 execute 完)。本轨纯待触发:新纠错线索出现才开波;QA 工具 commit 状态以 git log --grep "fix(catalog-qa)" 为准。

## 2026-08-04 — bgm tag 安全轴波(NSFW 分级 + 脏 tag 清洗,三波单日)

- **背景**:bgm 12,259 名/215,022 行 sexual 全 false(轴从未判过,glm 只判过 tier/kind);读面投影原始行
  的 sexual,词表旗传不出去 → kungal 把拔作/R18/NTR 当正常 tag 展示(SEO 面泄露)。
- **A 波** `9fcddd5a`(Opus 执行/Fable 验收):`cmd/classify-tag-safety` + `internal/jobs/tagsafety`,
  glm-5.2(CF Workers AI,temp 0)三类判定 sexual/junk/normal;批 40 名带索引+原文回显双对齐,截断拒收,
  resume 续跑;apply 三桶幂等、降旗永不自动。真批 14,404 名(bgm+dlsite+词表复核)~3.5h 零 429。
- **B 波** `ee93cc96`:enrichCanonicalTags 把词表 sexual OR 进行——词表=安全轴单一真源。
- **C 波** forum `40c6aa0f`+`450bab35`:SFW 详情响应服务端滤 sexual tag(SSR 泄露堵死)/tag 搜索补
  SFW 闸/sitemap 全量翻页(100→~1,700)/`'all'` 语义两端统一/category 改判真 sexual 旗+tier=hidden 全面
  排除(含详情芯片)。部署顺序:数据已 LIVE → push 两仓即可。
- **prod 落库(两轮 apply + 人裁 SQL,全零错误)**:raw 行 sexual bgm 0→20,231、dlsite 0→18,051;词表
  sexual 371→458、hidden 9→62;人裁 236 条(系列名 12 保留[东方Project 55 部等]、低置信 sexual 195 全
  裁、mapped junk 15 裁、降旗 10 词[正太/石化/祖母/蔷薇向/裤袜/受·攻主人公/精神崩溃/顺从的主人公/男性受]
  +raw 残旗清 1,986 行、Free! 复位);catalog_tags 索引重投 1,772。验收:kungal gid 6 实测 14 tag 入
  sexual 类目(数据修好即时生效,未等部署)。
- **教训**:tagcanon 旧映射会放大 junk 裁定(FREE[免费]→Free![系列]误藏一例,SQL 复位);工具设计上
  降旗只进人审不自动,是对的。剩件:低置信 unmapped junk ~2,700 无写入意义未裁(不显示);模型回显全半角
  归一致 1 名(姐控？！)永判不了,放行。
