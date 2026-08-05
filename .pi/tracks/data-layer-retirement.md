# 数据层退役轨(data-layer retirement)— pi 值守文件(2026-07-31 二次交接新建)

**使命**:galgame 表家族从 kun_catalog 彻底消失——写读全部翻到 catalog 原生表,最终 DROP。
**Claude memory 参考(只读)**:`data-layer-retirement-track.md`(全史,最详);计划书=`refs/plans/10-data-layer-retirement/`;波底稿=refs/proj/ doc 140/146/154-164 系列。
**commit 前缀**:`feat(catalog)` wave-16x 系(注意与聚合轨撞前缀,按 wave 号区分;**已发生一次波号撞车:退役轨 a03338ec 与聚合轨各有一个 "wave-164"**)。

## 二次交接快照(2026-07-31,Claude Fable 留)

- **W 窗执行完毕 07-31 ~10:40Z**:rekey 12,602、resite 64,515(site `galgame_wiki`→`kungal`)+等量事件、四下游容器翻窗、值日链剪 i,j,k,l(**任务书说剪但 neo `/root/reindex-catalog/run.sh` 现场未剪,仍在跑**)、冒烟五门绿(87ccd48a)。
- ~~**当前段位:T1 48h 浸泡中** → T2 终扫 m,n,o+dump → **T3 DROP 前对账交用户** → DROP(149 SQL)→ T4 清尾。**DROP 是不可逆动作,必须用户明示。**~~
  **↳ 已过时:T1-T3 全走完,DROP 已于 2026-08-04 经用户令执行**(27 表全灭,终档 /root/wave149/ 恢复验证过;详见 Claude memory 轨文件 08-04 两条)。剩 T4 清尾,见下方 08-05 状态行。
- **CI test 门红**:P5 撤夹具供给致五包跨包污染,交接时点 Opus 修复中——接手先看 main CI 最新态,勿重复修。
- **⚠️ 165 回归教训(07-31 当日,聚合轨代修)**:resite 改值漏改七处读者(reindex-catalog claimed 桥×3/intromt/dlsitemedia/galgametouch/olangfix/bidaudit/merge-work-dups),reindex 把 64,515 部 claimed 作品搜索文档投空→全站中文搜索事故。修复=聚合轨 commit `049eb9b5`(分支 `w165-resite-readers`,未 push,**push 是防复发件**);prod 已手工重投恢复+值日链坏二进制已重铺。底稿=`refs/proj/165-resite-reader-sweep.md`。**本轨接手后:改值波验收门必须 `git grep 旧值` 全仓零残留。**
- 库约定(07-31 跨轨定约):**kun_catalog_rehearsal 归聚合轨专用**;本轨演练用 `kun_catalog_w<波号>` 私库用毕即弃;测试库按轨分名。
- N 系编辑面本体化(N1-N5,doc 154-164)全收官已推;W5-3 铸造端点已交付(162)。

## 边界(勿碰)

- 聚合轨在飞件(refs/proj/150 人物身份解析 program、164/165 波产物)勿碰;`kun_catalog_rehearsal` 勿重置(二犯已定约)。
- 共享工作树交接时点在分支 `w161-hotfix`——**勿switch 别轨所在分支**;自己的波开独立 worktree 基底显式 origin/main。

## ⚠️ P5 摘面漏了下游 — 2026-08-03 事故与修复

P5 摘 `/internal/*` + `/api/*` staff 面时,149 STOP-1 要求的「先盘下游依赖并给迁移期」只覆盖了 forum 的
`client.go`,**五条 lane 漏网**(实测全部 404):`/internal/edit/*`(编辑面,全站无法编辑游戏
**2026-07-31 10:25:48Z 起约 67 小时**)、`/internal/galgame/meta`(属主判定,15.9 万次/天)、
`/internal/galgame/taxonomy/*/search`(投稿标签选择器)、`/internal/galgame/messages/mine`、
`/api/admin/{stats,galgame/messages}`。

前两条已修(forum `77289497` + `3ca9beda`:编辑链归一 S2S、属主改读本地 066 冻结列);
**后三条待产品裁定替代面**,见值守状态行。

