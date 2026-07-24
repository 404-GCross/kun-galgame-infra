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
