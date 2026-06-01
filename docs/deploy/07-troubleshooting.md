# 7 · 故障排查

下面每一条都是**本次实跑中真实踩到的**,按「现象 → 原因 → 解法」整理。

## 构建期

### B1 · oauth/image 构建报 `build constraints exclude all Go files`
- **现象**:`CGO_ENABLED=0 go build ./cmd/oauth`(或 image)失败,提示 `kolesa-team/go-webp/encoder ... build constraints exclude all Go files`。
- **原因**:go-webp 是 cgo-only(无纯 Go 回退)。`image` 编码 WebP 直接用它;**`oauth` 经内嵌的图床 admin 间接 import**(`image/service` → `processor` → go-webp)。
- **解法**:这两个用 `docker/cgo.Dockerfile`(`CGO_ENABLED=1` + 构建期 `libwebp-dev` + 运行期 `libwebp7`),**不要**走 `go.Dockerfile`。`galgame` 和迁移工具纯 Go,继续用 `go.Dockerfile`。判定:`go list -deps ./cmd/X | grep go-webp`,非空即需 cgo。

### B2 · `the --mount option requires BuildKit`
- **原因**:本机无 `docker buildx`,`docker compose build` 用 legacy builder,不支持 `--mount=type=cache`。
- **解法**:三仓 Dockerfile 已移除 cache mount(仅普通层缓存)。装了 buildx 可自行加回。构建时的 `requires buildx plugin` 警告无害。

### B3 · 运行镜像构建报 `Unable to locate package libsharpyuv0`
- **原因**:Debian **bookworm** 的 libwebp 1.2.x 把 sharpyuv 打包进 `libwebp7`,没有独立 `libsharpyuv0` 包。
- **解法**:运行阶段只装 `libwebp7`(`cgo.Dockerfile` 已修正)。

### B4 · Nuxt 构建报 `packages/ui/.nuxt/tsconfig.app.json ... no such file`
- **原因**:`@kun/ui` 是 Nuxt **layer**,需自己的 `.nuxt`;但 `pnpm install --ignore-scripts` 跳过了它的 `prepare`,且 `.dockerignore` 剥掉了主机的 `.nuxt`。
- **解法**:`nuxt.Dockerfile` 在 app 构建前先 `pnpm --filter @kun/ui run prepare`。(`--ignore-scripts` 本身是必须的——deps 阶段还没拷源码,apps 的 `postinstall: nuxt prepare` 会失败。)

## kungal 专属

### K1 · `service "api" depends on undefined service "redis": invalid compose project`
- **现象**:在 kungal 目录直接 `docker compose build/up` 即报错,连构建都进不去。
- **原因**:kungal 主 compose 在 `depends_on` 引用了 `postgres`/`redis`,但**不定义**它们,也没声明外部网络。
- **解法**:必须叠加一个提供/外置 pg+redis 的 compose:
  - 连 hub:`-f docker-compose.yml -f docker-compose.hub.yml`(本仓库已附,`!reset` 清掉 depends_on + 接 `kun-oauth-admin_default`)。
  - 自测:`-f docker-compose.yml -f docker-compose.standalone.yml`。

### K2 · kungal api 启动即退出(无明显日志)
- **原因**:`OAUTH_CLIENT_ID` / `OAUTH_CLIENT_SECRET` 是 `requireEnv`,**空则 fail-fast**。kungal 仓自带的 `api.env` 这俩是空的(standalone 取向)。
- **解法**:填非空值(`kungal-web` / 注册时的 secret)。见 [05-configuration.md](./05-configuration.md)。

### K3 · kungal api 连不上库 / 搜索 403
- **原因**:kungal 自带 `api.env` 是 standalone 配置——DB 密码 `kungal_dev_pw`(hub 是 `191007`)、`MEILISEARCH_KEY` 空(hub meili 有 master key → 403)。
- **解法**:接 hub 时把密码改 `191007`、`MEILISEARCH_KEY` 填共享 master key、`JWT_SECRET` 填共享密钥。

## 跨仓 / 基础设施