放大器是日志缺陷:未匹配路由的 404 被记成 200,所以 16 万次/天的失败在日志里长成 200。已修
(`ae09dc30`,7 个服务共用)。**连带作废一条方法论**:07 §78 用「访问日志 path 统计」证明「A 桶流量归零」
——该方法在缺陷修复前分不清「面服务了」和「面没了」,以后摘面别再单独依赖它。

## pi 值守状态(就地更新,一行一钩子)

- (待 pi 填;接手第一件=核 T1 浸泡态+CI 门是否已绿)
- **🏁 2026-08-04 P5 漏网全清(wave 169,Fable 亲执,用户全权委托裁定;底稿=refs/proj/169)**:
  ①标签选择器已由 f7c3fb74(08-03)换源 catalog;②wiki 通知页=撤页(FE+代理+read-state 全链删,
  表留历史);③管理台=摘 7 个 wiki 指标不造对等物;④**盘出第五条断裂:图片上传**(POST
  /image/galgame→死面,编辑能改字段不能传图)——infra 新造 catalog 编辑面字节上传腿
  `POST /api/v1/catalog/edit/images`(27b451c1,字节落 site=catalog 随 refping 保活)+forum
  换轨(157d7abb/51715b11,净删 ~3,900 行:13 写代理/词表控制台/浏览页建改删 modal/wiki 通知链/
  census 测试随双 base 字段消亡=编译期保证)。部署顺序 infra 先于 forum。
- **🩹 169-E 部署后热修 f3a473aa(08-04 当日)**:冒烟发现 169-D 上传腿错挂 `cfg.ImageClient`
  =prod 的 galgame-wiki-admin 身份(字节落 wiki scope、catalog refping 够不着=66k GC 险情同类)。
  修=改用 `cfg.CatalogImageClient`(compose 透传既有栈级 env,无新 secret)+handler 内
  preset 映射 galgame_*→catalog_{cover,screenshot}(变体同构);**原「prod SQL 加 preset」
  前置整条取消**。底稿=refs/proj/169 §6;待再 push 部署。
- **🩹 作者章小修 2f4b0b9a(forum 仓,08-04)**:claim cron 建 stub 从不写 creator_user_id
  (列原为 wiki 冻结快照)→ 切轨后新词条全部无作者章(207 行在产累积)。修=cron 归属申领人
  (approval 路读 +3 同款 submitter memo,绝不误记 reviewer;owner-publish/born-live 记事件
  actor)+`SetCreatorIfUnset` 随建 stub 事务写一次、永不覆写。存量 prod 已回填 133/208
  (claim_event 首事件 actor);余 75 行 claim 早于事件流(切轨系统回填 actor=0)按设计保持
  无章。语义更新:creator=「wiki 提交者或领养申领人」快照。待 forum push 部署。
  ↳ 部署后复跑(08-04 晚):窗口期新增 6 行无章,1 行补章(228620→44632,claim 首事件真人
  actor)、5 行 resite actor=0 归设计桶;9682 核明=首事件 to_state=0 带 actor 16285 非申领
  (wiki 快照提交者实为 uid 1),按「非申领事件不落章」不动。终态:无章 75 行全为设计内。
- **🏁 170 封面补全波(08-04 当日,双 Opus 派发)**:169 坏窗口期 72 部无封面词条,`cmd/backfill-vndb-covers`
  (15e4e666,vndbcovers 机架仿 bangumicovers/dlsitemedia,exact 锚 only+NOT EXISTS 幂等门+quota 停机)
  prod 跑毕 49/72 补齐(47 现锚+2 新铸锚 v67584/v67585;v67582 为 VNDB 无图 stub)。检索波核出:
  **5 部为现有词条重复投稿**(228616/625/628/629/634→已锚老作品,三部还在 kungal 草稿)+3 对同 id 重复
  (228618/21、228620/26、228623/24)=8 案待用户裁并;3 部 Steam-only(228631/632/633)+1 特典盘
  (228627)无 VNDB 身份;分级 2 处(228625/629→R18)与 olang 2 处(228617→ko/228632→en)已当场治数。
