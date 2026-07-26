# 开发者门户

> 本文承载 §9 开发者门户(`developer.nextmoe.dev`)。设计与命名约定见 [01-design.md](./01-design.md);门户展示的公开 spec 与 OpenAPI 策略见 [02 §10](./02-public-api.md)。

---

## 9. 开发者门户(`developer.nextmoe.dev`)

- **账号复用**:用生态账号经 IdP 登录即开发者账号,**不另造身份**(品牌显示随「NextMoe 账户」改名同步,机制零变)。
- **核心功能**:
  1. 创建应用(= 一行 `oauth_clients`,`owner_user_id=当前用户`,`dev_enabled=true`)→ 拿 `client_id`(OAuth)。
  2. 管理 **API Keys**:创建(**show-once** 明文)、看 `prefix+last4+last_used`、轮换(带宽限)、吊销。
  3. **用量/配额**(`/usage` 页,读 `GET /dev/usage?days=N`,窗口 7/14/30 天):
     - **每日调用量**柱状图 + 窗口合计(总请求 / 错误率 / 4xx / 5xx)——读 `developer_api_usage` rollup 的稠密日序列。
     - **按应用** / **按面** 两张分解表(各按量降序)。
     - **实时配额剩余**:每把 active key 一张卡,显示今日剩余 / 每日配额 + 用量条 + 速率上限——**直接读 Redis 执法计数器**(与限流同源,非 rollup 估算)。计数后端不可达时该区降级为「暂不可用」提示,页面其余照常(`live_unavailable`)。
  4. **OpenAPI 文档**:用 **Scalar** 渲染(MIT、Try-It 最强、支持 OAuth flow、可嵌 Nuxt);两份公开 spec(catalog 面 / galgame 面)分 tab 呈现,未来媒介面同构加 tab。
  5. 申请更高 tier / NSFW(走审批)。
- **技术**:门户前端 Nuxt(`apps/` 下新增或并入现有);平台后端扩展 account/IdP 侧的 API(应用/key/用量 CRUD,鉴权用现有 JWT + `owner_user_id` 归属校验)。

### 9.1 登录升级为 OP 跳转 SSO(拍板 2026-07-23 · 生产部署收官 2026-07-26)

门户登录已从本地密码表单升级为 **OAuth Authorization Code + PKCE(S256)跳转登录**(下游站点那套「已在 hub 登录即一键进入」)。门户即 IdP 的一个**第一方 confidential client**;OAuth token 落进**现有** access_token / refresh cookie 约定,`/dev/*` 与 `/auth/me` 靠同一 signer 的 access_token 直接消费(后端**零代码改动**)。

**实现(apps/developer,全部 client/Nitro 侧)**:

- `app/utils/oauth-pkce.ts` — PKCE code_verifier / code_challenge(S256)/ state 生成;
- `app/composables/useOAuthLogin.ts` — `startLogin` / `startRegister`:存 verifier+state+redirect 进 sessionStorage → 顶层跳 `{authorizeBase}/oauth/authorize`(register 先经 OP `/auth/register?redirect=`);
- `app/pages/auth/callback.vue` — 校验 state → POST `/auth/exchange` → 播种 access_token + 拉 user → 跳 redirect/`/dashboard`;
- `server/routes/auth/{exchange,refresh,logout}.post.ts` + `server/utils/oauth-session.ts` — 服务端换码/刷新/吊销 + 落 cookie(access_token JS 可读 Path=/、refresh_token httpOnly Path=/auth、auth_mode 标记);
- 登录 modal(`components/login/Modal.vue`)= SSO 主按钮 + 密码表单回退 + SSO 注册引导。

