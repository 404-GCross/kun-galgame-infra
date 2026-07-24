# 编辑引擎轨 — pi 值守文件(💤 触发式,无活跃队列)

**使命**:统一 edit_proposal 编辑引擎(doc 21;maintainer-edit 一等公民)。E0-E3b+tail 全生产 LIVE,W4(apps/wiki 之死)2026-07-17 达成。
**Claude memory 参考(只读)**:`editing-engine-track.md`(全史+并发锁/OnMerge 钩子/契约文档教训)。**底稿**:`refs/plans/08-editing-engine/`。

## 剩余(全部触发式,用户点名才做)

- **E4 = /v1 第三方写面**(非退役必需,归属届时另议)。
- 退役尾:infra 孤儿路由(Create/Update/links/aliases 写面)、旧表 galgame_pr/galgame_revision drop(留期,前置=adminRepo 计数已亡)、galgame-wiki-admin redirect URI 清理。
- owner-merge 情境授权拍板(roles 通道表达不了)——用户件。

## 动这里前必知的三件事

- 修订写者**读前**必须 `pg_advisory_xact_lock(0x65646974, hashtext(type:id))`(双并发合并可双赢的真洞,CI 有并发测试);锁序 proposal→entity。
- 单一写路径副作用全走 OnMerge 钩子(post-commit best-effort);galgame OnMerge=Meili reindex+contributor;**contributor 是权威表不是投影**(58% 归属早于修订日志纪元)。
- 退役路由要删两处:runtime mount + spec 生成器(SetupXxxReadSpec),否则发布 spec 宣告死路由。

## pi 值守状态(就地更新)

- (待 pi 填)
