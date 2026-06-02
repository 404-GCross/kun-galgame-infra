# 13 · 镜像 Registry + CI 构建(GHCR + GitHub Actions)

> 配合 [12-dokploy.md](./12-dokploy.md):**12 讲"怎么部署/路由"(Dokploy + Traefik + 域名),本篇讲"镜像从哪来"**。两者互补——Dokploy 负责拉镜像 + 反代 + 零宕机,GHCR + CI 负责在**别处**把镜像 build 好。

## 13.0 原则:不要在生产服务器上构建

Dokploy 官方明确:在部署服务器上 build "**可能导致服务器超时甚至冻结,所有应用宕机**"(Docker build 吃大量 RAM/CPU)。本生态镜像偏重——**oauth/image 是 cgo + libwebp**,每个前端是 **Nuxt 全量 build**,三仓合计 **13 个镜像**。在一台生产机上全 build = 单点风险。

**最佳实践(业界 + Dokploy 一致)**:
```
源码 push ─► CI(GitHub Actions)build 镜像 ─► 推送 registry(GHCR)
                                                   │ 触发 webhook / API
                                                   ▼
                            单服务器 Dokploy ─► 拉预构建镜像 ─► Traefik 零宕机滚动
```
生产机**零构建负载**,且天然带 **tag/回滚**。

## 13.1 选型结论

| 方案 | 何时用 | 评价 |
|---|---|---|
| **GHCR(GitHub Container Registry)** ✅ 首选 | 你们当前(已在 GitHub、单服务器) | 免费、原生集成 Actions(`GITHUB_TOKEN` 直接推)、**公开仓库镜像可设公开 → Dokploy 免凭证拉**、零额外基础设施 |
| 自托管 `distribution/registry:2` | 需私有 + 全自托管,且有**独立构建机/CI** | 轻量(单容器),但要自己加 **TLS + 认证 + GC**;若 build 仍在同一台生产机则**白搭**(没卸掉构建负载) |
| 自托管 **Harbor** | 多节点 / 需 RBAC、漏洞扫描、镜像签名、复制 | 功能全(CNCF 毕业),但**重**(core/db/redis/jobservice/registry/trivy 多容器),单台生产机不划算 |
| Dokploy **Build Server** | 不想用 GitHub Actions、想全自托管构建 | 独立 build VPS → 推 registry → 部署机拉(官方:"用 build server 时 registry 必需") |

**决定:现在上 GHCR + GitHub Actions;自托管 registry / Harbor 留到"多节点或需私有+扫描"时再说。**

## 13.2 镜像清单

CI 按各仓**现有 Dockerfile**(参数化)构建以下镜像并推到 `ghcr.io/kun1007/<name>`(GHCR 名必须小写):

| 镜像 `ghcr.io/kun1007/…` | 仓库 | Dockerfile | 关键 build-arg | 容器端口 |
|---|---|---|---|---|
| `hub-oauth` | infra | `docker/cgo.Dockerfile` | `CMD=oauth` | 9277 |
| `hub-image` | infra | `docker/cgo.Dockerfile` | `CMD=image` | 9278 |
| `hub-galgame` | infra | `docker/go.Dockerfile` | `CMD=galgame` | 9280 |
| `hub-web` | infra | `docker/nuxt.Dockerfile` | `APP=web` | 3000 |
| `hub-wiki` | infra | `docker/nuxt.Dockerfile` | `APP=wiki` | 3000 |
| `hub-migrate` | infra | `docker/go.Dockerfile` | `CMD=migrate` | —(一次性) |
| `hub-migrate-galgame` | infra | `docker/go.Dockerfile` | `CMD=migrate-galgame` | —(一次性) |
| `kungal-api` | nuxt4 | `docker/go.Dockerfile` | `CMD=server` | 2334 |
| `kungal-web` | nuxt4 | `docker/nuxt.Dockerfile` | `APP=web` | 7777 |
| `kungal-migrate` | nuxt4 | `docker/go.Dockerfile` | `CMD=migrate` | —(一次性) |
| `moyu-api` | patch-next | `docker/go.Dockerfile` | `CMD=server` | 5214 |
| `moyu-web` | patch-next | `docker/nuxt.Dockerfile` | `APP=web` | 3000 |
| `moyu-migrate` | patch-next | `docker/go.Dockerfile` | `CMD=migrate` | —(一次性) |

> 基础设施 `postgres`/`redis`/`minio`/`meili` 用上游官方镜像,不进 CI。

## 13.3 Tag 与回滚约定

每次构建打**两个 tag**:
- **`:<git-sha>`** —— 不可变,精确回滚锚点。
- **`:latest`**(或 `:prod`)—— 移动标签,Dokploy 监听并拉取。