**关键契约发现(修正原「落进现有 refresh 约定」的假设)**:第一方 `/api/v1/auth/refresh` **拒绝 client-bound(OAuth)session**(`auth_service.go:611`)——OAuth session **只能**经 `/oauth/token` `grant_type=refresh_token` 刷新(轮换)。故门户用 Nitro `/auth/refresh` 包 `/oauth/token`;`auth_mode` cookie 选择刷新/登出路径(`oauth`→Nitro 路由、`password`→第一方 relay),密码回退路径完全不变。access_token 仍是同一 signer,`/dev/*`、`/auth/me` 零改动消费。

**部署配置(✅ 已于 2026-07-26 全部落地:client 注册 + 双 env + oauth/portal 部署;SSO 全链与 /dev/* 栅栏放行均经生产实测)**:

1. **注册 OAuth client**(admin,`POST /api/v1/oauth/clients`,或管理台):`redirect_uris=["https://developer.nextmoe.dev/auth/callback"]`、`grants=["authorization_code","refresh_token"]`、`is_public=false`(confidential,门户有 Nitro 服务端)、`auto_consent=true`(第一方跳过同意页)、`allowed_scopes=[]`(默认 openid/profile/email)。响应给出 `client_id` + 一次性明文 `client_secret`。
2. **配置门户环境变量**(生产):`NUXT_PUBLIC_OAUTH_CLIENT_ID`、`NUXT_OAUTH_CLIENT_SECRET`(服务端)、`NUXT_PUBLIC_OAUTH_AUTHORIZE_BASE=https://oauth.kungal.com/api/v1`、`NUXT_PUBLIC_OAUTH_WEB_BASE=https://oauth.kungal.com`、`NUXT_PUBLIC_OAUTH_REDIRECT_URI=https://developer.nextmoe.dev/auth/callback`。`redirect_uri` **完全串匹配**,勿有尾斜杠漂移。**注意:Dokploy Environment 面板的值只做 compose 变量替换——变量必须同时在 `docker-compose.developer.yml` 的 `environment:` 块里声明转发才会进入容器**(缺声明会静默回落到镜像构建期的 localhost 默认值,2026-07-23 首次部署实爆);五个 SSO 变量已全部声明,新增 runtime-config 键时须同步补这里。
3. **本地 dev**:向本地 `kun_galgame_infra` 播一条等效 client 行(redirect_uri 用 `http://127.0.0.1:9430/auth/callback`),并设对应 `NUXT_PUBLIC_OAUTH_*` / `NUXT_OAUTH_CLIENT_SECRET`;authorize/web base 指向本地 OP(API :9277 / 前端 :9420)。注意 `refresh-dev-db` 会抹掉一切手播的 dev-only client 行——刷新后需重播,或改用快照自带的 prod client + `dev-secret-<client_id>` 契约。**已固化配方(2026-07-26 首播)**:client 行 = `devportal-dev` / secret = `sha256:` + hex(sha256(`dev-secret-devportal-dev`))(公开 dev 凭证契约)/ confidential / `auto_consent=true` / grants `["authorization_code","refresh_token"]` / scopes `["openid","profile","email"]` / redirect 精确 `http://127.0.0.1:9430/auth/callback`,SQL 模板沿用 `docs/dev-environment.md` 的 letmoe-dev upsert(替换 VALUES 即可);门户侧 env 落 `apps/developer/.env`(gitignored)= 五个 SSO 变量 + **`NUXT_OAUTH_API_BASE=http://127.0.0.1:9277`**(nuxt.config 的 dev 默认是 `:19277`,本地 oauth 实际监听 `:9277`,不覆写则 Nitro 换码/刷新打不通)。**栅栏联动**:oauth 进程须带 `KUN_DEV_PORTAL_CLIENT_IDS=devportal-dev`,否则 SSO 登录成功但 `/dev/*` 被 DevPortalFence 403(fail-closed;密码回退不受影响)。**本地已固化为默认(2026-07-26)**:`apps/api/.env`(godotenv——air 热组与手起二进制皆读)、`apps/api/.env.example`、`docker-compose.dev.yml` oauth 块三处均为 `devportal-dev`;oauth 重启后协议级 E2E 全链绿(login → consent 签码 → 门户换码 → client-bound token → `/dev/*` 200 → refresh 轮换 → 再 200)。注意:`GET /oauth/authorize` 设计上恒 303 到 OP 前端页,授权码由 OP 前端 auto_consent 后打的 `POST /oauth/authorize/consent` 签发——脚本化 E2E 必须走 consent 腿。

> `/dev/*` 的 owner 判定按 uid、与 token 的 client 归属无关(已核:OAuth access_token 在 `/auth/me` 与 devapi 链上等价于直登 token)。

**验收后记(2026-07-23 双维度评审后修正)**:

- **token 读取器**(`server/utils/oauth-session.ts` `tokenWirePayload/tokenWireError`):`/oauth/token` 是 OAuth 协议端点,只有 RFC 6749 裸 shape(成功 `{access_token,...}` / 失败 `{error,error_description}`)。exchange/refresh 路由以 **access_token 存在性**判成败,不看任何状态字段——2026-07-25 线格式切换那天,只看 `code` 的读取器会静默全断,这条判据是唯一没被咬到的原因。
- **登出双模全清**(`useAuth.logout`):密码与 SSO 两种 session 的 refresh_token 同名不同 Path(`/api/v1/auth` vs `/auth`),登出无条件两路都打——只清当前 auth_mode 会让另一模式的存活 cookie 在下次导航时把用户「静默复活」登录(跨账号时更是错账号复活)。
- **瞬时刷新失败不清会话**(`useTokenRefresh` 返回 `REFRESH_TRANSIENT`):网络抖动 / IdP 5xx / Nitro 刷新路由的蓄意 503 不再被判成 session 死亡强制登出;仅 4xx(无 cookie / 过期 / 吊销)才清会话跳登录。**重试 UI(2026-07-26 补齐)**:transient 态现有全局呈现——`useRefreshTransient`(useState,记账收敛在单飞 promise 上)驱动 `layout/RefreshBanner` 固定横幅(说明会话仍有效 + 一键重试/忽略;以 `auth_mode` cookie 为「确有会话」门,匿名访客不见横幅);重试成功即落 token、拉 user、`refreshNuxtData()` 重取降级页面的数据,重试发现 session 已死才清态跳 /login。同波修正 `middleware/auth.ts`:原先把 transient 布尔坍缩成「未刷新→弹 /login」,违反本条契约;现改为三态——成功落 token 放行、transient 放行(页面降级渲染 + 横幅接手)、确死才弹登录。
- **已接受的偏差(有意为之,评审记录在案)**:access_token 为 JS 可读 cookie(沿袭 apps/web 约定;refresh_token httpOnly 兜底持久层);PKCE verifier/state 存 sessionStorage(confidential client 下 PKCE 是纵深防御,主认证在服务端 client_secret)。
- **client 栅栏(已拍板并实现,2026-07-23 wave 08)**:上面「owner 判定与 token 的 client 归属无关」原是隐患——`middleware.Auth` 只验 signer + uid、不查 token 属哪个 OAuth client,任何第三方 app 的用户 token(仅授 `openid profile email`)都能替用户铸/轮换/吊销 API key(confused-deputy)。已加 **`DevPortalFence`**(`middleware.Auth` 之后):第一方 `/auth/login` session token(`client_id==""`)与 env `KUN_DEV_PORTAL_CLIENT_IDS` 白名单内的 client 放行,其余 403;**空白名单 = fail-closed(仅放行第一方)**。因此门户专属 client 注册后,须把它的 `client_id` 填进 **oauth 服务**的 `KUN_DEV_PORTAL_CLIENT_IDS`,否则门户自己的 SSO 用户会被栅栏 403(密码回退不受影响)。完整契约与 `dev:manage` 升级路径见 [03 §4.4](./03-auth-and-tiers.md)。
