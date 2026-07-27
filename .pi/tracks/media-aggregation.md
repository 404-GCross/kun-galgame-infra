# 数据聚合轨(media aggregation)— pi 值守文件

**使命**:四源(vndb/bangumi/dlsite/erogamespace)全字段聚合进 catalog 注册表,「NextMoe 即真理」。
**Claude memory 参考(只读)**:`media-aggregation-track.md` + `multi-source-execution-workflow.md` + `upstream-source-staging-dbs.md`。
**底稿**:`refs/proj/`(gitignored)。**台账 = `refs/proj/61-field-ledger.md`**(每波闭环必须回写)。任务书格式照抄最近几波(87/88/89)。**新波编号从 90 起。**

## 边界(勿碰)

- 编辑引擎(`internal/platform/editing/`)、apps/web、refs/qa/ 是别的轨。
- 人物身份解析**冻结**;catalog 词汇红线(tags 绝不入 catalog_label);image 服务 galgame_wiki key 永不碰。
- 本地 `kun_catalog` 只读;写演练在 `kun_catalog_rehearsal`;dlsite/cien 数据只在 `:55432`。

## 交接快照(2026-07-23,Claude Fable 留)

- **85 Ci-en 爬取 ⏸**:等专用 JP 出口(用户采购件)。续跑配方 doc 85 ⑨;拿到出口后数小时补完 28,573。**别用美国 IP/共享代理硬爬(WAF 实证两轮)。**
- **86 cien→label 投影:parked**,85 爬完一炮打齐(fill-missing 幂等,提前跑=白付 ops)。
- **推荐下一波 = 新名配对补波**(8,593 词表外 bgm tag 名;复用 doc 87 的 tagcanon 管线+风险分割人审配方)——**等用户拍板才开**(烧 LLM+要人审)。
- 其后:tag 层级波(2,858 关系留档,先设计)/ 用户拍板项 T2b·REL2·D 族。
- 70 家族+E 族全关账;零待跑迁移;下次部署揭示 70c+E2c 读面。
- 生产幂等重跑二进制备份在 neo `/root/wave83|87|88`。

## 纪律要点(本轨特有)

- 波节奏:任务书(供给侧**亲测**数字+裁定拍死)→ 执行 → **独立验收**(对库逐字核样本、dry→apply→二遍零写)→ 闭环记录+台账回写。
- prod apply 用 busybox+postgres netns+env-file(shred)配方,只在用户明示时跑。
- 常量:EntityTypeWork=**5**/Label=3;LinkKind 0/1/2。测试 DSN host=**localhost**。

## pi 值守状态(就地更新,一行一钩子)