**回滚** = 把 Dokploy 的镜像引用从 `:latest` 临时改成某个已知良好的 `:<git-sha>` 再 redeploy(或在 prod compose 里 pin sha)。

## 13.4 GitHub Actions workflow

每仓放一个 `.github/workflows/build.yml`。下面是 **hub(最复杂,cgo + 2×Nuxt + Go)** 的完整示例;kungal/moyu **同构**,仅 `matrix` 列表不同。

```yaml
# kun-galgame-infra/.github/workflows/build.yml
name: build-and-push
on:
  push:
    branches: [main]
permissions:
  contents: read
  packages: write          # 推 GHCR 必需
concurrency:                # 同分支新 push 取消旧构建
  group: build-${{ github.ref }}
  cancel-in-progress: true

jobs:
  build:
    runs-on: ubuntu-latest
    strategy:
      fail-fast: false
      matrix:
        include:
          - { name: hub-oauth,           file: docker/cgo.Dockerfile,  args: "CMD=oauth" }
          - { name: hub-image,           file: docker/cgo.Dockerfile,  args: "CMD=image" }
          - { name: hub-galgame,         file: docker/go.Dockerfile,   args: "CMD=galgame" }
          - { name: hub-migrate,         file: docker/go.Dockerfile,   args: "CMD=migrate" }
          - { name: hub-migrate-galgame, file: docker/go.Dockerfile,   args: "CMD=migrate-galgame" }
          - { name: hub-web,             file: docker/nuxt.Dockerfile, args: "APP=web" }
          - { name: hub-wiki,            file: docker/nuxt.Dockerfile, args: "APP=wiki" }
    steps:
      - uses: actions/checkout@v4
      - uses: docker/setup-buildx-action@v3
      - uses: docker/login-action@v3
        with:
          registry: ghcr.io
          username: ${{ github.actor }}
          password: ${{ secrets.GITHUB_TOKEN }}
      - uses: docker/build-push-action@v6
        with:
          context: .
          file: ${{ matrix.file }}
          build-args: ${{ matrix.args }}
          push: true
          tags: |
            ghcr.io/kun1007/${{ matrix.name }}:latest
            ghcr.io/kun1007/${{ matrix.name }}:${{ github.sha }}
          cache-from: type=gha,scope=${{ matrix.name }}
          cache-to: type=gha,mode=max,scope=${{ matrix.name }}

  deploy:                    # 全部镜像就绪后通知 Dokploy 拉取重部署
    needs: build
    runs-on: ubuntu-latest
    steps:
      - run: curl -fsS -X POST "${{ secrets.DOKPLOY_WEBHOOK_HUB }}"
```

- **cgo 镜像**(oauth/image)在 `ubuntu-latest` 上正常 build —— cgo 发生在 build 容器内(`docker/cgo.Dockerfile` 的 debian-slim + libwebp),runner 无需特殊配置。
- **公开仓库 Actions 分钟免费**;`type=gha` 层缓存让二次构建快很多。
- kungal/moyu 的 workflow:`matrix` 换成 `kungal-api`/`kungal-web`/`kungal-migrate`(及 moyu 同理),`deploy` 步骤用各自的 `DOKPLOY_WEBHOOK_*`。

## 13.5 前端域名配置:运行时优先(build-once)

Nuxt 的 public 配置有两种注入方式,直接影响"镜像是否环境无关":

- **运行时 `NUXT_PUBLIC_*` env(推荐)** —— kungal/moyu 的 web 已用 `docker/web.env` 的 `NUXT_PUBLIC_*`(Nuxt 启动时读)。**CI 构建通用镜像、不烤域名**,真实域名在 **Dokploy 的环境变量**里注入。一个镜像可用于任意环境。
- **构建期 build-arg `PUBLIC_*`(hub web/wiki 现状)** —— 域名在 **CI build 时**烤进镜像 → 镜像与环境绑定。可行,但要在 workflow 的 `build-args` 里传 12-dokploy 的真实域名。

**建议**:把 hub web/wiki 也改为读运行时 `NUXT_PUBLIC_*`(其 `nuxt.config` 已声明 `runtimeConfig.public.*`,只需在 Dokploy 设对应 env),实现"**一次构建、各处部署**";过渡期保留 build-arg 也行。

> **SSR 双 base 不变**:`NUXT_API_BASE_SSR` / `NUXT_AUTH_API_BASE_SSR` / kungal `NUXT_API_BASE_URL` 仍是**运行时**容器内服务名(见 [12-dokploy §12.5](./12-dokploy.md));registry 化只影响"镜像怎么来",不影响 SSR/浏览器 base 的划分。

## 13.6 生产 compose 改用 `image:`(不再 `build:`)

新增一份**只引用镜像**的生产 compose(如各仓 `docker-compose.prod.yml` 或 `compose.ghcr.yml`),Dokploy 指向它;CI 负责把这些 tag build+push 出来。示例(hub 片段):

