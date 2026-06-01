# 6 · 运维(Day-2)

## 健康与可见性

```bash
# 全生态状态
docker ps --format '{{.Names}}\t{{.Status}}' | grep -E 'kun-oauth-admin-|moyu-|kungal-' | sort

# 端点探活
for u in 19277/api/v1/health 19278/healthz 19280/api/health \
         15214/api/v1/health 12334/healthz; do
  printf "%-24s " "$u"; curl -s -o /dev/null -w "%{http_code}\n" "http://localhost:$u"
done

# 日志(跟随)
docker compose logs -f --since 10m oauth
```

- 三个 Go HTTP 服务的容器 `HEALTHCHECK` 用**二进制自带的 `healthcheck` 子命令**(distroless 无 shell):`/app healthcheck`(hub cgo 镜像是 `/app/app healthcheck`)自探 `/health(z)`。前端用 Node TCP 探活。
- `docker inspect --format '{{.State.Health.Status}}' <container>` 看健康判定。

## 升级 / 重新部署

无状态服务,滚动重建即可:
```bash
# 改了代码后,重建并替换(数据卷不动)
docker compose build oauth && docker compose up -d oauth
# 下游同理(kungal 记得带 -f override)
```
- 升级 Go/Nuxt 服务**不需要**动数据库。
- 升级**有状态**镜像(Postgres/Meili 大版本)要先备份卷,见下;Meili 跨大版本不可直接复用旧卷(见 [07-troubleshooting.md](./07-troubleshooting.md))。

## 备份 / 恢复

数据全在 hub 的 4 个命名卷:`kun-oauth-admin_{pg,redis,minio,meili}`。

```bash
# Postgres 逻辑备份(所有库)
docker exec kun-oauth-admin-postgres-1 pg_dumpall -U postgres > all-$(date +%F).sql
# 单库
docker exec kun-oauth-admin-postgres-1 pg_dump -U postgres kungalgame > kungalgame-$(date +%F).sql

# 卷级备份(停服更安全)
docker run --rm -v kun-oauth-admin_pg:/data -v "$PWD:/b" alpine \
  tar czf /b/pg-vol-$(date +%F).tgz -C /data .

# MinIO 对象:挂卷 tar,或用 mc mirror 到异地
docker run --rm -v kun-oauth-admin_minio:/data -v "$PWD:/b" alpine \
  tar czf /b/minio-$(date +%F).tgz -C /data .
```

恢复:`psql < dump.sql` 进对应库;卷级则 `tar x` 回空卷后再起服务。
- Redis 是缓存/会话,**可不备份**(丢了重新登录即可)。
- Meili 索引可由 `cmd/reindex-search` 从 Postgres 重建,**不必备份**。

## 迁移 / 一次性任务

都是 `profiles: ["jobs"]` 的一次性容器,`up` 不会拉起:
```bash
docker compose run --rm migrate              # hub oauth schema
docker compose run --rm migrate-galgame      # hub wiki schema
# 其它工具用 build-arg 出镜像后 run:
docker build -f docker/go.Dockerfile --build-arg CMD=sync-vndb -t kun-oauth-admin/sync-vndb .
docker run --rm --network kun-oauth-admin_default --env-file docker/galgame.env kun-oauth-admin/sync-vndb
```
跨仓数据迁移的**顺序**见 [03-bootstrap.md](./03-bootstrap.md) B 节——`migrate-users` 是分水岭。

## 定时任务

hub `oauth` 进程内置 job 调度器(启动日志可见 `jobs: scheduler started`),自动跑 `image-gc` / `sync-vndb` / `*-refping` 等,**无需** crontab。可在 admin 端 `/api/v1/admin/jobs/*` 手动触发 / 看历史。

## 扩缩容

- 无状态 api/web 可水平扩:`docker compose up -d --scale galgame=2`(前面要有反代做负载均衡;hub 的 job 调度用 PG advisory lock 做单飞,多副本安全)。
- 有状态(pg/redis/minio/meili)单实例;要高可用需各自的集群方案,超出本机范围。

## 资源 / 清理

```bash
docker images --format '{{.Repository}}:{{.Tag}}\t{{.Size}}' | grep -E 'kun-oauth-admin/|moyu/|kungal/'
docker system df            # 看占用
docker image prune          # 清悬空镜像
```
- 5 个 Node SSR 进程内存占大头(各 ~150–300MB)。内存紧张时给 web 容器加 `mem_limit`。

## 反向代理(生产)

各 web/api 前面套 Caddy/Traefik,按域名分流并终止 TLS。示例(Caddy):
```
oauth.kungal.com   → hub web:3000     ;  /api/* → oauth:9277
www.kungal.com     → kungal web:7777
www.moyu.moe       → moyu web:3000    ;  /api/* → moyu api:5214
image.kungal.com   → minio:9000/kun-images   (或 CDN 回源 MinIO)
```
注意把对应 web 镜像的 `PUBLIC_*`/`NUXT_PUBLIC_*` 改成真实公网域名(见 [05-configuration.md](./05-configuration.md))。
