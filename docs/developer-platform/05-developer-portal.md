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

### 9.1 下一波待办:登录升级为 OP 跳转 SSO(拍板 2026-07-23,待执行)

现状:门户登录**已是生态账号**(`/auth/login` 经同源 Nitro relay 直打 IdP,access_token / refresh cookie 约定镜像 apps/web),但形态是本地密码表单,不是下游站点那种 OAuth 跳转(已在 hub 登录即一键进入)。

待办内容(交执行者):

- 注册 `developer.nextmoe.dev` 专属 OAuth client(authorization code + **PKCE**);
- Nitro 侧新增 callback 换码,落进**现有** access_token / refresh cookie 约定(不引入第二套会话);登录页保留密码表单作回退,注册引导(现为跳主站)随 SSO 无缝化;
- 后端预期**零改动**:`/dev/*` 自助面本就吃用户 JWT Bearer(devapi extractKey 以 `nm_` 前缀区分 key 与用户 JWT,测试在案),OP 流签发的 access token 与直登 token 同一 signer;
- 执行时验证一点:`/dev/*` 的 owner 判定按 uid,与 token 的 client 归属无关(预期无阻力)。
