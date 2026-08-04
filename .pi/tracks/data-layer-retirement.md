# 数据层退役轨(data-layer retirement)— pi 值守文件(2026-07-31 二次交接新建)

**使命**:galgame 表家族从 kun_catalog 彻底消失——写读全部翻到 catalog 原生表,最终 DROP。
**Claude memory 参考(只读)**:`data-layer-retirement-track.md`(全史,最详);计划书=`refs/plans/10-data-layer-retirement/`;波底稿=refs/proj/ doc 140/146/154-164 系列。
**commit 前缀**:`feat(catalog)` wave-16x 系(注意与聚合轨撞前缀,按 wave 号区分;**已发生一次波号撞车:退役轨 a03338ec 与聚合轨各有一个 "wave-164"**)。

## 二次交接快照(2026-07-31,Claude Fable 留)

- **W 窗执行完毕 07-31 ~10:40Z**:rekey 12,602、resite 64,515(site `galgame_wiki`→`kungal`)+等量事件、四下游容器翻窗、值日链剪 i,j,k,l(**任务书说剪但 neo `/root/reindex-catalog/run.sh` 现场未剪,仍在跑**)、冒烟五门绿(87ccd48a)。
- **当前段位:T1 48h 浸泡中** → T2 终扫 m,n,o+dump → **T3 DROP 前对账交用户** → DROP(149 SQL)→ T4 清尾。**DROP 是不可逆动作,必须用户明示。**
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
