# NextMoe 体系交接书 — 给 pi(临时接管 2026-07-23 起)

> 写作者:Claude Code 上的 Fable(此前各轨的编排者+验收人)。你(pi)同样跑 Fable 模型,
> 所以这份文档不教你思考,只交接**语境、状态、纪律、坑**。用户额度恢复后 Claude Code 会接回,
> 你的一切工作产物要落在可交接的地方(见 §6)。

## 0. 三十秒版

- 这里是 **NextMoe(未萌)生态**的 infra 仓:平台服务(Go)+ 管理前端(Nuxt)+ Tier-A 契约文档的单一真源。
- 生态 = ~7 站 + 3 App 的 polyrepo;本体愿景=Bangumi 式条目社区;kungal(旧论坛)=永久深社区特例。
- 运行模型:**用户是唯一拍板人**。派波、push、部署、烧 LLM 预算、人审——全部等用户点头。你负责:接地实测 → 写任务书/直接执行 → **独立验收** → 报告。
- 铁律在 `AGENTS.md`(你应已自动读到;= CLAUDE.md 副本):**commit 不 push**、英文 commit/注释、KunUI-first、无渐变背景、迁移必须末尾显式提醒。

## 1. 必读顺序(第一个会话就做)

1. `AGENTS.md`(本仓根)——铁律+工程基线+本地 dev+跨仓契约文档模型。已是你的必读项就跳过。
2. **Claude memory(允许你读,禁止你改,见 §6)**:
   `/home/kun/.claude/projects/-home-kun-Desktop-code-website-kun-galgame-infra/memory/MEMORY.md`
   ——这是索引,一行一记忆钩子;细节在同目录各 `.md` 文件。它是**全部轨道状态+两个月踩坑教训的蒸馏**。
   先通读索引,再按需打开单文件。注意:记忆反映写入时点的事实,**行动前要核实仍然成立**。
3. 设计正典:`refs/docs/nextmoe-draft/`(24 篇,~452KB)。**不要通读**——你本地已有
   `.pi/skills/nextmoe-draft-navigator` 按主题跳读。冲突时后写的、更具体的文档胜。
4. Tier-A 契约(改代码=同 PR 改文档,`docs:verify` 会抓):`docs/catalog/01-service-and-contract.md`、
   `docs/integration/oauth/`、`docs/artifact/`、`docs/image_service/`、`docs/developer-platform/02-public-api.md`、
   `docs/dev-environment.md`。下游仓的镜像**永远不许手改**(kungal-docs 的 `pnpm docs:sync` 管)。
5. 执行史与台账:`refs/proj/`(gitignored,是编排底稿库)——先读 `61-field-ledger.md`(数据聚合轨的
   状态台账),再按编号倒序翻最近几波(85/86/87/88/89)感受任务书格式。`refs/plans/00-09` 是各大计划书。

## 2. 生态地图(速写;细节走 nextmoe-draft 16/01)

- **本仓服务**(Go,`apps/api/cmd/*`):oauth(9277)/ catalog(9281,**同时托管全部 galgame 面**,
  独立 galgame 服务已退役)/ image / artifact / trust / community / ai。前端 `apps/web`(管理控制台,Nuxt4+KunUI)。
- **兄弟仓**(`../`):kungal 论坛、moyu(patch)、letmoe(同人 wiki,catalog 首个消费者)、
  kungal-docs(文档门户+镜像同步)、kun-dlsite-api / kun-erogamespace-api(上游 staging 爬虫,独立项目)。
  仓名以 `ls ../` + nextmoe-draft doc 16 为准,勿凭记忆猜。
- **数据库**:宿主 PG18 @5432 = 共享 dev 真源,**但 refresh-dev-db 管辖的库名会被周期刷成 prod 断面**
  (core 8 + dlsite/erogamespace)——**本地独有成果严禁写这些库名**。dlsite 镜像+cien 爬取的权威持久库 =
  容器 `:55432`(user=dlsite)。`kun_catalog_rehearsal` = 生产演练副本(写演练在这);
  **本地 `kun_catalog` 对你只读**。密码取法:`PW=$(awk -F: '$1=="localhost" && $4=="postgres" {print $5}' ~/.pgpass)`,
  **绝不把密码/key/DSN 回显进会话输出**。
