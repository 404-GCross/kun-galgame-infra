# developer 门户 / 开放 API 面轨 — pi 值守文件

**使命**:developer.nextmoe.dev 门户(SSO/key/用量/文档)+ /dev/* 面 + MCP server。
**Claude memory 参考(只读)**:`developer-portal-sso.md`(含 OAuth refresh 契约坑全文)+ `open-api-and-wiki-retirement.md`。
**底稿**:`refs/plans/09-open-api-phase2/`(波任务书)+ `docs/developer-platform/`(05 §9.1=实现档案,02=公开 API 面)。commit 前缀 `feat(developer)`。

## 交接快照(2026-07-23)

- **代码全推毕**(origin 9ce8931 止:SSO+栅栏+计量+MCP M1,CI 绿,镜像已在 GHCR)。零迁移。
- **剩的全是用户侧部署编排**(顺序敏感,催办单):①admin 注册门户 OAuth client(confidential/auto_consent/grants 含 refresh_token/redirect_uri 精确串);②**`KUN_DEV_PORTAL_CLIENT_IDS`(oauth 服务 env)必须与 client 注册同批**,否则 SSO 用户被自家栅栏 403;③oauth 手动 pull 重部(no-pull gap);④portal env 五项+Deploy;⑤MCP 新 Dokploy 项目+mcp.nextmoe.dev DNS。
- 遗留 polish:账户级用量页缺「实时剩余」;瞬时刷新无重试 UI。
- 开放 API 轨侧尾巴(同一片水域,动前先看 memory):**三把 internal key 轮换待用户裁决**;第三方开放=用户级决策。

## 纪律要点(本轨特有)

- **OAuth session 只能走 `/oauth/token` 刷新**(第一方 /auth/refresh 硬拒 client-bound session,反之亦然);token 端点不设 cookie,Nitro 自落三 cookie(`auth_mode` 选路)。
- 门户是 IdP 的第一方 confidential client;redirect_uri 精确串匹配勿尾斜杠。
- 若用隔离 worktree 派发/自跑:**基底=origin/main 非本地 HEAD**,依赖本地未推提交须先 reset 到本地 tip。

## pi 值守状态(就地更新,一行一钩子)

- **2026-07-26 接管核实**:快照「待推批次二」已过时——git 核实 `origin/main..HEAD` 空,批二(9ce8931,MCP M1+workflows)已推毕;工作树净。
- **polish ①(账户级用量页「实时剩余」)= 陈旧钩子,勿再做**:wave09(`921dfce`,已在 origin)已交付 usage 页「实时配额剩余」区块(每 key 卡:今日剩余/配额/用量条/速率上限 + `live_unavailable` 降级 + 空态),与 05 §9-3 逐条对齐;类型 `DevLiveKey`/`DevUsageSummary` 同步。该 polish 行写于 wave09 之前。
- **polish ②(瞬时刷新重试 UI)完成 = `b923210`**:`useRefreshTransient`(useState,记账收敛在单飞 promise)+ `layout/RefreshBanner` 全局横幅(重试/忽略;`auth_mode` cookie 为「确有会话」门)+ `middleware/auth.ts` 三态化(原布尔坍缩把 transient 误弹 /login,违反 REFRESH_TRANSIENT 契约,已修)。typecheck/lint/build 三绿(pnpm 需 `--config.verify-deps-before-run=false`,并行轨升了 packageManager)。
- **插曲(已修)**:`b923210` 中 docs/developer-platform/05 被我整页覆写混入行锚元数据;`e8b8de3` 修复(HEAD~1 净化底本 + Python 精确重放两处编辑)。两枚同批推即无害。教训:编辑永远走精确替换,不要从带锚读取缓冲整页覆写。
- **本地 dev SSO 已接线(2026-07-26,用户实测 ClientID required 触发)**:播 `devportal-dev` client(dev-secret 契约,confidential/auto_consent/双 grant/三 scope/redirect 精确 :9430)+ `apps/developer/.env`(五 SSO 变量 + **`NUXT_OAUTH_API_BASE=http://127.0.0.1:9277` 覆写**,nuxt.config dev 默认 :19277 无人监听)。冒烟:authorize 303 → OP 授权页、伪 client 15001 对照、门户 payload 已含 client_id。配方固化 05 §9.1 第 3 条(refresh-dev-db 会抹行,照配方重播)。
- **⚠️ 本地栅栏缺口(未动,非我进程)**:在跑的 oauth(`/tmp/pi-web-track/oauth`,web 轨 dev loop)无 `KUN_DEV_PORTAL_CLIENT_IDS` → SSO 登录成功但 /dev/* 403(fail-closed)。解法:oauth 重启带 `KUN_DEV_PORTAL_CLIENT_IDS=devportal-dev`,或先用密码回退(快照任意账号,密码 `kungal-dev`)。
- **未动件**:部署编排 5 步(用户侧,催办单在交接快照);三把 internal key 轮换待用户裁决。
- **栅栏缺口一劳永逸闭合(2026-07-26,`7461f34`)**:`KUN_DEV_PORTAL_CLIENT_IDS=devportal-dev` 三处固化——`apps/api/.env` 活值(godotenv,air 热组与手起二进制皆读)+ `.env.example` 模板 + `docker-compose.dev.yml` oauth 块默认(原 fail-closed 空串);oauth 已带新 env 重启(healthz 200)。协议级 E2E 全链绿:login → consent 签码(state 回显)→ 门户换码(三 cookie 落齐)→ client-bound token(claims client_id=devportal-dev)→ /dev/apps + /dev/usage 200 → refresh 轮换 → 再 200。协议事实:`GET /oauth/authorize` 设计上恒 303 到 OP 前端页,授权码由 OP 前端 auto_consent 后打的 `POST /oauth/authorize/consent` 签发——脚本化 E2E 必须走 consent 腿。
- **⚠️ 工具层缓冲写回事故(本会话两起)**:带锚读取缓冲被整页写回工作树——第一起污染 docs/05(`e8b8de3` 修);第二起把陈旧快照刷回多文件(我域已提交文件被回退/删除 + 他轨文件也在波及面)。我域已从 HEAD 全量恢复,提交历史零损失;他轨文件(.pi/tracks/{catalog-data-qa,media-aggregation}.md、apps/api/cmd/*、jobs/tagcanon/* 等)不属我域未动,需各轨会话自查。对策已固化:编辑一律 Python 精确替换 + 字节级验证 + 即改即提交即核 HEAD。
- **浏览器级 UI 验证未做**:agent_browser 已全局卸载、Playwright MCP 未注册进本会话网关;协议级 curl E2E 已覆盖全链,RefreshBanner 的可视冒烟待工具就位或用户浏览器手点。
- **部署编排开跑(2026-07-26)**:runbook 落底稿 `refs/plans/09-open-api-phase2/11-portal-sso-deploy-runbook.md`(五步精确值+每步 pi 验收探针+回退)。CI 复绿:`build infra-mcp` 原红=Docker Hub 瞬时超时,`gh run rerun --failed` 后全绿(run 30192380471),15 镜像齐(infra-developer 含重试 UI/infra-oauth 含 fence/infra-mcp 刷新);deploy webhook 已触发主栈重部(安全:fence 空 env fail-closed+生产今天全 first-party token)。等用户执行①-⑤,pi 逐步验收。
- **生产校准(2026-07-26,用户质疑触发)**:07-23 快照过时——**①④⑤ 已在产**(①client `cfe9553d…` 注册+redirect_uri 精确匹配探针过;④portal env 已配但镜像缺今晨重试 UI,面板 Deploy 一次即补;⑤MCP 全链探针过:initialize/tools/list/假 key 真调用→上游 401 信封正确透出=跨项目 DNS+鉴权链实证)。**②③ 外部不可探**,判定法=用户浏览器 SSO 一遍(dashboard 403→②缺 env;/usage 无 live 区→③未拉镜像;全正常→编排收官)。runbook 已加校准节。
