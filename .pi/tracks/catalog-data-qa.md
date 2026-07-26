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
- 2026-07-26 **agent 波完成**:8+1 分诊 agent(批 2 双跑一致性完美)→110 条裁定 = 103 STILL_DEFERRED / 2 REPOINT / 5 MERGE_DUP;GLM-5.2 复核 6 PASS / 1 FAIL(3144 拒了,candidate 是 v6433 合集不是 v10620,退回 deferred)。**rehearsal 全过**:2 REPOINT(1629→493602、9183→215565)dry/apply/二遍零写+富集修复 2 alias+1 intro+2 meta+SQL 独立验证;额外揪出 9183 的 'ABANDONER THE SEVERED DREAMS' 污染 alias(来自错 subject infobox 别名,enrich 工具不覆盖),守卫 SQL=sql/21,rehearsal 已清净。4 MERGE_DUP 进 deferred-mergedup-4-wave2.tsv(累计 11 条交聚合轨)。产物:agent-wave-{verdicts-110,glm-review,live-scan-116}.tsv。**prod pending 用户批准:① repoint 2 ② 富集修复 ③ 9183 alias 清理 SQL**。
- **共享工作树事故 x3**:main.go 的 96 行 promote 代码被别的会话反复 revert(prod 二进制曾因此缺 flag,零写无损),轨文件状态行同样被打回;均 `git restore --source=HEAD` 恢复。教训=从共享树出二进制前要验证产物的 flag 而不是只看源码;轨文件以 git 历史为真源。
- 134 人审 = 用户件,列着未催。undermerge 两张 worklist = 聚合轨输入,未动。