- **生产**:SSH `kungal-neo`(sudo docker;Dokploy stack `kun-visual-novel-infra-vqvqbc-*`)。
  一次性命令=静态二进制 scp + busybox 借 postgres 容器 netns + env-file(用后 `shred -u`)。
  配方细节在 memory `prod-ops-access` / `deploy-migration-gap`。**prod 操作只在用户明示时做。**

## 3. 台面快照(2026-07-23 交接时点;之后以 refs/proj 台账为准)

- **未 push**:infra main **8 枚**(数据轨 4:`8a138cf`/`0fb76b9`/`d751fa0`/`f04357e`;developer 轨 4:
  `35d569e`/`98b1d6a`/`3023324`/`ba6312e`)+ kun-dlsite-api 一批(含 `adc5c27`)。用户攒批推。
- **零待跑迁移**。下次部署自然揭示两件已收账的读面:70c claimed bgm tags(doc 88)+ E2c label
  intros[]/links[](doc 89)。
- **数据聚合轨**(memory `media-aggregation-track`,台账 refs/proj/61):
  - 85 Ci-en 爬取 ⏸ **等专用 JP 出口**(用户采购件;续跑配方 doc 85 ⑨,拿到出口后数小时补完 28,573)。
  - 86 cien→label 投影:**parked**,85 爬完一炮打齐(fill-missing 幂等,别提前跑)。
  - 队列推荐下一波 = **新名配对补波**(8,593 个词表外 bgm tag 名;复用 70b 的 tagcanon 管线+风险分割人审
    配方,见 doc 87)——**等用户说开才开**。层级波(2,858 关系留档)排其后,需先设计。
  - 用户拍板项(列着别催):T2b popularity claimed 暴露 / REL2 / D 族解冻;人物身份解析冻结勿碰。
- **其他轨一句话**(细节各自 memory 文件):developer 门户待推批次二(含 workflows 文件→需 SSH push);
  open-api 轨剩三把 internal key 轮换待裁决+第三方开放;catalog QA 轨剩 134 人审(用户件);
  编辑引擎剩 E4+触发式。

## 4. 工作纪律(这套文化是两个月血泪换的,请原样继承)

1. **任务书驱动**:每个实质波先在 `refs/proj/NN-*.md` 落任务书——供给侧事实**亲自实测**(不引传闻数字)、
   裁定逐条拍死、纪律段、验收标准;完成后报告写回同文档,闭环记录追加在尾部。refs/proj **永不入 commit**。
   编号已用到 89,**新波从 90 起**(refs/proj 只属数据聚合轨;别的轨见 §6 各自底稿)。
2. **验收文化(最重要)**:不信任何断言——不信执行产物、不信下游报告、**不信自己的第一次查询**。
   独立重算、对库逐字核样本、dry→apply→**二遍零写**、部署类操作先打 PRE 基线。声称"没有/全部"之前
   grep 所有兄弟仓(memory `verify-universal-claims-across-all-repos`)。
3. **证据纪律**:Go 测试必须 `TEST_DATABASE_DSN` 显式给且 **host=localhost**(127.0.0.1 匹配不上 .pgpass
   → 整包静默跳过 0.00Xs 假绿,已三次踩);`-count=1 -p 1 -v`,报告写真实 PASS 数+耗时旁证(真跑 ≥0.5s/包)。
   IDE/LSP 诊断常是陈旧噪音(已四犯),**以 fresh `go build ./...` 为准**。
4. **提交纪律**:并行轨共享工作树——**永远 `git commit -- <显式路径>`,绝不 `add -A`**;提交前审
   `git diff --cached --name-only`(gitignore 反选曾差点泄 .env)。
5. **秘密纪律**:永不 `source` 生产 secrets env(连字符名报错整行回显,07-23 刚泄过三把 key);
   取值走服务器侧 grep+cut;诊断永不回显 env;**image 服务的 galgame_wiki key 永远不碰**。
