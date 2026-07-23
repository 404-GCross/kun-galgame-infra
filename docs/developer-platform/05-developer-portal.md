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