- 2026-07-24 pi 接管本轨。台面核实:交接书所列 infra 8 枚已全部入 origin/main(现未推仅 8927eda;dlsite-api 5 枚原样);85⏸/86 parked 无变化;下一波=新名配对补波(doc 87 配方)待用户令。
- 2026-07-25 90 任务书 DRAFT 完成(`refs/proj/90-tag-newname-pairing.md`)。供给侧本地只读实测(实测件在 `refs/proj/data/90-measure/`):联合词表重建 214,331 行=prod 逐位吻合;池 9,512(claimed 8,903 对账闭合,差 191=70b map 增量);真·新单源仅 50(全跨线名 100-380);claimed-only 4,528 名用量全≤23=长尾;blocking 估算 prod 口径 ~2.9k 对,--prior 可省旧对重判。
- 2026-07-26 90 波 P1 完成:commit `72c5652`(--prior 跳过[fail-fast]+单源 core 用量闸 1000+3 单测);包 16/16 PASS 真跑;rehearsal 全链二遍零写 + --prior E2E(重生成 1,639 对全部跳过)。glm-reviewer 两次派发均 600s 超时(纯工具超时)→按两轮纪律升级用户。⚠️ 工作树发现 `.env.example` 外部删除 + 本轨文件状态行两次被外部回退(疑编辑器缓冲),均非本轨所为。
- 2026-07-26 90 波 P2-P4 全链生产完结(用户批准后执行)。P2:mock 预检零预算核带(blocking 2,140/skipped_prior 1,103/待判 1,139)→真批 glm-5.2 ~74min 实耗 ~1,290 次(errors 4.8%+mop-up 后净损 0.5%);P3:64 exact 全审批 57 拒 7(高达=Gundam 等假朋友)+100 单源全审批 97 拒 3(Otomate/御苑生メイ/竹子社=归属实体渗入,全审拦截生效);P4:apply +150 tag/+207 map/errors 0/二遍零写,终态 catalog_tag 1,772/map 2,293,tier×kind 六桶与逐行预测吻合,15 行对库逐字核,拒名零渗入。台账 61 已回写;重跑件留 neo /root/wave90。90 波关账。tag 面剩触发式:层级波(留档累计 ~3,800)/T2b。
- 2026-07-26 用户裁定:91 刷新波推迟,前置=Ci-en 能 24h 内爬完(即 JP 出口到位)再议。顺手完成台账 61 对账:六行陈旧表行翻新(62/64/65/66/71/75 闭环回填)+新增「时长/playtime」行(审计发现从未上账;EG raw 有 total_play_time_median 实测在,vndb length 未 stage)。88/89 读面已随 07-25 部署 LIVE(镜像 11:53Z 构建实证);55432 容器已恢复在线。
- 2026-07-26 排期定(用户批全量清队+时长 demand-pull):91=EG 收尾+时长(playtime 新窄表+dmm 源行两个 schema 件待批;EG shoukai 实测=URL 非散文,负知识关账)→92=角色 traits(3,327 词表+297 万链已 stage)→93=系列/译本(设计稿波)→94=别名(依赖 91 的 vn 重 stage)→95=平台词表(最低优)。全五波零 LLM 零人审。
- 2026-07-26 91 波(时长+EG 收尾)当日全链生产完结:commit `21a1702`(playtime facet+读面 playtimes[]+steam/dmm refs+vn 全列重 stage);prod 终态 catalog_work_playtime 29,476(eg 13,233×60 换算/vndb 16,243)、steam 1,017+dmm 14,014 probable refs(dmm=新源行 15)、vn 64,881 行×13 列;全程 dry=apply 计划相等+二遍零写+10 行逐字抽核(eg 5 行对镜像/vndb 5 行 inline)全中;重跑件留 /root/wave91。读面随下次 push 部署对外。⚠️ 90 波四文件开工前又遭外部回退(第三次),已恢复。下一波=92 角色 traits(任务书起草中)。
- 2026-07-26 插队小 PR 任务书 DRAFT 完成:92 = 公开 /v1/galgame/{gid} 读面加 refs.dlsite(workno;forum 联盟链接消费者)。供给侧亲测:prod galgame_dlsite_meta 8,418 行全非空 workno(全量覆盖 13.4%;forum 的 43.8% 系其自家分母)。裁定:落位 refs.dlsite、null 语义沿 PublicRefs 惯例、batch 路径同步接线、attribution 不扩。⚠️ ln/ln_id/galgame_ln_meta 文档债仓内全域 grep 零命中=债不存在或已清,零改动。编号:粘贴文本的"91"已被时长波占用,本波=92,队列顺移(traits→93/系列→94/别名→95/平台→96)。等用户批准后执行。
- 2026-07-26 92 波(公开 /v1/galgame/{gid} 读面 refs.dlsite)代码侧当日完结:commit `7dc9a70`(7 文件 +51/-2:ScoreMeta 第四窄表+PublicRefs.Dlsite 无 omitempty+publicRefs 第四支+frozen-shape/null 双测试+三处测试建表清单补表);batch 委托自动继承,list 面不动;spec 再生 +5 行幂等自证;galgame 三包回归全绿(W1b 的 Meili 401=预存环境缺 key,提 dev-meili master key 后绿)。前置已办:本地 migrate-catalog 补齐 catalog_work_playtime(91 代码/快照错位,用户指令)。部署后 curl 验收待下次 push。文档债子项零改动关闭(用户确认债在别仓)。
- 2026-07-26 93 波(角色 traits)任务书 DRAFT 完成待批 schema。供给侧亲测:词表 3,327(11 根组+3,316 子,账目闭合;sexual 685)/DAG 边 3,697/链 2,966,144(spoil 0/1/2=275万/10.5万/11.1万,lie 2,491,悬空 0);锚命中 2,903,130 行(97.9%)落 150,920 角色。设计:三新表(词表 vndb_tid UNIQUE 即 map[单源不建 map 表]/DAG 边/链表 290 万行分批导入)+S2S traits[] 读面(默认 spoiler≤0,公开面不动留用户拍)。等批准。
- 2026-07-26 93 波(角色 traits)当日全链生产完结:commit `defca48c`(16 文件:三新表+cmd/import-character-traits 三相导入+S2S traits[] 读面[默认 spoiler≤0/?spoilers 0-2]+契约/spec/TS 配对);rehearsal 76s 全量+二遍零写;prod 终态 vocab 3,327(sexual 685)/edges 3,697/links 2,897,264 落 150,920 角色,spoil 0/1/2=268万/10.3万/11.0万;**全量双向反 join 对账 missing=0/extra=0**(抽核升级为全量核);重跑件留 /root/wave93。读面随下次 push 部署对外。队列剩:94 系列/译本(设计稿波)→95 别名→96 平台词表。
- 2026-07-26 QA 轨收官移交两张单已验收登记进本轨队列:①merge-dup 11 例(deferred-mergedup-4-wave2.tsv 4 例+deferred-mergedup-7.tsv 7 例,work 级合并,ExecuteMerge+48h 冷却协议,执行需用户授权)②undermerge 两张(characters 1,080 组/642 works;credit_names 13,966 组/5,049 works——credit_name 是唯一硬删合并面,需独立设计波)。本轨对 QA 无新派发(90-93 波验收全零差异,无修错债)。
- 2026-07-26 94 设计稿完成待拍板(refs/proj/94):series 供给实测=592 个多员系列/2,078 works(两两边要 5,334 条,O(n²)),推荐实体化两新表(catalog_series+member,台账既定 REL2 方向);**译本=负知识关账**(みんなで翻訳不覆盖游戏域:children 62,699 全在漫画/语音,游戏 0);bgm series 布尔无分组键仅记录。97 任务书完成待放行(refs/proj/97):merge-dup 11 例走 Propose→Approve→48h 冷却→Execute 协议,新小工具 cmd/merge-work-dups 读 QA TSV 驱动 service 层,#8154 execute 前复核。
- 2026-07-26 94 波(系列实体化,选项 B)当日全链生产完结:commit `44f5a273`(13 文件:catalog_series+member 两新表/导入器[改名就地更+成员镜像同步+跌破门整删]/S2S series[] 读面/契约+spec+TS);rehearsal 与 prod 逐位一致(series 592/members 2,078/孤儿 0/二遍零写);⚠️ 语义注记:dlsite series 部分是 circle 题材文件夹(異種姦・触手 36 员为最大),source_id 归因在行,消费端按源理解。译本=负知识关账。台账 61 系列行翻绿。
- 2026-07-26 97 波(QA merge-dup 11 例)propose+approve 段完成:commit `da0dd54f`(cmd/merge-work-dups,全 service 层协议);rehearsal 快照过旧(QA 修复晚于克隆点)改合成案例全链验证四不变量全绿;prod 11 例 sanity 全过→proposal #36130-36140 已 approve,**execute due ≈ 2026-07-28 15:47Z**(届时 execute -run + #8154 复核后终态对账)。
- 2026-07-26 98 设计稿完成待批(refs/proj/98):**characters 侧 98.5% 假阳性**——1,080 组过 49 波同源分裂守卫仅 16 组可合并(抽核证实:岡部×2=两个不同 vndb 角色 id,变体非欠合并;1,064 组=负知识,变体折叠=未来读面产品决策);credit_names 真实人口=3,899 对(orphan↔orphan 2,957=50 波类扩展到全角色/mixed 942=新类[幸存者=挂 person 侧,导入残影去重非身份断言]/frozen 0=冻结面零碰撞)。若批:~3,915 对走 detect→抽样审计 30→propose+approve→48h→execute 协议。
- 2026-07-26 99 任务书 DRAFT 完成待批(refs/proj/99,插曲件:forum workno 校验批)。供给侧亲测:解析口径复现 prod 6,604 对(forum 的 5,074=子集,等其 TSV 取交集);交叉校验复现 agree 4,541/disagree 174/no_refs 1,889;镜像存在性 RJ 全覆盖、缺 417=416 个 VJ-8 新 id 段(pro 域爬取缺口非死链,单列判定);标题面 100% 可用。裁定:相似度阈值 0.60+子串兜底(执行时直方图校准);三产出 verified/suspicious/refs-fixlist;refs 修正不在本批(fixlist 先报)。零 LLM。
- 2026-07-26 99 波(forum workno 校验)当日完结:三产出落 refs/proj/data/99/(verified 5,758/suspicious 846[low-sim 404+vj-gap 441+not-in-mirror 1]/refs-fixlist 机器 4→人审 0)。直方图双峰自证阈值 0.60(0.9+ 桶 5,660 vs 中段 99)。**头条:refs 侧零真错锚**——174 disagree=61 editions+97 link 侧可疑(meta 分更高,wiki 链接才是问题方)+8 ambiguous+4 版本变体假阳性;「refs 锚错」假设证伪,QA 三件套无需启动。遗留:forum TSV 到后交集复算。suspicious.tsv 待交 forum。
- 2026-07-26 98 波 P1+prod detect 完成,**量级 6× 停下待确认**:工具 commit `d29f1cb1`(orphan 全角色化[桶 (work,role,fold)]+mixed 新类[frozen 守卫]+waveTag98);rehearsal 金丝雀 8 对全链四不变量绿;prod detect=character 808/orphan 16,828 组 20,349 对/mixed 1,873 组 2,849 对/frozen 0,合计 ~24,010 对 vs 设计稿 3,915。根因查实:①fold 语义(去空格)宽于 QA 的 name_norm——72% bucket 是 vndb 带空格 vs bgm 无空格变体,QA 谓词看不见;②S1b(07-21)灌 55,058 孤儿名义,step-50(07-16)跑在其前从未见过这批债;③B1+cron 增长。抽样审计 28/28 零假阳。建议照量放行,execute 与 97 同窗(07-28)。
- 2026-07-26 98 波 propose+approve 全量完成(用户照量放行):24,010 对零错误(character 812 前台/mixed 2,849 前台/orphan 20,349 后台容器 ~13min);prod 独立核对 step-98 approved=24,010 逐位闭合(credit_name 23,198+character 812),execute_after 全落 07-28(最晚 17:11Z)。**07-28 ≥17:11Z 待办:97 execute(11 例+#8154 复核)+98 execute(canary→全量)同窗跑,终态对账回填两文档。**
- 2026-07-26 99 交集复算完结:forum TSV 回执逐项核验全过(5,077 行/前缀分布/双 workno gid/格式全 ✓,Opus 回执可信);**forum 5,077 对=我 prod 6,604 的严格子集(100% 包含,forum-only=0,环境差异担忧未成真)**;forum 口径产出 forum-verified.tsv 4,320(85.1%)+forum-suspicious.tsv 757(vj-gap 429/low-sim 328),对账闭合。99 波全部收官。两 TSV 待转交 forum。
- 2026-07-26 95 波(别名)当日全链生产完结:commit `eb899861`(cmd/import-work-aliases 双泳道+集成测试);prod 终态 kind=1 别名 0→6,545(bgm infobox,4,237 bodyless works)+kind=3 kana +223(B1 尾巴,残 57=镜像无 kana 终态);dry=apply 逐位对账+二遍零写+抽核 10 行全绿(bgm 5 行 IN-INFOBOX/kana 5 行片假名读音)。**vndb 泳道负知识关账**(62,220/62,222 vndb 锚在 claimed 上,bodyless 仅 2;排期表"vn.alias 18,005 已就位"预期作废)。台账 61 别名行翻绿。队列剩:96 平台词表(殿后)+07-28 execute 窗(97+98)。
- 2026-07-26 96 波(平台词表,队列殿后件)当日全链生产完结:commit `d24863dc`(catalog_platform registry 48 vndb 码+catalog_work_platform facet+cmd/import-work-platforms 双泳道+读面 releases[].platform/platforms 与 work platforms[]+契约/spec/TS)+`3c728ce1`(测试债:92 波 scores 夹具漏 GalgameDlsiteMeta 表,二分定位;另 rolegen sources() 14→15 并入 96 提交)。prod 终态:release platform 填充 159,718→**174,238**(dlsite game 域 +14,520 精确:win 全量/and 166/ios 19;smartphone/play=观看器标志跳过;ASMR 存根不动)+catalog_work_platform **14,242** 行落 11,851 部 bodyless bgm。全程 dry=apply 逐位+rehearsal/prod 各二遍零写+dlsite 全量 SQL 重算反 join 14,520/mismatch 0+bgm 抽 5 行逐字;bgm -10.8% 口径差根因查实=1,442 部平台字段空数组(SQL EXISTS 宽于导入器"产出≥1 串",prod 复算 11,851 逐位闭合)。staging=wave96_dl 临时 schema(用后 DROP);未映射 top20≈164 行留档=aliasMap 扩展饲料。复跑件 /root/wave96。**排期队列清空**,剩 07-28 ≥17:11Z execute 窗(97 十一例+#8154 复核;98 canary→24,010 全量)。
- 2026-07-27 用户 push 授权执行:20 笔全部上去(6675c4f0..78c702a3),5 个新读面(playtimes/refs.dlsite/traits/series/platforms)随下次部署揭晓。99 勘误批(forum 复盘反馈:4156/RJ088116 标题 1.00 过验但镜像 work_name 空):同口径复扫 verified 5,739 workno→命中 22(全 status=info_only=商品页从未抓到,下架/占位语义,非锚错);其中 13 条在 forum 集合内→勘误单 refs/proj/data/99/forum-errata.tsv 待转交,9 条仅我方宽集合无需勘误。方法论修正入 doc 99:下批 verified 谓词追加 work_name 非空存活性检查。