```yaml
# kun-galgame-infra/docker-compose.prod.yml(节选)
name: kun-galgame-infra
services:
  oauth:
    image: ghcr.io/kun1007/hub-oauth:latest   # ← 不再有 build:
    env_file: [./docker/oauth.env]
    expose: ["9277"]                           # ← expose 而非 ports(见 12-dokploy §12.2-B)
    depends_on: { postgres: { condition: service_healthy }, redis: { condition: service_healthy } }
    healthcheck: { test: ["CMD", "/app/app", "healthcheck"], <<: *svc-health }
    restart: unless-stopped
  web:
    image: ghcr.io/kun1007/hub-web:latest
    environment:
      NUXT_API_BASE_SSR: http://oauth:9277/api/v1
      # NUXT_PUBLIC_*: 见 13.5(运行时注入真实域名)
    expose: ["3000"]
  # galgame / image / wiki 同理 …
  postgres: { image: postgres:16-alpine, ... }   # 基础设施仍用上游镜像
networks:
  default: { name: dokploy-network, external: true }
```
- Dokploy compose 见到 `image:` 即**拉取**(不在生产机 build);webhook 触发 → `docker compose pull && up` → 拉最新 `:latest` 滚动更新。
- 本地/测试仍用带 `build:` 的 `docker-compose.yml`,**两套并存**(prod 用镜像、dev 用本地 build)。

## 13.7 Dokploy 侧配置

1. **加 Registry**(Settings → Registry):公开镜像可不加;私有则填 Name / Username=GitHub ID / Password=`read:packages` 的 **PAT** / URL=`https://ghcr.io`。
2. **应用指向 prod compose**(或用 Docker provider 填 `ghcr.io/kun1007/<svc>:latest`)。
3. **触发部署**:
   - **Webhook**:复制应用 Deployments 页的 Webhook URL → 放进 CI 的 `secrets.DOKPLOY_WEBHOOK_*`,`deploy` job `curl` 它。
   - 或 **API**:`POST /api/application.deploy`(带 API key)。
4. **公开 vs 私有镜像**:仓库公开 → 把对应 GHCR package 也设为 **public**,Dokploy 免凭证拉(最省事);私有则用 PAT。

## 13.8 Registry 维护

- **GHCR**:用 GitHub Packages 的**保留策略 / 手动删旧 tag** 控制体积(`:latest` 覆盖产生的旧 untagged 版本可定期清,可用 `actions/delete-package-versions` 之类)。
- **自托管(若将来用)**:registry **不会自动清**被覆盖 tag 的层 → 必须 `REGISTRY_STORAGE_DELETE_ENABLED=true` + 定期 `registry garbage-collect`;**TLS**(Let's Encrypt)+ **认证**(htpasswd / token)是底线;存储后端小规模用文件系统、规模化用 S3。

## 13.9 一次性迁移工具镜像(cutover)

跨仓迁移用到的 hub 一次性命令很多(`migrate-users`/`migrate-galgame-data`/`migrate-moyu-galgame`/`sync-vndb*`/`reindex-search` 等)。两种做法:
- 简单:cutover 时在 Dokploy Terminal 用 `hub-galgame`/`hub-migrate` 等已发布镜像 `docker run --entrypoint <bin>` 跑,或临时 `go run`;
- 规整:加一个 `hub-tools` 镜像(一个 Dockerfile 编译所有 `cmd/*` 需要的二进制)推 GHCR,cutover 用它跑。
顺序见 [03-bootstrap.md](./03-bootstrap.md) 与 `docs/migration/`;连库用容器服务名 `postgres:5432`。

## 13.10 升级路径

- **多节点 / 想要漏洞扫描·RBAC·签名** → 上 **Harbor**(或 Dokploy Cluster + 其内置 registry 分发镜像)。
- **想全自托管构建**(不依赖 GitHub)→ **Dokploy Build Server**(独立 build VPS)+ 自托管 `distribution/registry:2`。
- 在那之前,**GHCR + Actions 足够**。

## 13.11 安全 checklist

- [ ] workflow `permissions: packages: write`,用 `GITHUB_TOKEN`(不要长期 PAT 推送)。
- [ ] Dokploy webhook URL / API key 放 **GitHub Secrets**,不入库。
- [ ] 私有镜像在 Dokploy 用最小权限 PAT(`read:packages`);能公开则公开免凭证。
- [ ] 各服务 env(`docker/*.env`、`web.env`)里的**密钥全部轮换**(见 [05-configuration.md](./05-configuration.md)),不要把生产密钥烤进镜像——走 Dokploy env / env_file。
- [ ] 镜像 tag 用 `:<git-sha>` 保证可追溯/可回滚。