### I1 · galgame 日志 `EnsureIndexes failed ... Unknown field 'disableOnNumbers'`
- **原因**:`meilisearch-go` 客户端发了新字段,**Meili 版本过旧**(<1.13)。
- **解法**:Meili 用 `v1.20`(≥1.13)。注:该错误**非致命**,galgame 仍健康,只是搜索设置没生效。

### I2 · Meili 起不来,崩溃循环 `incompatible database version` → 连带 `lookup meili: no such host`
- **现象**:升级 Meili 镜像后它反复重启;依赖它的服务报「解析不到 meili」。
- **原因**:Meili **不允许跨大版本直接复用旧数据卷**;崩溃循环时容器不在网络上 → DNS 名消失。
- **解法**:**开发**直接清卷重来:`docker compose rm -sf meili && docker volume rm kun-oauth-admin_meili && docker compose up -d meili`;**生产**按官方指引 dump→升级→import。

### I3 · kungal 连 `meilisearch` 解析不到
- **原因**:kungal 用服务名 `meilisearch`,hub 的服务叫 `meili`。
- **解法**:hub 的 meili 已加网络别名 `meilisearch`(`networks.default.aliases`),两个名都解析到同一实例。

### I4 · 下游报「数据库不存在」/ 服务起来但业务接口 500
- **原因 a**:`kungalgame` / `kungalgame_patch` 没建。initdb 脚本**只在数据卷首次初始化时跑一次**;复用旧卷不会补建。
  - **解法**:`docker exec ...postgres... psql -U postgres -c "CREATE DATABASE kungalgame" -c "CREATE DATABASE kungalgame_patch"`。
- **原因 b**:库是**空 schema**——各仓 `migrate` 是清理型,假设 dump 已恢复;空库上它打印「没有待执行的迁移」。健康端点 OK,但业务查询无表 → 报错。
  - **解法**:走完整数据 Bootstrap(恢复 dump + 跨仓迁移),见 [03-bootstrap.md](./03-bootstrap.md) B 节。

### I5 · OAuth 登录跳转后报错 / 拿不到令牌
- **原因**:对应 OAuth client 没注册到枢纽(client 不在任何 migrate 种子里)。
- **解法**:在 hub 管理端注册 client 或入 `oauth_clients` 表,secret 按 `sha256:` 哈希存,并让下游 `OAUTH_CLIENT_SECRET` 等于明文。见 [03-bootstrap.md](./03-bootstrap.md) A.5。

### I6 · 容器起来了但外部 curl 不通(连接拒绝)
- **原因**:服务绑了 `127.0.0.1`。
- **解法**:容器内必须绑 `0.0.0.0`(`KUN_FIBER_SERVER_HOST=0.0.0.0` / `KUN_IMAGE_SERVICE_HOST=0.0.0.0` / Nuxt `HOST=0.0.0.0`,均已在 env/Dockerfile 设好)。

### I7 · host 端口冲突
- **原因**:本机 `air` 开发服务占了 9277/9280 等。
- **解法**:整套 host 端口用 `1xxxx` 段(见 [00-architecture.md](./00-architecture.md) 端口表),与 dev 共存。

### I8 · 浏览器拿到的图 / API 地址是 `127.0.0.1:9277`(连不上)
- **原因**:前端 public 配置(`apiBase`/`imageCdnBase`)用了 in-config 默认值,没在 build 时烘焙正确的 host/公网地址。
- **解法**:构建时传 `PUBLIC_*` build args(或运行时 `NUXT_PUBLIC_*`),指向 host 端口 / 真实域名。见 [02-build.md](./02-build.md) + [05-configuration.md](./05-configuration.md)。

### I9 · 容器内 `go mod download` / 外呼超时,但宿主机能连(透明代理)
- **原因**:宿主机用了 dae 之类内核级透明代理,只代理本机流量,不代理 docker 网桥的**转发**流量。**仅开发机**问题。
- **解法**:见附录 [08-dae-dev-proxy.md](./08-dae-dev-proxy.md)(dae override + `lan_interface`)。**生产机不涉及**。

## 一条命令快速体检

```bash
docker ps --format '{{.Names}}\t{{.Status}}' | grep -E 'kun-oauth-admin-|moyu-|kungal-' | sort
# 期望:13 个容器,Go/Nuxt 服务均 (healthy)
```