- **🏁 170b 重复词条清理(08-04 当日,用户令)**:12 合并案全走 propose→approve→execute 协议
  (`cmd/merge-dup-submissions` 显式对驱动;冷却窗按用户令 SQL 提前放行,proposal 61266-61277)。
  方向=「live 在产侧存活」:6 老草稿并入新 live 词条/3 新 dup 并入老 live 词条/3 新新互并;forum 孤儿
  stub 删 4(互动近零)。**发现 merge 机架缺口:work 合并不 rehang catalog_work_cover(表晚于 rehang
  清单)**——12 行封面手动搬运归位;缺口待修(候选小波)。终账:72 部无封面→55 covered+6 并殁+11 残
  (5 源头无图/1 VNDB stub/1 freem/3 Steam-only/1 特典盘)。三个 draft 目标(228618/620/625)=投稿人
  自撤,非损伤。
- **🏁 作者章二次事故当日闭环(08-05,Fable 亲执,纯 prod 治数零代码)**:用户报新词条又无认领人。
  定性两半:①cron 建行无章批(15520/13926/12032/10865)=publish 早于 2f4b0b9a 部署(12:12Z),
  修复本身在役无缺陷(16:05Z 起同形事件全落章);**且 08-04 晚复跑曾把这批误归设计桶(按首事件
  actor=0 判,漏看后续真人 publish 事件)——复跑规则失准自认**。②零散时间裸行=交互泳道
  (评分/资源/评论 EnsureLocalStub 防御建行)遇「live 无 forum 行」词条,蓄水池=2,154 部 live
  词条无 stub,cron 游标已过其事件故永不落章。治法(一事务):catalog 抽 10,987 live 词条
  (gid/created_at/最早非 approval 真人 live 事件 actor)→ 补建 stub 2,154(时间戳=catalog
  created_at 防洪泛;默认列表本就滤无资源词条,total 7,527 不变)+ 落章 2,176(新建+存量 25
  统一覆盖;9682 维持 08-04 裁定排除)。终态:stub 11,014、无章 61(可恢复者仅剩 9682=有意)、
  蓄水池 0=裸 stub 泳道干涸,cron 逻辑未动。抽查 15520→112505/13926→28089 等全对,
  公网详情+作者章渲染绿。
- **🏁 T4 现役件清尾(08-05,Fable 亲执)**:①**DROP 后孤儿终审**:149 号 27 表名单对编译面
  (非测试 Go)逐名 word-boundary 扫零命中;两 enrich CLI 已被 f89fd8e6 摘除、build-tag-canonical/
  tag-canon-pair 已 repoint(过时头注释本波修正);`platform/galgame/perm` **不是孤儿=有意保留的
  负控夹具**(registry_test 钉「退役词汇不进控制台」+roles_union_test 用它测并集机制),勿删。
  ②prod meili 三个 wiki 索引(galgames/galgame_tags/galgame_officials,4 天零写入)全仓扫零读者
  (命中全为 json 字段名)后删除,现存=五 catalog_*+letmoe_search。③服务器清理:wave140/146/161
  的工具二进制+**全部密钥件**(app.env/dsn-*.txt/env.tmp)已删,dump/记录件保留;/root/wave149
  终档不动(30 天留期至 ~09-03)。④forum/patch docs/CONVENTIONS.md:14 镜像清单 galgame_wiki→
  artifact(两仓各一行,path-scoped commit 候 push)。**T4 剩余(时间闸/别轨)**:
  kun_galgame_wiki_retired_w1 DROP(>08-15)、/root/wave149 清理(>09-03)、canonical 转告结账
  (spec locale 描述/摘牌口径纠偏,归 canonical 轨)。
- **🏁 170c merge 机架 rehang 全量对账波(08-04 晚,Opus 执行+Fable 验收)**:170b 缺口根治。
  e229b2ed:rehang 清单抽出为 merge_rehang.go(363 行),work 批新收 12 facet 表(intro/cover/
  screenshot/rating/tag/popularity/playtime/platform/engine/series_member/label/character 折叠版)
  +label/person intro+entity_relation;排除项(revision/claim_event 史表、external_ref 等专步、
  match 队列、product_work_id)逐个具名注释=「清单即契约」。测试=一次性容器库 3 新用例真跑
  PASS(负控核实非空转)。**prod 对账治数**:9 表 453 行历史悬空(tag 269/popularity 72/intro 24/
  character 23/rating 19/label 14/screenshot 14/platform 9/playtime 7/label_intro 2;cover 0=
  170b 手搬即全量)→ 同款语义一事务治毕(移 373/冲突删 80/roster 折叠 0),21 幸存 work 补
  touch 喂 changes,复跑对账 15 表全零。无 schema 变更、无 migration。
