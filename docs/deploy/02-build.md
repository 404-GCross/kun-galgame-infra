# 2 · 镜像构建

所有镜像的**构建 context 都是各自仓库根**(前端要把 `packages/ui` 这个 Nuxt layer 一起带进去)。

## Hub(kun-oauth-admin)

Hub 有 3 个 Dockerfile,因为它的二进制分两类:

| Dockerfile | 构建对象 | CGO | 基镜 |
|---|---|---|---|
| `docker/go.Dockerfile` | `galgame` + 所有 `migrate-*`/worker | 0 | distroless |
| `docker/cgo.Dockerfile` | **`oauth` + `image`** | 1 | debian-slim + libwebp |
| `docker/nuxt.Dockerfile` | `web` + `wiki` | — | node-slim |

> **为什么 oauth 也要 cgo**:`oauth` 内嵌图床 admin 端点,其 `image/service` import 了 WebP `processor`(`kolesa-team/go-webp` → libwebp 的 cgo 绑定)。用 `go list -deps ./cmd/oauth | grep go-webp` 可验证。`galgame` 和迁移工具不碰 processor,故纯 Go。

一键构建:

```bash
cd kun-oauth-admin
docker compose build          # oauth image galgame web wiki + migrate jobs
```

参数化单独构建(go/cgo 用 `CMD`,nuxt 用 `APP`):

```bash
docker build -f docker/go.Dockerfile  --build-arg CMD=galgame -t kun-oauth-admin/galgame .
docker build -f docker/cgo.Dockerfile --build-arg CMD=oauth   -t kun-oauth-admin/oauth .
docker build -f docker/nuxt.Dockerfile --build-arg APP=wiki   -t kun-oauth-admin/wiki .
```

## moyu(kun-galgame-patch-next)

纯 Go(单 `server` 二进制 + 迁移/同步工具)+ Nuxt。无 cgo。

```bash
cd kun-galgame-patch-next
docker compose build          # api web(+ migrate job)
```

## kungal(kun-galgame-nuxt4)

⚠️ **kungal 的 `docker-compose.yml` 单独无法构建**——它在 `depends_on` 里引用了未定义的 `postgres`/`redis`,直接 `docker compose build` 会报 `invalid compose project`。必须叠加一个定义/外置了 pg+redis 的 compose:

```bash
cd kun-galgame-nuxt4
# 连已运行的 hub(推荐):
docker compose -f docker-compose.yml -f docker-compose.hub.yml build
# 或 自带 pg/redis 自测:
docker compose -f docker-compose.yml -f docker-compose.standalone.yml build
```

## Go 版本

- hub:`golang:1.25-bookworm`(go.mod `go 1.25.x`)。
- moyu / kungal:`golang:1.26-bookworm`(它们的 go.mod 更新;Dockerfile `ARG GO_VERSION=1.26`)。

## 前端 public 配置(构建期烘焙)

Nuxt 的 `runtimeConfig.public`(apiBase、OAuth client、image CDN)在 `nuxt.config.ts` 里读的是**自定义 `KUN_*` env 名**,只在 **build 期**生效。因此各仓 compose 在 `build.args` 里以 `PUBLIC_*` 传入并烘焙进镜像。例如 hub wiki:

```yaml
args:
  APP: wiki
  PUBLIC_API_BASE: http://localhost:19280/api
  PUBLIC_OAUTH_AUTHORIZE_BASE: http://localhost:19277/api/v1
  PUBLIC_OAUTH_CLIENT_ID: galgame-wiki-admin
  PUBLIC_OAUTH_REDIRECT_URI: http://localhost:19421/auth/callback
  PUBLIC_IMAGE_CDN_BASE: http://localhost:19000/kun-images
```

> 想「一次构建、运行时改配置」:不传 `PUBLIC_*`,改在容器运行时设 Nitro 约定名 `NUXT_PUBLIC_*`。但 `oauthClientID`/`oauthRedirectURI` 这类驼峰键到 env 名的映射有歧义,所以本部署默认走 build 期烘焙(各 web 的 host 地址固定,烘焙无碍)。详见 [05-configuration.md](./05-configuration.md)。

## 构建产物体积参考(实测)

```
distroless(galgame/migrate/kungal-api/moyu-api)   24–45 MB
cgo-slim   (oauth/image)                            ~180 MB
nuxt       (各 web)                                 ~390 MB
```

## 构建期会看到、可忽略的告警

- `requires buildx plugin to be installed` —— legacy builder 回落,正常。
- `@nuxt/image: sharp binaries included for linux-x64. Make sure you deploy to the same architecture.` —— build+run 都在 linux-x64 容器,匹配。
- `@tailwindcss/vite ... Sourcemap is likely to be incorrect` —— 无害。
