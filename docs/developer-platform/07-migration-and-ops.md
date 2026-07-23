# 迁移与运维提醒

> 本文承载 §14 迁移与运维提醒。设计与命名约定见 [01-design.md](./01-design.md);数据模型见 [04 §5](./04-platform-internals.md);认证/分层见 [03](./03-auth-and-tiers.md)。

---

## 14. 迁移与运维提醒

- **数据库迁移(主库 `kun_galgame_infra`)**:新增 `oauth_clients` 列(`owner_user_id` + `dev_*`)+ 新表 `developer_api_keys` / `developer_api_usage` → **`go run ./cmd/migrate`**。⚠️ 部署**不自动**跑迁移,漏跑 = GORM 读不存在的列 → 静默失败(参见仓库历史教训)。
- **新域名**:`api.nextmoe.dev`、`developer.nextmoe.dev` → DNS + Cloudflare(含公开读的 Cache Rules)+ Traefik 路由(按 `/v1/<face>/` 分发到 catalog/galgame 服务)+ 各后端 CORS allowlist。
- **Redis**:新增 `ratelimit:*` / `quota:*` / `apikey:*`(introspection 缓存)键空间。
- **契约**:公开 spec ×2 纳入 `docs:verify` + oasdiff,在 kungal-docs 登记为对外 Tier-A 契约。
- **面服务中间件**:catalog/galgame 两面共用鉴权/限流/配额中间件——首选提取为共享包(`kungal-kit` 候选);过渡期同构复制时,两处必须同步演进(写进各自 README owner 声明)。
