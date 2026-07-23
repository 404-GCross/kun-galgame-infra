# 开发者门户

> 本文承载 §9 开发者门户(`developer.nextmoe.dev`)。设计与命名约定见 [01-design.md](./01-design.md);门户展示的公开 spec 与 OpenAPI 策略见 [02 §10](./02-public-api.md)。

---

## 9. 开发者门户(`developer.nextmoe.dev`)

- **账号复用**:用生态账号经 IdP 登录即开发者账号,**不另造身份**(品牌显示随「NextMoe 账户」改名同步,机制零变)。
- **核心功能**:
  1. 创建应用(= 一行 `oauth_clients`,`owner_user_id=当前用户`,`dev_enabled=true`)→ 拿 `client_id`(OAuth)。
  2. 管理 **API Keys**:创建(**show-once** 明文)、看 `prefix+last4+last_used`、轮换(带宽限)、吊销。
  3. **用量/配额**:实时剩余 + 历史曲线(读 `developer_api_usage`)。
  4. **OpenAPI 文档**:用 **Scalar** 渲染(MIT、Try-It 最强、支持 OAuth flow、可嵌 Nuxt);两份公开 spec(catalog 面 / galgame 面)分 tab 呈现,未来媒介面同构加 tab。
  5. 申请更高 tier / NSFW(走审批)。
- **技术**:门户前端 Nuxt(`apps/` 下新增或并入现有);平台后端扩展 account/IdP 侧的 API(应用/key/用量 CRUD,鉴权用现有 JWT + `owner_user_id` 归属校验)。

### 9.1 登录升级为 OP 跳转 SSO(拍板 2026-07-23 · 门户侧实现完成,待部署配置)

门户登录已从本地密码表单升级为 **OAuth Authorization Code + PKCE(S256)跳转登录**(下游站点那套「已在 hub 登录即一键进入」)。门户即 IdP 的一个**第一方 confidential client**;OAuth token 落进**现有** access_token / refresh cookie 约定,`/dev/*` 与 `/auth/me` 靠同一 signer 的 access_token 直接消费(后端**零代码改动**)。

**实现(apps/developer,全部 client/Nitro 侧)**:

- `app/utils/oauth-pkce.ts` — PKCE code_verifier / code_challenge(S256)/ state 生成;
- `app/composables/useOAuthLogin.ts` — `startLogin` / `startRegister`:存 verifier+state+redirect 进 sessionStorage → 顶层跳 `{authorizeBase}/oauth/authorize`(register 先经 OP `/auth/register?redirect=`);
- `app/pages/auth/callback.vue` — 校验 state → POST `/auth/exchange` → 播种 access_token + 拉 user → 跳 redirect/`/dashboard`;
- `server/routes/auth/{exchange,refresh,logout}.post.ts` + `server/utils/oauth-session.ts` — 服务端换码/刷新/吊销 + 落 cookie(access_token JS 可读 Path=/、refresh_token httpOnly Path=/auth、auth_mode 标记);
- 登录 modal(`components/login/Modal.vue`)= SSO 主按钮 + 密码表单回退 + SSO 注册引导。

**关键契约发现(修正原「落进现有 refresh 约定」的假设)**:第一方 `/api/v1/auth/refresh` **拒绝 client-bound(OAuth)session**(`auth_service.go:611`)——OAuth session **只能**经 `/oauth/token` `grant_type=refresh_token` 刷新(轮换)。故门户用 Nitro `/auth/refresh` 包 `/oauth/token`;`auth_mode` cookie 选择刷新/登出路径(`oauth`→Nitro 路由、`password`→第一方 relay),密码回退路径完全不变。access_token 仍是同一 signer,`/dev/*`、`/auth/me` 零改动消费。

**部署配置(交运维 / 用户,门户代码已就绪)**:

1. **注册 OAuth client**(admin,`POST /api/v1/oauth/clients`,或管理台):`redirect_uris=["https://developer.nextmoe.dev/auth/callback"]`、`grants=["authorization_code","refresh_token"]`、`is_public=false`(confidential,门户有 Nitro 服务端)、`auto_consent=true`(第一方跳过同意页)、`allowed_scopes=[]`(默认 openid/profile/email)。响应给出 `client_id` + 一次性明文 `client_secret`。
2. **配置门户环境变量**(生产):`NUXT_PUBLIC_OAUTH_CLIENT_ID`、`NUXT_OAUTH_CLIENT_SECRET`(服务端)、`NUXT_PUBLIC_OAUTH_AUTHORIZE_BASE=https://oauth.kungal.com/api/v1`、`NUXT_PUBLIC_OAUTH_WEB_BASE=https://oauth.kungal.com`、`NUXT_PUBLIC_OAUTH_REDIRECT_URI=https://developer.nextmoe.dev/auth/callback`。`redirect_uri` **完全串匹配**,勿有尾斜杠漂移。
3. **本地 dev**:向本地 `kun_galgame_infra` 播一条等效 client 行(redirect_uri 用 `http://127.0.0.1:9430/auth/callback`),并设对应 `NUXT_PUBLIC_OAUTH_*` / `NUXT_OAUTH_CLIENT_SECRET`;authorize/web base 指向本地 OP(API :9277 / 前端 :9420)。

> `/dev/*` 的 owner 判定按 uid、与 token 的 client 归属无关(已核:OAuth access_token 在 `/auth/me` 与 devapi 链上等价于直登 token)。
