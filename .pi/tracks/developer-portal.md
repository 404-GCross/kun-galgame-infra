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

- (待 pi 填)
