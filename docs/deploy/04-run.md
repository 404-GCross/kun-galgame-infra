# 4 · 日常启停

## 网络模型

所有容器都跑在**枢纽创建的 docker 网络** `kun-oauth-admin_default` 上,靠服务名互相解析。三种把下游接上来的方式:

| 方式 | 说明 | 适用 |
|---|---|---|
| **增量并行**(本文推荐,已实跑) | 先起 hub(它建网络+基础设施),再在各下游仓 `up`,各自加入同一外部网络 | 单机、已有 hub 在跑 |
| **伞状编排** | `website/compose.yaml` 用 `include:` 把三仓拼成一个 project,共享一套网络 | 生产、一键起整套 |
| **kungal standalone** | kungal 叠 `docker-compose.standalone.yml` 自带 pg/redis,**不连 hub** | 只想单测 kungal api+web |

## 增量并行(已验证)

### 1) 枢纽
```bash
cd kun-oauth-admin
docker compose up -d            # 9 个服务全起
docker compose ps              # 应全 healthy
```

### 2) moyu
moyu 的 `docker-compose.yml` 自带:
```yaml
networks:
  default: { name: kun-oauth-admin_default, external: true }
```
所以直接:
```bash
cd kun-galgame-patch-next
docker compose up -d api web
```

### 3) kungal（需要 hub override）
kungal 的主 compose **没有**外部网络声明,且 `depends_on` 引用了它自己不定义的 `postgres`/`redis`。本仓库已附 `docker-compose.hub.yml` 解决:
```yaml
services:
  api:     { depends_on: !reset [] }     # pg/redis 是 hub 的,清掉本地依赖
  migrate: { depends_on: !reset [] }
networks:
  default: { name: kun-oauth-admin_default, external: true }
```
运行:
```bash
cd kun-galgame-nuxt4
docker compose -f docker-compose.yml -f docker-compose.hub.yml up -d api web
```

> 把 `-f docker-compose.yml -f docker-compose.hub.yml` 定义成别名省事:
> ```bash
> alias kungal='docker compose -f docker-compose.yml -f docker-compose.hub.yml'
> kungal up -d api web
> ```

## 启停顺序

- **启动**:Postgres/Redis(healthy)→ hub oauth/image/galgame → hub web/wiki → moyu → kungal。`depends_on: condition: service_healthy` 已把 hub 内部顺序串好;下游 `restart: unless-stopped` 会在 hub 起来后自动重连。
- **停止**:倒序无所谓(无状态);直接 `down` 即可。

## 常用命令

```bash
# 看全生态(跨三个 compose project)
docker ps --format '{{.Names}}\t{{.Status}}' | grep -E 'kun-oauth-admin-|moyu-|kungal-' | sort

# 单仓状态 / 日志
docker compose ps
docker compose logs -f oauth
docker compose -f docker-compose.yml -f docker-compose.hub.yml logs -f api   # kungal

# 停某仓(保留数据卷)
docker compose down                 # 在对应仓目录
# 停 + 清空数据卷(危险)
docker compose down -v              # 仅在 hub 目录会删 pg/redis/minio/meili 卷
```

> ⚠️ **数据卷只在 hub**(`pg`/`redis`/`minio`/`meili`)。moyu/kungal 无自己的卷(无状态),`down -v` 对它们无影响;但在 **hub** 目录 `down -v` 会清空全生态数据。

## 伞状编排(生产建议)

在 `website/` 放一个 `compose.yaml`:
```yaml
include:
  - kun-oauth-admin/docker-compose.yml
  - kun-galgame-patch-next/docker-compose.yml
  - kun-galgame-nuxt4/docker-compose.yml      # 注:伞状下 kungal 不需要 hub override
# 注意:include 各子 compose 的 `name:` 与 moyu 的 external network 块在伞状下需调整
# (同一 project 共享一张网络,external 块应去掉)。详见 07-troubleshooting.md。
```
前面再套 Caddy/Traefik 按域名分流到各 web/api,并统一 `/img` CDN 域。

## 访问入口(本测试机)

| 入口 | URL |
|---|---|
| oauth-admin 管理端 | http://localhost:15008 |
| galgame-wiki | http://localhost:15009 |
| moyu 补丁站 | http://localhost:15011 |
| kungal 论坛 | http://localhost:15013 |
| MinIO 控制台 | http://localhost:15003(minioadmin/minioadmin) |
