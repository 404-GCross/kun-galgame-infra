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

## pi 值守状态(就地更新,一行一钩子)

- (待 pi 填;接手第一件=核 T1 浸泡态+CI 门是否已绿)