6. **高频坑速查**(细节在 memory 同名文件):`rg -r` 是 replace 不是 recursive(六犯!);
   catalog 常量 **EntityTypeWork=5**、EntityTypeLabel=3、LinkKind exact/probable/related=0/1/2;
   GORM 四坑(default 标签吃零值/连续大写蛇形化/复合索引 priority/identity PK 三段标签);
   Huma 两坑(指针 query panic/匿名内嵌不展开静默丢字段);PG bind 参数 65,535 上限→1 万分片;
   advisory lock 全实例单键空间;openapi spec 动刀查三 CI 门(spec-breaking/test/openapi-types 的
   paths 清单);Dokploy redeploy 常不重拉 :latest(no-pull gap)+ 共享网同名服务 DNS 轮询;
   `.github/workflows` 变更 push 需 SSH;私有仓 GH Actions 分钟计费→攒批 push。
7. **对用户**:中文交流;报告先结论后细节、真实数字;方案给推荐而非选项综述;凡"之后每次都要 X"类
   请求属 harness 自动化,不要口头答应。

## 5. 前端专项(若动 apps/web)

KunUI(`@kungal/ui-*`)优先且**不许改 KunUI 源码**(有 bug 报给用户);颜色只用项目自定义色板
(`text-foreground`/`text-default-500`/semantic 50-950,无 `dark:` 前缀);全部箭头函数;
pages/ 只放路由定义,业务组件进 `components/<route>/` 且文件名不重复目录前缀;
Nuxt 页面单一真实根元素;`scrollbar-gutter: stable` 全局已设勿破坏。KunModal 宽度归 size prop。

## 6. 分轨值守与交接回来的约定(重要)

多个 pi agent 并行时,**复刻 Claude 时代的多会话架构**——一轨一 agent 一状态文件:

- **`.pi/tracks/_index.md`** = 分轨索引(哪轨归谁、状态文件、commit 前缀、活跃度)。用户指派你哪一轨,
  你就只认领 `.pi/tracks/<你的轨>.md`,**其他轨的状态文件与代码域勿碰**(历史上有会话从 git status
  误判自己是别的轨——先读你的轨文件确认边界)。
- **状态记录 = 三层,不写流水日记**:①git 即流水(路径域提交+沿用你轨的 scope 前缀,
  `git log --grep` 可按轨回放);②实质波的过程与验收写进该轨**既有底稿**(数据聚合=refs/proj 编号
  文档+台账 61;QA=refs/qa;developer=refs/plans/09;等——那是共享底稿,按既有格式追加即可);
  ③你的轨文件里只维护「值守状态」节——**就地更新的台账行**(一行=一个状态钩子),Claude 回接时
  读它+git log 即可无缝续上,几百次提交也不怕。
- **Claude memory 目录:只读**。不要编辑那些文件(格式、口径、链接体系是 Claude 侧的;写坏会污染回接)。
- 跨轨协调:共享工作树,**任何"暂 hold"的改动必须开独立分支**(共享 main 上任一 agent 的 push 会连带
  推走所有人的 commit,实爆过);共享入口文件撞车参考 memory `shared-worktree-path-scoped-commit`
  的 checkout-HEAD 隔离配方。
- 结束每个会话前:确认工作树干净(该 commit 的 commit)、没有你启动的野后台进程、没有 secrets 残留文件、
  你的轨文件「值守状态」节已更新。

## 7. 给同为 Fable 的你的最后几句

你会想跳过接地直接动手——别。这个体系里几乎每个"显然"都埋过反例(127.0.0.1≠localhost、
-r≠recursive、"S2S 有 label 端点"=假、"5432 是安全的家"=会被周刷)。**先实测,再裁定,后动手;
交付前用与实现不同的路径独立验证一遍**——这是这两个月所有波零回滚的全部秘密。遇到真正的
scope 决策(建不建新表、烧不烧预算、动不动冻结面)停下来问用户,其余自主推进。祝顺利。
