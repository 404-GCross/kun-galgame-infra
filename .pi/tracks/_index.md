# pi 值守分轨索引(2026-07-23 交接)

> 复刻 Claude Code 时代的多会话架构:**一轨 = 一个 pi agent = 一个状态文件**。
> 规则:①你只认领用户指派给你的那一轨,**其他轨的文件与代码域勿碰**;②本目录你的轨文件由你
> **就地更新**(台账行风格:一行=一个状态钩子,细节写短段;**不是流水日记**——流水在 git log 里);
> ③每轨沿用既有 commit scope 前缀,`git log --grep` 即可按轨回放;④push 永远归用户,
> 任何"暂 hold"必须开独立分支(共享 main 上别人一 push 会连带把你推上产,实爆过)。

| 轨 | 状态文件 | 底稿/状态源 | commit 前缀 | 活跃度 |
|---|---|---|---|---|
| 数据聚合(catalog 四源全字段) | `media-aggregation.md` | `refs/proj/`(台账=61) | `feat(catalog)` 等 | 🔥 有队列 |
| apps/web 管理控制台重构 | `web-admin-console.md` | 本文件+git | `feat(web)`/`fix(web)` | 🔥 有尾波 |
| catalog 数据 QA(只修错) | `catalog-data-qa.md` | `refs/qa/` | `fix(catalog-qa)` | 🌗 半休眠 |
| developer 门户/开放 API 面 | `developer-portal.md` | `refs/plans/09` + docs/developer-platform | `feat(developer)` | 🌗 待部署件 |
| 编辑引擎 | `editing-engine.md` | `refs/plans/08` | `feat(editing)` | 💤 触发式 |
| canonical-api(/v1 数据 API 重设计) | `canonical-api.md` | `refs/proj/106` | `feat(api-v1)` | 🔥 设计阶段 |
| 其余已收官轨(合并一览) | `dormant-tracks.md` | 各 memory 文件 | — | 💤 |

共同必读:`.pi/ONBOARDING.md`(生态地图+纪律+坑速查)。每轨文件里的「Claude memory 参考」
是该轨全部历史与教训的深水区,只读。

## 双模型审查资产(.pi/agents/,2026-07-24 新增)

执行默认 Fable(主模型,更强);GLM-5.2 只当独立审查者——不同模型族系盲区不重叠,交叉验收才有价值。

- **glm-reviewer**(交叉验收):实质波/迁移/契约改动的验收审。任务书必须给具体 artifact(commit/diff/SQL)+逐条 checklist,产出 per-item PASS/FAIL+证据。
- **glm-redteam**(对抗检验):不可逆动作前用——DDL/prod ops/「没有全部」宇宙断言/冻结面改动。需要具体靶子,产出带复现路径的反例。
- 派发:`subagent_spawn` + `agentScope: "project"`(在仓根目录的会话直接可用)。
- 红线:两者均只读(不改文件不写库);审查结论也要过你的验收;审查↔修改最多两轮,之后升级用户;分歧即上报。
