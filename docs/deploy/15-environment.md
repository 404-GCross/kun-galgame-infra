# 15 · 环境变量大全(三仓 / CI / 生产 / Dokploy / Cloudflare)

> 本篇是**配置层的总索引**:把三个仓库的每一个环境变量、构建参数、密钥,以及它们在
> **本地 / GitHub Actions / 生产服务器 / Dokploy / Cloudflare** 各处的设法,集中讲清楚。
> 偏「上线怎么填」的速查在 [05-configuration.md](./05-configuration.md);偏「Dokploy 怎么编排」在
> [12-dokploy.md](./12-dokploy.md);CI 在 [13-registry-ci.md](./13-registry-ci.md)。本篇是它们的**底层全集**。
>
> ⚠️ 文中所有具体值都是**测试占位**(`191007`、`kun-docker-test-*` 等)。真实生产密钥**只填在
> Dokploy / GitHub secrets / 各机的 `docker/*.env`**,**永不入仓、永不写进本文档**。

---

## 15.0 先看懂四个问题

| 问题 | 答案速记 |
|---|---|
| **配置分几层?** | ① 构建参数(烤进镜像)② 后端运行时 `env_file`(`docker/*.env`)③ compose 编排插值 `${VAR}`(仅 infra 生产)④ 前端 public(infra 烤镜像 / 下游运行期)⑤ 平台 secret(GitHub Actions) |
| **在哪里设?** | 本地:`docker/*.env` + compose build args。生产:Dokploy 的 **Environment** + 各机 `docker/*.env`;CI:**GitHub repo Secrets**;DNS/图床:**Cloudflare 控制台** |
| **公开还是密钥?** | `NUXT_PUBLIC_*` / `PUBLIC_*` = 浏览器可见,**不能放密钥**;`KUN_PG_PASSWORD` / `JWT_SECRET` / `OAUTH_CLIENT_SECRET` / S3 keys = 密钥 |
| **谁必须和谁一致?** | 见 [§15.3 一致性铁律](#153-跨服务一致性铁律最容易踩) —— 配错这几对,服务能起但功能 401/403/连不上 |

---

## 15.1 配置分层模型

一个值最终怎么进到进程里,取决于它属于哪一层。**这是整篇的骨架**:

| 层 | 载体 | 何时生效 | 进镜像? | 生产在哪设 |
|---|---|---|---|---|
| **A. 构建参数** | compose `build.args` / Dockerfile `ARG` | `docker build` 时 | ✅ 烤进镜像 | CI:`.github/workflows/build.yml` 的 `build-args`;或 Dokploy「从 Git 构建」时的 build args |
| **B. 后端运行时** | `env_file: docker/*.env` | 容器启动 | ❌ 被 `.dockerignore` 挡掉 | 放到**部署机**的 `docker/*.env`,或用 Dokploy 把内容写进去(见 [§15.8](#158-生产--dokploy-注入方法)) |
| **C. 编排插值** | compose 里 `${VAR:?...}`(**仅 infra 生产 compose**) | `docker compose up` 时 | ❌ | **Dokploy 的 Environment**(或部署目录的根 `.env`) |
| **D. 前端 public** | infra:**A 层烤镜像**;kungal/moyu:**B 层运行期** | 见 [§15.5](#155-前端-public-配置infra-烤镜像-vs-下游运行期) | infra ✅ / 下游 ❌ | infra 改 CI build-args 重构;下游改 `docker/web.env` |
| **E. 平台 secret** | `${{ secrets.* }}` | CI 运行时 | ❌ | **GitHub → 仓库 Settings → Secrets** |

**两条最关键的事实**:

1. **`docker/*.env` 不进镜像**。`.dockerignore` 里有 `docker/*.env` / `**/.env*`,镜像里没有它们;
   `env_file:` 是 compose 在**容器启动时从宿主机读**。所以同一个 GHCR 镜像在任何机器都通用,
   机器自己的 `docker/*.env` 决定它连哪个库、用什么密钥。
2. **只有 infra 的生产 compose 用 `${VAR:?}` 插值**(postgres/minio/meili 这几个基础设施的密码)。
   kungal/moyu 生产 compose **没有任何 `${VAR}`**,它们的全部配置都在 `docker/api.env` / `docker/web.env` 里。

---

## 15.2 命名前缀规律(看名字就知道它属于哪层、给谁用)

| 前缀 / 形态 | 含义 | 谁读 | 例 |
|---|---|---|---|
| `KUN_PG_*` / `KUN_IMAGE_*` / `KUN_MEILISEARCH_*` / `KUN_FIBER_*` | **infra** Go 服务运行时 | oauth/image/galgame | `KUN_PG_PASSWORD`、`KUN_IMAGE_S3_ENDPOINT` |
| `KUN_DATABASE_URL` / `OAUTH_*` / `CORS_ALLOW_ORIGINS` / `KUN_VISUAL_NOVEL_S3_*` | **下游** Go 服务运行时 | kungal/moyu api | `KUN_DATABASE_URL`、`OAUTH_CLIENT_SECRET` |
| `NUXT_PUBLIC_*` | **浏览器可见**,Nitro 运行期覆盖 runtimeConfig.public | 各前端 | `NUXT_PUBLIC_API_BASE`、`NUXT_PUBLIC_OAUTH_CLIENT_ID` |
| `NUXT_API_BASE_SSR` / `NUXT_API_BASE_URL` / `NUXT_AUTH_API_BASE_SSR` | **SSR 内部 base**(容器内服务名),**每个前端名字不同**(见 [§15.5](#155-前端-public-配置infra-烤镜像-vs-下游运行期)) | 各前端 SSR | `http://oauth:9277/api/v1` |
| `PUBLIC_*`(build arg) | 构建期喂给 `nuxt.Dockerfile`,烤进前端镜像 | 构建器 | `PUBLIC_API_BASE`、`PUBLIC_OAUTH_CLIENT_ID` |
| `${VAR:?msg}` | compose 插值,**空则 fail-fast** | docker compose | `${POSTGRES_PASSWORD:?...}` |
| `${VAR:-default}` | compose 插值,**空则用默认** | docker compose | `${POSTGRES_USER:-postgres}` |
| `GO_VERSION` / `NODE_VERSION` / `CMD` / `APP` | 纯构建参数(版本 / 多二进制选择 / monorepo app) | Dockerfile | `CMD=oauth`、`APP=wiki` |

> ⚠️ **SSR base 三仓命名不统一**(历史原因,代码已实现「双 base」):
> infra web/wiki = `NUXT_API_BASE_SSR`(wiki 多一个 `NUXT_AUTH_API_BASE_SSR`)、
> **moyu** = `NUXT_API_BASE_SSR`、**kungal** = `NUXT_API_BASE_URL`。改 SSR base 时认准各自的名字。

---

## 15.3 跨服务一致性铁律(最容易踩)

这几对值**必须在指定范围内完全相同**。配错时服务通常**能启动**,但运行时报 401/403/连接失败,
很难排查。**上线前对一遍**:

| 值 | 必须一致的范围 | 载体(各处) |
|---|---|---|
| **Postgres 密码** | infra postgres ↔ 所有连库者 | postgres `POSTGRES_PASSWORD` = infra `KUN_PG_PASSWORD`(oauth/image/galgame.env)= kungal/moyu `KUN_DATABASE_URL` 里的密码 |
| **`JWT_SECRET`** | **仅 infra 三个 Go 服务之间** | oauth/image/galgame.env 的 `JWT_SECRET`。oauth 用它 **HS256 签发** access_token,image/galgame 用 `utils.ParseToken` **本地验签**。三者不一致 → image/galgame 对带令牌的请求 401 |
| **Meili master key** | infra meili ↔ 用搜索者 | meili `MEILI_MASTER_KEY` = galgame.env `KUN_MEILISEARCH_API_KEY` = kungal api.env `MEILISEARCH_KEY` |
| **MinIO 凭据**(仅自托管图床时) | infra minio ↔ 连 S3 者 | minio `MINIO_ROOT_USER/PASSWORD` = oauth/image.env `KUN_IMAGE_S3_ACCESS_KEY/SECRET_KEY`。**生产用 R2 则填 R2 的 key,与 minio 无关** |
| **OAuth client secret** | 枢纽注册的明文 ↔ 下游 env | 枢纽 `oauth_clients` 表存 `sha256:<hex>`;下游 `OAUTH_CLIENT_SECRET` 填**注册时的明文**。详见 [12-dokploy §12.3](./12-dokploy.md) |
| **图片 CDN 公网域** | 所有「生成图片 URL」的后端/前端 | infra 各 env 的 `KUN_IMAGE_PUBLIC_BASE_URL`、moyu `KUN_IMAGE_CDN_BASE`(+ 前端 imageBed)、kungal `KUN_IMAGE_PUBLIC_BASE_URL` 应指**同一公网域**(`https://image.kungal.iloveren.link`) |

> 🔑 **`JWT_SECRET` 的常见误解**:它**不需要**三仓一致。下游 kungal/moyu **不本地验签** infra 的
> access_token —— 它们走 OAuth 授权码流程,再调 `GET /oauth/userinfo`(网络请求)拿身份。
> 所以:**kungal** 的 `JWT_SECRET` 只用来签**它自己**的会话 cookie(仅需 kungal 内部自洽,生产换成强随机即可);
> **moyu** 根本没有 `JWT_SECRET`(会话是不透明 session,靠调 OAuth 校验)。只有 **infra 内部**那三个服务必须共用同一个。

> 🖼 **图片 URL 路径契约**:image_service 的对象键是 `{cdnBase}/{hash[:2]}/{hash[2:4]}/{hash}[_variant].webp`(**无 `/img/` 段**)。
> infra/kungal 一直如此;moyu 早期前后端多加了 `/img/`(且 `imageBed` 硬编码成 `image.moyu.moe`),现已对齐——
> moyu 前后端均用规范路径,`KUN_IMAGE_CDN_BASE` 与前端 imageBed 都指向共享的 `image.kungal.iloveren.link`。
> 压缩按用途选 variant(均为 webp):整图/`topic` 截图用主图(主流水线 ≤1920×1080 q77)、banner 缩略用 `mini`(460×259)、头像列表用 `100`、设置页大图用 `256`。

---

## 15.4 逐服务环境变量详表

> 值列为**测试占位**;`(密钥)` 表示生产必须换成真值且不入仓。空白格 = 该值默认留空。

### 15.4.1 infra · 基础设施(compose 直接给,非 env_file)

| 服务 | 变量 | 测试值 | 生产(`docker-compose.prod.yml`) | 作用 |
|---|---|---|---|---|
| **postgres** | `POSTGRES_USER` | `postgres` | `${POSTGRES_USER:-postgres}` | 超级用户名 |
| | `POSTGRES_PASSWORD` | `191007` | `${POSTGRES_PASSWORD:?}`(**必填**) | 超级用户密码,**必须 = 各 `KUN_PG_PASSWORD` / `KUN_DATABASE_URL` 密码** |
| | `POSTGRES_DB` | `kun_galgame_infra` | 同(硬编码) | 首库;其余 4 库由 `initdb.d/01-create-databases.sh` 建 |
| **redis** | —(无 env) | | | `--appendonly yes` 持久化 |
| **minio** | `MINIO_ROOT_USER` | `minioadmin` | `${MINIO_ROOT_USER:?}`(**必填**) | S3 管理员;**生产用 R2 时此服务可空跑** |
| | `MINIO_ROOT_PASSWORD` | `minioadmin` | `${MINIO_ROOT_PASSWORD:?}`(**必填**) | S3 管理员密码 |
| **meili** | `MEILI_MASTER_KEY` | `kun_docker_test_meili_master_key_change_me` | `${MEILI_MASTER_KEY:?}`(**必填**) | 搜索主密钥,**必须 = galgame `KUN_MEILISEARCH_API_KEY`** |
| | `MEILI_NO_ANALYTICS` | `true` | 同 | 关遥测 |
| | `MEILI_ENV` | —(dev 默认) | `production` | 生产模式 |

> postgres 在 pg18 起 VOLUME 路径是 `/var/lib/postgresql`(不再是 `/data`);meili 别名 `meilisearch`(供 kungal 用同名解析)。

### 15.4.2 infra · oauth(`docker/oauth.env`)

| 变量 | 测试值 | 必填 | 作用 |
|---|---|---|---|
| `KUN_ENV` | `production` | | 运行模式 |
| `KUN_FIBER_SERVER_HOST` | `0.0.0.0` | | **容器内必须 0.0.0.0** |
| `KUN_FIBER_SERVER_PORT` | `9277` | | 监听端口 |
| `KUN_SITE_URL` | `http://localhost:15005` | | oauth 自身公网地址(邮件/跳转用)→ 生产 `https://oauth.kungal.com` |
| `KUN_FRONTEND_URL` | `http://localhost:15008` | | admin 前端地址 → 生产 `https://oauth.kungal.com` |
| `KUN_FRONTEND_CORS_ORIGIN` | `http://localhost:15008,...` | | CORS 白名单(逗号分隔,含 localhost+127.0.0.1) |
| `KUN_PG_HOST/PORT/USER` | `postgres`/`5432`/`postgres` | | 连库 |
| `KUN_PG_PASSWORD` | `191007` | ✅ | **`config.validate` 要求非空**;= postgres 密码 |
| `KUN_PG_DATABASE` | `kun_galgame_infra` | | 主库 |
| `KUN_PG_SSLMODE` / `KUN_PG_TIMEZONE` | `disable` / `Asia/Shanghai` | | |
| `KUN_GALGAME_PG_DATABASE` | `kun_galgame_wiki` | | 下游 wiki 库(身份关联查询) |
| `KUN_IMAGES_PG_DATABASE` | `kun_images` | | 内嵌图床 admin 端点用 |
| `REDIS_ENABLED` / `REDIS_HOST` / `REDIS_PORT` | `true` / `redis` / `6379` | | 验证码/会话 |
| `JWT_SECRET` | `kun-docker-test-jwt-secret-change-me-please` | ✅ | **HS256 签发 access_token**;= image/galgame 的 `JWT_SECRET` |
| `KUN_IMAGE_S3_ENDPOINT` | `http://minio:9000` | | S3/MinIO 端点 → 生产 R2 `https://<acct>.r2.cloudflarestorage.com` |
| `KUN_IMAGE_S3_REGION` | `us-east-1` | | R2 用 `auto` |
| `KUN_IMAGE_S3_ACCESS_KEY` / `KUN_IMAGE_S3_SECRET_KEY` | `minioadmin` / `minioadmin` | (密钥) | 生产填 R2 凭据 |
| `KUN_IMAGE_S3_BUCKET` | `kun-images` | | 桶名 |
| `KUN_IMAGE_S3_FORCE_PATH_STYLE` | `true` | | MinIO 必须 true;R2 可 false |
| `KUN_IMAGE_PUBLIC_BASE_URL` | `http://localhost:15002/kun-images` | | **浏览器取图前缀** → 生产 `https://image.kungal.iloveren.link` |
| `KUN_IMAGE_CLIENT_BASE_URL` | `http://image:9278` | | s2s 调 image 服务 |
| `KUN_VISUAL_NOVEL_EMAIL_HOST/PORT/ACCOUNT/PASSWORD/FROM` | `tuesday.mxrouting.net`/`587`/`auth@kungal.com`/(密钥)/`KUN VISUAL NOVEL` | (密钥) | 发信(验证码/找回)。**纯出站**,无需开入站端口 |

### 15.4.3 infra · image(`docker/image.env`)

与 oauth 重叠的(`KUN_PG_*`、`JWT_SECRET`、`KUN_IMAGE_S3_*`、`REDIS_*`)同上,额外:

| 变量 | 测试值 | 必填 | 作用 |
|---|---|---|---|
| `KUN_IMAGE_SERVICE_HOST` | `0.0.0.0` | | 绑定地址 |
| `KUN_IMAGE_SERVICE_PORT` | `9278` | | 端口 |
| `KUN_IMAGE_UPLOAD_ENABLED` | `true` | | 开 `POST /image/upload`(代码默认 false) |
| `KUN_IMAGES_PG_DATABASE` | `kun_images` | | 自身库(启动 AutoMigrate) |
| `JWT_SECRET` | (同 oauth) | ✅ | **即使 image 不签发 JWT,`config.validate` 仍要求非空**;且 image 中间件用它**本地验** access_token |

> `KUN_IMAGE_PRESETS_PATH` 已在镜像内固定为 `/app/configs/image_presets.yaml`(cgo.Dockerfile 的 `ENV`),**勿覆盖**。

### 15.4.4 infra · galgame(`docker/galgame.env`)

| 变量 | 测试值 | 必填 | 作用 |
|---|---|---|---|
| `KUN_ENV` / `KUN_FIBER_SERVER_HOST` | `production` / `0.0.0.0` | | |
| `KUN_GALGAME_PORT` | `9280` | | 端口 |
| `KUN_FRONTEND_CORS_ORIGIN` | (同 oauth) | | CORS |
| `KUN_PG_*`(host/port/user/password/sslmode/timezone) | `postgres`…`191007`… | `PASSWORD` ✅ | 连主库(身份) |
| `KUN_GALGAME_PG_DATABASE` | `kun_galgame_wiki` | | wiki 业务库(`migrate-galgame` 建 schema) |
| `JWT_SECRET` | (同 oauth) | ✅ | 本地验下游令牌;= oauth/image |
| `KUN_MEILISEARCH_HOST` | `http://meili:7700` | | 搜索引擎 |
| `KUN_MEILISEARCH_API_KEY` | `kun_docker_test_meili_master_key_change_me` | | = `MEILI_MASTER_KEY`(否则 403;`EnsureIndexes` 非致命) |

### 15.4.5 infra · web / wiki(前端,**public 走构建期烤镜像**)

运行期只注入 SSR 内部 base(见 [§15.5](#155-前端-public-配置infra-烤镜像-vs-下游运行期)):

| 服务 | 运行期 environment | 构建期 build args(dev compose / CI 见 [§15.7](#157-github-actions-ci-变量与-secrets)) |
|---|---|---|
| **web**(admin) | `NUXT_API_BASE_SSR=http://oauth:9277/api/v1` | `APP=web`、`PUBLIC_API_BASE`、`PUBLIC_IMAGE_CDN_BASE` |
| **wiki** | `NUXT_API_BASE_SSR=http://galgame:9280/api`、`NUXT_AUTH_API_BASE_SSR=http://oauth:9277/api/v1` | `APP=wiki`、`PUBLIC_API_BASE`、`PUBLIC_AUTH_API_BASE`、`PUBLIC_OAUTH_AUTHORIZE_BASE`、`PUBLIC_OAUTH_CLIENT_ID`(=`galgame-wiki-admin`)、`PUBLIC_OAUTH_REDIRECT_URI`、`PUBLIC_IMAGE_CDN_BASE` |

### 15.4.6 kungal · api(`docker/api.env`)

| 变量 | 测试值 | 必填 | 作用 |
|---|---|---|---|
| `SERVER_PORT` / `SERVER_MODE` | `2334` / `prod` | | 端口 / Fiber 模式 |
| `CORS_ALLOW_ORIGINS` | `http://localhost:15013,http://127.0.0.1:15013` | | 浏览器源白名单 → 生产 `https://www.kungal.com,https://kungal.com` |
| `KUN_DATABASE_URL` | `postgresql://postgres:191007@postgres:5432/kungalgame` | ✅ | 连库 DSN(密码段 = postgres 密码) |
| `DB_MAX_OPEN_CONNS` / `DB_MAX_IDLE_CONNS` / `DB_CONN_MAX_LIFETIME` | `25` / `10` / `300` | | 连接池 |
| `REDIS_HOST` / `REDIS_PORT` / `REDIS_PASSWORD` / `REDIS_DB` | `redis` / `6379` / / `0` | | Redis |
| `OAUTH_SERVER_URL` | `http://oauth:9277/api/v1` | ✅ | OAuth API(s2s,服务名) |
| `OAUTH_CLIENT_ID` | (论坛 client,见 [12.3](./12-dokploy.md)) | ✅ | OAuth 客户端 |
| `OAUTH_CLIENT_SECRET` | (密钥) | ✅ | **空则 `requireEnv` 启动失败**;= 注册明文 |
| `OAUTH_REDIRECT_URI` | `http://localhost:15013/auth/callback` | ✅ | 回调,= 注册的 redirect → 生产 `https://www.kungal.com/auth/callback` |
| `JWT_SECRET` | `kun-docker-test-...` | | **签 kungal 自己的会话 cookie**(仅 kungal 内部自洽;生产换强随机) |
| `MEILISEARCH_URL` / `MEILISEARCH_KEY` | `http://meilisearch:7700` / `kun_docker_test_meili_master_key_change_me` | | 搜索;key = `MEILI_MASTER_KEY`(否则 403) |
| `GALGAME_WIKI_BASE_URL` | `http://galgame:9280/api` | | 调 wiki(s2s) |
| `KUN_IMAGE_PUBLIC_BASE_URL` | `https://image.kungal.com` | | 生成图片 URL 的公网前缀(应与 infra 一致 → `https://image.kungal.iloveren.link`) |
| `KUN_IMAGE_CLIENT_BASE_URL` | `http://image:9278` | | 直传封面到 image 服务(s2s) |
| `KUN_IMAGE_CLIENT_ID` / `KUN_IMAGE_CLIENT_SECRET` | / | (密钥) | image s2s 上传的 OAuth client;空则禁用上传 |
| `S3_ENDPOINT/REGION/BUCKET/ACCESS_KEY/SECRET_KEY` | (空) | (密钥) | 备用 S3(图床已交给 image 服务,通常留空) |
| `FILE_STORAGE_ENDPOINT/REGION/BUCKET/ACCESS_KEY/SECRET_KEY` | (空) | (密钥) | **Backblaze B2**,工具档案上传用 |
| `MAIL_HOST/PORT/USER/PASSWORD/FROM` | (空) / `587` / … | (密钥) | SMTP 发信 |

### 15.4.7 kungal · web(`docker/web.env`,**public 走运行期**)

| 变量 | 测试值 | 作用 |
|---|---|---|
| `NODE_ENV` | `production` | |
| `NUXT_API_BASE_URL` | `http://api:2334` | **SSR** 内部 base(服务名)——kungal 的 SSR 名是 `..._URL` |
| `NUXT_PUBLIC_API_BASE_URL` | `http://localhost:15012` | **浏览器** API 地址 → 生产 `https://www.kungal.com` |
| `NUXT_PUBLIC_OAUTH_SERVER_URL` | `http://localhost:15005/api/v1` | OAuth API(浏览器)→ `https://oauth.kungal.com/api/v1` |
| `NUXT_PUBLIC_OAUTH_FRONTEND_URL` | `http://localhost:15008` | OAuth 账户中心跳转 → `https://oauth.kungal.com` |
| `NUXT_PUBLIC_OAUTH_CLIENT_ID` | (论坛 client) | OAuth 客户端(浏览器) |
| `NUXT_PUBLIC_OAUTH_REDIRECT_URI` | `http://localhost:15013/auth/callback` | 回调 → `https://www.kungal.com/auth/callback` |
| `NUXT_PUBLIC_GALGAME_WIKI_URL` | `http://localhost:15007/api` | wiki API(浏览器)→ `https://wiki.kungal.com/api` |
| `NUXT_PUBLIC_KUN_GALGAME_URL` | `http://localhost:15013` | 站点根 URL(SEO/sitemap)→ `https://www.kungal.com` |
| `NUXT_PUBLIC_KUN_VISUAL_NOVEL_FORUM_YANDEX_VERIFICATION` | (注释掉) | Yandex 站点验证(可选) |

### 15.4.8 moyu · api(`docker/api.env`)

| 变量 | 测试值 | 必填 | 作用 |
|---|---|---|---|
| `KUN_SERVER_PORT` / `KUN_SERVER_MODE` | `5214` / `prod` | | 端口 / 模式(`prod` 触发图床变量 fail-fast) |
| `KUN_DATABASE_URL` | `postgresql://postgres:191007@postgres:5432/kungalgame_patch?sslmode=disable` | ✅ | 连库(`mustGetEnv`,空则 panic) |
| `REDIS_HOST` / `REDIS_PORT` / `REDIS_PASSWORD` | `redis` / `6379` / | | Redis |
| `OAUTH_SERVER_URL` | `http://oauth:9277/api/v1` | | OAuth API(s2s) |
| `OAUTH_CLIENT_ID` | `df3ff6008d740bfacbe46aa8cf483cf2`(补丁 client) | | OAuth 客户端 |
| `OAUTH_CLIENT_SECRET` | (密钥) | ✅ | OAuth Basic Auth |
| `OAUTH_REDIRECT_URI` | `http://localhost:15011/auth/callback` | | 回调 → `https://www.moyu.moe/auth/callback` |
| `KUN_GALGAME_WIKI_BASE_URL` | `http://galgame:9280/api` | | 调 wiki(s2s) |
| `KUN_IMAGE_SERVICE_BASE_URL` | `http://image:9278` | **prod 必填** | image 服务源(`getEnvProd` fail-fast) |
| `KUN_IMAGE_CDN_BASE` | `http://localhost:15002/kun-images` | **prod 必填** | 图片公网域 → `https://image.kungal.iloveren.link`(`getEnvProd` fail-fast) |
| `KUN_IMAGE_OAUTH_CLIENT_ID` / `KUN_IMAGE_OAUTH_CLIENT_SECRET` | / | | image s2s client;空则回落到 `OAUTH_CLIENT_ID/SECRET` |
| `KUN_VISUAL_NOVEL_S3_STORAGE_ENDPOINT` | `https://s3.us-east-005.backblazeb2.com` | | **B2**(补丁文件,非图床) |
| `KUN_VISUAL_NOVEL_S3_STORAGE_URL` | `https://oss.moyu.moe` | | B2 公网下载前缀 |
| `KUN_VISUAL_NOVEL_S3_STORAGE_REGION` | `us-east-005` | | |
| `KUN_VISUAL_NOVEL_S3_STORAGE_BUCKET_NAME` | `kun-galgame-patch` | | |
| `KUN_VISUAL_NOVEL_S3_STORAGE_ACCESS_KEY_ID` / `_SECRET_ACCESS_KEY` | `__SET_ME__` | (密钥) | B2 凭据,补丁下载需真值 |
| `CORS_ALLOW_ORIGINS` | `http://localhost:15011,http://127.0.0.1:15011` | | → 生产 `https://www.moyu.moe,https://moyu.moe` |
| `KUN_POSTS_DIR` | `/posts` | | about 页静态 `.mdx` 目录(已烤进 api 镜像) |

> moyu **没有 `JWT_SECRET`**;`KUN_VISUAL_NOVEL_EMAIL_*` 即使出现在 env 里,当前 Go API 也未使用(遗留)。

### 15.4.9 moyu · web(`docker/web.env`,**public 走运行期**)

| 变量 | 测试值 | 作用 |
|---|---|---|
| `NUXT_API_BASE_SSR` | `http://api:5214/api/v1` | **SSR** 内部 base(moyu 的 SSR 名是 `..._SSR`) |
| `NUXT_PUBLIC_API_BASE` | `http://localhost:15010/api/v1` | **浏览器** API → 生产 `https://www.moyu.moe/api/v1` |
| `NUXT_PUBLIC_OAUTH_SERVER_URL` | `http://localhost:15005/api/v1` | OAuth API → `https://oauth.kungal.com/api/v1` |
| `NUXT_PUBLIC_OAUTH_WEB_URL` | `http://localhost:15008` | OAuth 前端跳转 → `https://oauth.kungal.com` |
| `NUXT_PUBLIC_OAUTH_CLIENT_ID` | `df3ff6008d740bfacbe46aa8cf483cf2` | OAuth 客户端 |
| `NUXT_PUBLIC_OAUTH_REDIRECT_URI` | `http://localhost:15011/auth/callback` | 回调 → `https://www.moyu.moe/auth/callback` |

---

## 15.5 前端 public 配置:infra 烤镜像 vs 下游运行期

**两套机制,务必分清**(否则改了域名不生效):

| | **infra web / wiki** | **kungal / moyu web** |
|---|---|---|
| public 值从哪来 | **构建期烤进镜像**(`PUBLIC_*` build args → `nuxt build`) | **运行期** `docker/web.env` 的 `NUXT_PUBLIC_*`(Nitro 覆盖 runtimeConfig) |
| 为什么 | `oauthClientID`/`oauthRedirectURI` 的运行期 env 名映射别扭,索性 build 时定死 | 标准 Nitro 覆盖,**一次构建到处部署** |
| 生产改域名怎么做 | 改 `.github/workflows/build.yml` 的 `build-args` → **重新构建推镜像** → Dokploy 拉新镜像 | 改 `docker/web.env` 的 `NUXT_PUBLIC_*` → **重启容器**即可,无需重构 |
| 运行期还注入什么 | 仅 SSR 内部 base(`NUXT_API_BASE_SSR` / `NUXT_AUTH_API_BASE_SSR`) | SSR base 也在 web.env(`NUXT_API_BASE_SSR` / kungal `NUXT_API_BASE_URL`)|

**双 base 原则(三仓一致)**:SSR(容器内)永远用**服务名**(`http://oauth:9277`、`http://api:2334`…),
浏览器永远用**公网域名**。Dokploy 下没有宿主端口,这套正好契合(详见 [12-dokploy §12.5](./12-dokploy.md))。

---

## 15.6 构建参数(build args / Dockerfile `ARG`)

纯构建期、烤进镜像。生产由 CI 传([§15.7](#157-github-actions-ci-变量与-secrets));本地由 dev compose 传。

| 仓 | Dockerfile | `ARG` | 默认 | 作用 |
|---|---|---|---|---|
| infra | `go.Dockerfile` | `GO_VERSION` / `CMD` | `1.25` / `oauth` | Go 版本 / 选哪个 `cmd/`(galgame、migrate、migrate-galgame…) |
| infra | `cgo.Dockerfile` | `GO_VERSION` / `CMD` | `1.25` / `image` | cgo(libwebp);`CMD=oauth` 或 `image` |
| infra | `nuxt.Dockerfile` | `NODE_VERSION` / `APP` / `PUBLIC_*`×6 | `24` / `web` / 空 | Node 版本 / `web`\|`wiki` / 前端 public(见 [§15.5](#155-前端-public-配置infra-烤镜像-vs-下游运行期)) |
| kungal | `go.Dockerfile` | `GO_VERSION` / `CMD` | `1.26` / `server` | `server` 或 `migrate` |
| kungal | `nuxt.Dockerfile` | `NODE_VERSION` / `API_BASE_URL` / `OAUTH_*` / `GALGAME_WIKI_URL` / `KUN_GALGAME_URL` | `24` / 空 | **注意 kungal 不用 `PUBLIC_` 前缀**;且 CI 不传(空)→ 全靠运行期 web.env 覆盖 |
| moyu | `go.Dockerfile` | `GO_VERSION` / `CMD` | `1.26` / `server` | `server` 或 `migrate` |
| moyu | `nuxt.Dockerfile` | `NODE_VERSION` / `APP` / `PUBLIC_*`×6 | `24` / `web` / 空 | `PUBLIC_UMAMI_ID` 等;CI 仅传 `APP=web` → public 靠运行期 web.env |

镜像运行期 `ENV`(已在 Dockerfile 定死,无需设):Nuxt 前端 `NODE_ENV=production HOST=0.0.0.0`、
`NITRO_PORT`(infra/moyu `3000`、kungal `7777`);Go distroless 无 shell,无额外 ENV。

---

## 15.7 GitHub Actions(CI)变量与 secrets

三仓各有 `.github/workflows/build.yml`:push 到 main → 构建矩阵镜像 → 推 GHCR → (可选)触发 Dokploy 重部署。

**Secrets(GitHub → 仓库 Settings → Secrets and variables → Actions)**:

| Secret | 三仓 | 必需 | 作用 |
|---|---|---|---|
| `GITHUB_TOKEN` | 全部 | **内置**(无需手动建) | 登录 GHCR 推镜像(workflow 已声明 `packages: write`)|
| `DOKPLOY_WEBHOOK_INFRA` | infra | 可选 | 推完镜像后 POST 它触发 Dokploy 拉取重部署;**不设则跳过**(CI 仍成功)|
| `DOKPLOY_WEBHOOK_KUNGAL` | kungal | 可选 | 同上 |
| `DOKPLOY_WEBHOOK_MOYU` | moyu | 可选 | 同上 |

> GHCR 用内置 `GITHUB_TOKEN` + `github.actor` 登录,**不需要 PAT**。镜像 tag:`:latest` 和 `:<git-sha>`。

**CI 烤进镜像的 build-args(只有 infra 前端真正在 CI 烤公网域名)**:

| 镜像 | CI build-args |
|---|---|
| `infra-oauth` / `infra-image` | `CMD=oauth` / `CMD=image` |
| `infra-galgame` / `infra-migrate` / `infra-migrate-galgame` | `CMD=galgame` / `migrate` / `migrate-galgame` |
| **`infra-web`** | `APP=web`、`PUBLIC_API_BASE=https://oauth.kungal.com/api/v1`、`PUBLIC_IMAGE_CDN_BASE=https://image.kungal.iloveren.link` |
| **`infra-wiki`** | `APP=wiki`、`PUBLIC_API_BASE=https://wiki.kungal.com/api`、`PUBLIC_AUTH_API_BASE=https://oauth.kungal.com/api/v1`、`PUBLIC_OAUTH_AUTHORIZE_BASE=https://oauth.kungal.com/api/v1`、`PUBLIC_OAUTH_CLIENT_ID=galgame-wiki-admin`、`PUBLIC_OAUTH_REDIRECT_URI=https://wiki.kungal.com/auth/callback`、`PUBLIC_IMAGE_CDN_BASE=https://image.kungal.iloveren.link` |
| `kungal-api` / `kungal-migrate` / `kungal-web` | `CMD=server` / `CMD=migrate` / (web 无,public 走运行期) |
| `moyu-api` / `moyu-migrate` / `moyu-web` | `CMD=server` / `CMD=migrate` / `APP=web`(public 走运行期) |

> **改 infra 前端域名 = 改 build.yml 这两行再重构**(因为是烤进去的);改 kungal/moyu 前端域名 = 改各自 `docker/web.env` 重启即可。

---

## 15.8 生产 / Dokploy 注入方法

生产用 [Dokploy](./12-dokploy.md):3 个 Compose 应用,共享 `dokploy-network`,镜像走 GHCR 预构建。
环境变量分两条注入路径:

### A. `${VAR:?}` 插值 → Dokploy 的 **Environment**(仅 infra 应用)

infra 的 `docker-compose.prod.yml` 用了 4 个必填插值。在 **infra 这个 Dokploy 应用的 Environment** 里填:

```env
POSTGRES_PASSWORD=<强随机>          # = 各 KUN_PG_PASSWORD / KUN_DATABASE_URL 密码
MINIO_ROOT_USER=<自定义>            # 自托管图床才需要;用 R2 可随便填
MINIO_ROOT_PASSWORD=<强随机>
MEILI_MASTER_KEY=<强随机>           # = galgame KUN_MEILISEARCH_API_KEY
# 可选:POSTGRES_USER(默认 postgres)
```

> Dokploy 把 Environment 写成部署目录的根 `.env`,`docker compose up` 时插值。`:?` 表示**留空直接报错**,不会静默用空密码。

### B. `env_file: docker/*.env` → 文件必须在部署目录(三仓均 **gitignore**)

三仓生产 compose 都 `env_file: ./docker/*.env`,这些文件**不在 git 里**(`.gitignore`),Dokploy
克隆仓库时**不会带它们**。所以 **A 步在 Environment 里填 `${VAR}` 并不能替代它们**——`${VAR}` 只解决 compose
插值那几个基础设施密码;Go/Nuxt 服务的全部业务配置在 `env_file` 里,文件缺了 compose 直接报 `env file ... not found`。三种补法:

1. **(推荐)Dokploy「Environment / Env File」面板**:把每个服务该有的 `KEY=value` 贴进去,
   或用 Dokploy 的文件挂载功能把内容写到 `docker/oauth.env` 等路径。
2. **部署机手动放**:SSH 到服务器,在 Dokploy 的应用代码目录补齐 `docker/*.env`(适合一次性)。
3. **私有配置仓**:把脱敏后的 env 模板入一个私有仓 / secret 管理器,部署时拉取生成。

> 不论哪种,**§15.3 的一致性铁律照样要满足**:`docker/*.env` 里的库密码/Meili key 必须等于 A 步在 Environment 里设的 `${VAR}`。
>
> ⚠️ **历史坑(已修)**:infra 仓曾**误把** `docker/oauth.env`/`image.env`/`galgame.env` 提交进版本库
> (`.gitignore` 没覆盖到 `*.env` 这种非点开头文件名)。后果是:即便你在 Dokploy Environment 填了新密钥,
> infra 服务仍会从仓库里那份**旧 env_file** 读到测试值/泄露值。现已 `git rm --cached` 移出跟踪并补全 `.gitignore`;
> **若你的远端历史里还有这些文件,请把里面所有出现过的密钥(邮箱密码、JWT、S3 等)视为已泄露并轮换**。

### C. 顺序与首启

先部署 **infra**(等 pg/redis/minio/meili healthy)→ 在 Dokploy Terminal 跑 `migrate` / `migrate-galgame`
→ 再部署 **kungal**、**moyu**(它们连 infra 的服务名)。详见 [12-dokploy §12.4/§12.6](./12-dokploy.md) 与 [03-bootstrap.md](./03-bootstrap.md)。

> **完整数据 cutover 需要更多 job 镜像**:`migrate` / `migrate-galgame` 只够「空库起服务」。
> 带数据上线([03-bootstrap §B](./03-bootstrap.md))还要 `migrate-users`、`migrate-galgame-data`、
> `migrate-moyu-galgame`、`dedup-galgame-alias`、`reindex-search` 等——这些是**独立的 `cmd/` 二进制**,
> 而 Dockerfile 一镜像只编一个 `CMD`,**不能**用 `infra-galgame` 镜像 `--entrypoint migrate-users`。
> CI 现已额外发布 **`infra-tools` / `kungal-tools` / `moyu-tools`** 全量工具镜像([13-registry-ci.md](./13-registry-ci.md)),
> 用 `docker run ... ghcr.io/kunmoe/infra-tools <job-name>` 跑这些一次性迁移,**不依赖生产机临时 build / go run**。

---

## 15.9 Cloudflare 配置

Cloudflare 在本套里担三个角色,**只有图床真正涉及环境变量**:

### A. DNS + 代理(控制台,无 env)

把 [12.1 域名表](./12-dokploy.md) 的 A/AAAA 记录指向**服务器公网 IP**,**开橙云代理(Proxied)**。
代理隐藏源 IP + 抗 DDoS;配合服务器 ufw 只放行 Cloudflare 段 + SSH,防止绕过代理直连源站(见 [NOTES.md](./NOTES.md))。

> **不用 Cloudflare Tunnel**:单 `cloudflared` 在高并发下会出 **Error 1033**;新版改用 **Dokploy(Traefik)+ CF CDN 橙云**。
> ([11-edge-cloudflare-tunnel.md](./11-edge-cloudflare-tunnel.md) 仅作 NAT/无法开入站时的退路。)

### B. R2 图床(涉及 env)

生产图片 blob 走 **Cloudflare R2**(S3 兼容),自定义域 `image.kungal.iloveren.link` 由 R2 直供,**不经服务器**:

| R2 侧 | 对应 env(infra `oauth.env` / `image.env`) |
|---|---|
| R2 endpoint `https://<account>.r2.cloudflarestorage.com` | `KUN_IMAGE_S3_ENDPOINT` |
| Region(R2 用 `auto`) | `KUN_IMAGE_S3_REGION` |
| R2 API token 的 Access Key ID / Secret | `KUN_IMAGE_S3_ACCESS_KEY` / `KUN_IMAGE_S3_SECRET_KEY`(密钥) |
| 桶名 | `KUN_IMAGE_S3_BUCKET` |
| 自定义域 | 各服务 `KUN_IMAGE_PUBLIC_BASE_URL` / moyu `KUN_IMAGE_CDN_BASE` = `https://image.kungal.iloveren.link` |

> R2 通常 `KUN_IMAGE_S3_FORCE_PATH_STYLE` 可设 false;自托管 MinIO 才必须 true。用 R2 后 infra 的 `minio` 容器可空跑。

### C.(可选 / 遗留)Cloudflare 缓存清除

kungal 旧 `apps/web/.env` 里有 `KUN_CF_CACHE_ZONE_ID` / `KUN_CF_CACHE_PURGE_API_TOKEN`(发图后清 CDN 缓存)。
当前容器化运行流程**不依赖**它;若启用主动清缓存才需在对应运行环境提供(密钥,勿入仓)。

---

## 15.10 生产必改 / 必轮换清单

上线前逐项确认(`191007` / `minioadmin` / `kun-docker-test-*` 这类测试值**一个都不能留**):

- [ ] **Postgres**:`POSTGRES_PASSWORD`(Dokploy Env)+ 所有 `KUN_PG_PASSWORD` / `KUN_DATABASE_URL` 密码 —— **同一个强随机**
- [ ] **JWT**:infra 三服务的 `JWT_SECRET` 换同一个强随机;kungal 的 `JWT_SECRET` 单独换强随机
- [ ] **Meili**:`MEILI_MASTER_KEY`(Dokploy Env)= galgame `KUN_MEILISEARCH_API_KEY` = kungal `MEILISEARCH_KEY`
- [ ] **MinIO / R2**:用 R2 → 填 R2 凭据(`KUN_IMAGE_S3_*`);自托管 → 换 `MINIO_ROOT_*`
- [ ] **OAuth**:每个 client 的 secret 重新生成(枢纽存 `sha256:`,下游 env 填明文);`redirect_uris` 改 https 域名([12.3](./12-dokploy.md))
- [ ] **下游 S3/B2**:moyu `KUN_VISUAL_NOVEL_S3_*`(B2 补丁)、kungal `FILE_STORAGE_*`(B2 档案)填真值
- [ ] **SMTP**:`KUN_VISUAL_NOVEL_EMAIL_PASSWORD` / `MAIL_PASSWORD` 换真值
- [ ] **CORS**:三仓 `*CORS*ORIGIN*` 改成真实 https 域名(去掉 localhost)
- [ ] **前端域名**:infra 改 build.yml build-args 重构;kungal/moyu 改 `docker/web.env` 的 `NUXT_PUBLIC_*`
- [ ] **Cloudflare**:DNS 指源站 + 开橙云;ufw 只放 CF 段 + SSH

> 生产更稳妥:用 `docker secret` / 外部 vault 代替明文 `env_file`;`docker/*.env` 已被 `.dockerignore` + `.gitignore` 双重挡住。

---

## 15.11 最小可用集(最快跑通要设的)

只想**本地一把梭跑起来**,改这几处即可(其余用仓库默认):

1. **infra**:`docker/oauth.env`、`image.env`、`galgame.env` 已带测试值,直接 `docker compose up -d` 即可。
2. **注册 3 个 OAuth client**(否则前端登录走不通),把生成的 secret 回填下游 `OAUTH_CLIENT_SECRET`(见 [03-bootstrap §A.5](./03-bootstrap.md))。
3. **下游连 infra**:确认 kungal/moyu `docker/api.env` 的库密码 = `191007`、`MEILISEARCH_KEY` / s2s base 用服务名(仓库默认已对)。
4. 图床想真传图:填 `KUN_IMAGE_S3_*`(本地 minio 用 `minioadmin`)。

上生产则按 [§15.10](#1510-生产必改--必轮换清单) 全量替换 + [§15.8](#158-生产--dokploy-注入方法) 注入。

---

**相关**:[05-configuration.md](./05-configuration.md)(上线速查)· [12-dokploy.md](./12-dokploy.md)(编排)·
[13-registry-ci.md](./13-registry-ci.md)(CI/GHCR)· [07-troubleshooting.md](./07-troubleshooting.md)(配错时的现象)· [NOTES.md](./NOTES.md)(踩坑速查)
