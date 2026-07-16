# kun-galgame-infra

> 鲲 Galgame 生态的**枢纽(hub)** —— 统一身份(OAuth)、图床(image service)、galgame-wiki,并**拥有全生态共享的基础设施**。
>
> 原名 `kun-oauth-admin`,现更名为 `kun-galgame-infra` 以反映其"生态基础设施枢纽"的定位。

## 这是什么

本仓是鲲 Galgame 生态**三仓**中的**枢纽**,对内对外提供:

- **身份中心** —— 自建 **OAuth 2.0 授权服务器**(授权码 + PKCE),生态内所有站点的统一登录与用户/积分(moemoepoint)体系。
- **图床(image service)** —— 图片上传、处理(WebP)、分发。
- **galgame-wiki** —— galgame 元数据的权威来源(VNDB 同步、标签、厂商等)。
- **共享基础设施的拥有者** —— 一套 **Postgres(5 库)/ Redis / MinIO(S3)/ Meilisearch**;下游站点按服务名连过来,不各自再起一套。

## 生态

| 仓库 | 代号 | 角色 |
|---|---|---|
| **kun-galgame-infra**(本仓) | infra / 枢纽 | 身份 + 图床 + wiki + 共享基础设施 |
| [kun-galgame-forum](https://github.com/KunMoe/kun-galgame-forum) | kungal | 论坛主站 |
| [kun-galgame-patch](https://github.com/KunMoe/kun-galgame-patch) | moyu | 补丁站 |

下游(kungal / moyu)在运行时通过容器**服务名**调用枢纽:`oauth:9277`、`catalog:9281`(galgame-wiki 面自 W3 起由 catalog 服务承载)、`image:9278`,并共用枢纽的 Postgres / Redis / MinIO / Meili。

## 架构

**可部署服务**(均无状态;Go 多阶段编译,Nuxt 出自包含 `.output`):

| 服务 | 容器端口 | 说明 |
|---|---|---|
| `oauth` | 9277 | OAuth 授权服务器 + 用户 / moemoepoint(cgo:内嵌图床 admin) |
| `image` | 9278 | 图床服务(cgo + libwebp) |
| `artifact` | 9279 | 大文件(补丁)服务(纯 Go) |
| `catalog` | 9281 | 跨媒介目录 + **galgame-wiki API**(纯 Go;:9280 独立 galgame 服务已于 wiki 退役 W3/W5 退休) |
| `community` | 9282 | 社区原语服务(纯 Go) |
| `trust` | 9283 | Trust & Safety 平台(纯 Go) |
| `ai` | 9284 | AI 网关语义层(纯 Go) |
| `web` | 3000 | 管理端前端(Nuxt 4) |
| `wiki` | 3000 | galgame-wiki 前端(Nuxt 4) |
| `developer` | 3000 | NextMoe 开发者门户(Nuxt 4) |

**共享基础设施**(本仓 compose 定义一次):Postgres、Redis、MinIO、Meilisearch。

**一套 Postgres 承载全生态 5 个库**:`kun_galgame_infra`(oauth/用户)、`kun_galgame_wiki`、`kun_images`、`kungalgame`(下游论坛)、`kungalgame_patch`(下游补丁)。

## 技术栈

- **后端**:Go 1.25 + [Fiber](https://gofiber.io/),**单模块多二进制**(`cmd/*`)。`oauth`/`image` 因 `go-webp` 走 cgo(debian-slim + libwebp),其余纯 Go(distroless)。
- **前端**:Nuxt 4 SSR(Nitro `node-server`)+ TypeScript,两个应用共享 **`@kun/ui`** Nuxt layer。
- **数据**:PostgreSQL · Redis · MinIO(S3 兼容)· Meilisearch(全文搜索)。
- **工程**:pnpm 10 workspace(monorepo)· Docker Compose · GitHub Actions(CI→GHCR)。

## 仓库结构

```
apps/
  api/              Go Fiber 后端(多二进制)
    cmd/            入口与工具:oauth / image / catalog / artifact / trust … + migrate-* / sync-vndb* / reindex-search …
    internal/       app(装配)· platform(领域)· infrastructure(db/redis/s3 客户端)· jobs · middleware
  web/              管理端前端(Nuxt 4,extends @kun/ui)
  wiki/             galgame-wiki 前端(Nuxt 4,extends @kun/ui)
packages/
  ui/               @kun/ui —— 共享 Nuxt UI layer(组件 / 色系 / 样式)
  image-client/     @kun/image-client —— 图床客户端
docker/             Dockerfile(cgo / go / nuxt)+ 各服务 env + initdb.d(建 5 库)
docs/               文档(deploy / migration / api / galgame_wiki / image_service …)
scripts/            运维脚本(reset_all.sh)+ 源库 dump
```

## 快速开始

### 本地开发(一条命令)

```bash
pnpm install
pnpm dev        # 平台底座(docker-compose.dev.yml:redis/minio/meili/mailpit/迁移/community/ai)
                # + air 热重载五个常改 Go 服务(oauth/catalog/image/artifact/trust)+ Nuxt 前端
```

完整模型(镜像拉取、Replace 模式、数据脱敏快照)见 [docs/dev-environment.md](./docs/dev-environment.md);
`pnpm dev:full` = 全平台纯镜像(开发下游产品仓时用),`pnpm dev:down` 停底座。

### 生产

生产用 `docker-compose.prod.yml`(Dokploy + GHCR 预构建镜像;迁移 job 随每次部署自动跑)。

## 部署

- **快速上线**:[docs/deploy/QUICKSTART.md](./docs/deploy/QUICKSTART.md) —— 全新 Debian 服务器到三站上线的精简步骤(**Dokploy**:内置 Traefik 反代 + 自动 HTTPS)。
- **线上方案**:单服务器 + Dokploy,镜像由 **CI 构建推 GHCR**、生产机零构建。完整分章(架构 / 构建 / 首启 / 运维 / 排错 / Dokploy / Registry-CI / 备份还原 / 源站 IP 防泄漏)见 [docs/deploy/README.md](./docs/deploy/README.md)。
- **备份与还原**:[docs/deploy/14-backup-restore.md](./docs/deploy/14-backup-restore.md)。

## 开发规范

前端约定(UI 组件、页面/组件拆分、常量与类型位置、自定义色系、箭头函数等)见 [CLAUDE.md](./CLAUDE.md)。
