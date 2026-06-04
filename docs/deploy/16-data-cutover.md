# 16 · 带数据上线(Data Cutover · 容器化逐条命令)

> [03-bootstrap.md §B](./03-bootstrap.md) 讲**顺序与原理**;本篇是它的**可直接抄的服务器版命令**——
> 从两个生产 dump(`kungalgame_backup.dump` / `kungalgame_patch_backup.dump`)开始,把老数据迁进新的 5 库结构。
> 迁移的**机制/为什么**见 [docs/migration/user/](../migration/user/)(身份统一)与
> [docs/galgame_wiki/02-moyu-migration-design.md](../galgame_wiki/02-moyu-migration-design.md)(词条迁移)。

数据最终去向(一图):

```
kungalgame(_patch)_backup.dump
   │ pg_restore -d kungalgame / kungalgame_patch        ← 还原成"源库"(同名)
   ▼
[kungalgame]/[kungalgame_patch]  老结构+老数据+老 user.id   ── 数据源 ──┐
   ├─ migrate-users      合并去重+发新ID ─▶ [kun_galgame_infra].users   (身份抽出去)
   │                     两阶段offset回改 ─▶ 源库 user.id + 全部 FK 列   (就地对齐)
   ├─ migrate-galgame-data / migrate-moyu-galgame ─▶ [kun_galgame_wiki] (词条抽出去)
   └─ 清理型 .sql 迁移就地瘦身            ─▶ [kungalgame(_patch)] 仍作业务库
```

---

## 16.0 与本地 `go run` 的对应关系

团队本地 runbook 是裸 `go run ./cmd/X`(各仓独立 pg、独立密码)。服务器是**容器化 + 一个共享 postgres + `*-tools` 镜像**,4 个关键差异:

| 维度 | 本地 | 服务器(本篇) |
|---|---|---|
| 仓库名 | kun-oauth-admin / kun-galgame-nuxt4 / kun-galgame-patch-next | **kun-galgame-infra / kun-galgame-forum / kun-galgame-patch** |
| 跑工具 | `cd apps/api && go run ./cmd/X …` | `docker compose -f <repo>/docker-compose.prod.yml --profile jobs run --rm tools X …` |
| 数据库 | 各仓独立 pg、密码 `kunloveren`/`renlovekun` | **同一个 pg、同一个 `POSTGRES_PASSWORD`**;源库 DSN 用 `host=postgres` |
| 连库 | `host=127.0.0.1` | `host=postgres`(容器服务名;`compose run` 自动接 `dokploy-network`) |

每个 `cmd/*` 二进制都打包在对应 `*-tools` 镜像里(`infra-tools` / `kungal-tools` / `moyu-tools`,CI 推到 GHCR,**已含各仓 `/migrations` 与 infra 的 `docs/tagMap.ts`**)。`tools` 已是各仓 prod compose 的 jobs-profile 服务,**environment 内联、密钥从 Dokploy 面板的 `${VAR}` 取**——所以不用 `--env-file`、不用在服务器放任何 env 文件。镜像清单见 [13-registry-ci.md](./13-registry-ci.md)。

> **本地→容器 速查**:`go run ./cmd/migrate-users --kungal-dsn=…` ⇒
> `docker compose -f $INFRA/docker-compose.prod.yml --profile jobs run --rm tools migrate-users --kungal-dsn=…`

---

## 16.1 前置(三条铁律)

1. **三库先备份**:dump 本身就是源库备份;`migrate-users` 还会改源库,**跑前先 `pg_dump` 一份 `kun_galgame_infra`** 以便重跑(见 [14-backup-restore.md](./14-backup-restore.md))。
2. **下游必须停**:`migrate-users` 期间关了 FK 触发器,任何写入都会绕过校验、写进旧 ID。**只起 infra 的 `postgres`/`redis`,下游 api 一律别起/先停**。
3. **先 dry-run**:`migrate-users`、`remap-patch-ids` 都支持 `--dry-run`,先看合并/跳过/孤儿计数符不符预期。

---

## 16.2 变量(开个终端先 `export`)

```bash
PG=kun-galgame-infra-postgres-1          # infra postgres 容器名(docker ps 确认)
REDIS=kun-galgame-infra-redis-1
PGPASS='<你的 POSTGRES_PASSWORD>'          # = infra 的 POSTGRES_PASSWORD(不是老的 kunloveren/renlovekun!)
DUMPS=/srv/dumps                         # 两个 .dump 在服务器上的目录

# 各应用的 Dokploy 部署目录 —— 里面有 Dokploy 按面板写的 .env,`compose run` 会自动加载,
# 所有 ${VAR}(POSTGRES_PASSWORD / JWT_SECRET / MEILI_MASTER_KEY / OAUTH_CLIENT_SECRET …)就位。
INFRA=/etc/dokploy/compose/kun-galgame-infra/code   # 实际路径见 Dokploy 应用详情
FORUM=/etc/dokploy/compose/kungal/code
PATCH=/etc/dokploy/compose/moyu/code

# 源库 DSN(容器内按服务名连同一个 pg,密码统一;PGPASS 由本机 shell 展开进 flag)
KDSN="host=postgres port=5432 user=postgres password=$PGPASS dbname=kungalgame sslmode=disable"
MDSN="host=postgres port=5432 user=postgres password=$PGPASS dbname=kungalgame_patch sslmode=disable"

# 跑工具的快捷前缀(jobs profile 的 tools 服务;environment 内联、密钥从该应用 .env 取)
INFRA_OAUTH="docker compose -f $INFRA/docker-compose.prod.yml --profile jobs run --rm tools"
INFRA_WIKI="$INFRA_OAUTH"                 # 同一个:infra-tools 含全套 infra env(身份库 + wiki 库)
KUNGAL="docker compose -f $FORUM/docker-compose.prod.yml --profile jobs run --rm tools"
MOYU="docker compose -f $PATCH/docker-compose.prod.yml --profile jobs run --rm tools"
```

> `tools` 服务 environment 内联了全套库名/JWT/Meili,所以同一个 `INFRA_*` 前缀既能跑写身份库的 cmd(migrate-users)
> 也能跑写 wiki 库的(migrate-galgame*)。**从应用部署目录跑**(`-f $INFRA/...` 即指向那),compose 会加载 Dokploy 写的 `.env`,
> 所有 `${VAR}` 自动就位;否则需自行 `export POSTGRES_PASSWORD JWT_SECRET MEILI_MASTER_KEY OAUTH_CLIENT_SECRET`。

---

## 16.3 步骤 0 · 把 dump 放到位 + 只起基础设施

```bash
# (dump 已在机器上则跳过)从老机/本地拷过来
scp kungalgame_backup.dump kungalgame_patch_backup.dump kun@SERVER:/srv/dumps/

# 只起 infra 的 postgres + redis,下游不起
docker compose -f "$INFRA/docker-compose.prod.yml" up -d postgres redis
```

---

## 16.4 步骤 1 · 重置库 + 还原 dump(= 本地 `reset_all.sh`)

```bash
# infra 自有 3 库:置空(schema 后面由 migrate / AutoMigrate 重建)
for db in kun_galgame_infra kun_galgame_wiki kun_images; do
  docker exec "$PG" psql -U postgres -d postgres -c "DROP DATABASE IF EXISTS $db; CREATE DATABASE $db;"
done

# 源 2 库:drop + 建 + pg_restore(custom 格式走 stdin,无需 docker cp)
for db in kungalgame kungalgame_patch; do
  docker exec "$PG" psql -U postgres -d postgres -c "DROP DATABASE IF EXISTS $db; CREATE DATABASE $db;"
done
docker exec -i "$PG" pg_restore -U postgres -d kungalgame       -n public < "$DUMPS/kungalgame_backup.dump"
docker exec -i "$PG" pg_restore -U postgres -d kungalgame_patch -n public < "$DUMPS/kungalgame_patch_backup.dump"

# 清 redis(旧会话/缓存)
docker exec "$REDIS" redis-cli FLUSHALL
```

> ⚠️ **排序版本不匹配**:dump 多来自 glibc Debian,容器 pg 是 alpine/musl,还原后会有 collation 警告。对两个源库各做一遍(本地只对 patch 做过,这里两库都做更稳):
> ```bash
> for db in kungalgame kungalgame_patch; do
>   docker exec "$PG" psql -U postgres -d "$db" -c "REINDEX DATABASE \"$db\";"
>   docker exec "$PG" psql -U postgres -d "$db" -c "ALTER DATABASE \"$db\" REFRESH COLLATION VERSION;"
> done
> ```

---

## 16.5 步骤 2 · kungal 预处理(check-dup-email + 001–004/008–009)

```bash
$KUNGAL check-dup-email
$KUNGAL migrate          # 默认 -exclude=005,006,007,012,015,即只跑非延后项(001–004/008–009 等)
```

## 16.6 步骤 3 · moyu OAuth 对齐准备

```bash
$MOYU migrate-oauth-prep -yes
```

---

## 16.7 步骤 4 · infra 身份 + 内容(★ 顺序敏感,`migrate-users` 是分水岭)

```bash
# 4.1 infra schema + 站点/角色种子
$INFRA_OAUTH migrate

# 4.2 合并身份 + 对齐三库 user.id —— 先 dry-run!
$INFRA_OAUTH migrate-users --kungal-dsn="$KDSN" --moyu-dsn="$MDSN" --dry-run
$INFRA_OAUTH migrate-users --kungal-dsn="$KDSN" --moyu-dsn="$MDSN"

# 4.3 wiki schema + 词条抽取(目标 kun_galgame_wiki)
$INFRA_WIKI migrate-galgame
$INFRA_WIKI migrate-galgame-data --kungal-dsn="$KDSN"
$INFRA_WIKI migrate-moyu-galgame --moyu-dsn="$MDSN"
$INFRA_WIKI dedup-galgame-alias
```

> `migrate-galgame-data` / `migrate-moyu-galgame` 带过去的 `galgame.user_id` 是**已对齐**的值,所以**必须在 `migrate-users` 之后**,否则指向不存在的用户。

---

## 16.8 步骤 5 · kungal 收尾迁移(**必须在 `migrate-users` 之后**)

```bash
$KUNGAL migrate --only=005          # 删冗余表/字段
$KUNGAL migrate --only=006          # 新 resource provider 扫描机制
$KUNGAL backfill-provider-names     # 回填 provider 历史
$KUNGAL migrate --only=007
$KUNGAL migrate --only=015          # daily_toolset_upload_bytes(依赖 007 先建表)
$KUNGAL migrate --only=012          # 广播游标:必须在 migrate-users 重映射 id 之后,否则游标指向旧 id
```

## 16.9 步骤 6 · moyu 收尾迁移

```bash
$MOYU migrate -dir=up -only=001
$MOYU migrate -dir=up
# 先看孤儿补丁(可选):$MOYU remap-patch-ids -dry-run -orphans-out=orphans.txt
$MOYU remap-patch-ids               # patch id → galgame id 重映射
```

---

## 16.10 步骤 7 ·(可选)VNDB 同步 —— 必带 `-tagmap`

`sync-vndb` / `sync-vndb-relations` 运行期要读 `tagMap.ts`。本地默认路径 `../../docs/tagMap.ts` 在容器里**不成立**——
镜像内它在 `/app/docs/tagMap.ts`(WORKDIR=`/app`),所以**必须显式传 `-tagmap docs/tagMap.ts`**:

```bash
$INFRA_WIKI sync-vndb --full -tagmap docs/tagMap.ts
$INFRA_WIKI cleanup-bogus-vndb-id
$INFRA_WIKI cleanup-bogus-vndb-id --delete
$INFRA_WIKI sync-vndb-relations -tagmap docs/tagMap.ts
$INFRA_WIKI sync-vndb -tagmap docs/tagMap.ts        # 增量(以后做成 cron)
```

## 16.11 步骤 8 · 发布日期回填 + 萌萌点 —— **需要 galgame(wiki)服务在跑**

`backfill-release-date` 通过 HTTP 调 wiki 服务(`KUN_GALGAME_WIKI_BASE_URL=http://galgame:9280/api`),所以先把它起来:

```bash
docker compose -f "$INFRA/docker-compose.prod.yml" up -d galgame

$MOYU   backfill-release-date
$KUNGAL backfill-release-date
$INFRA_OAUTH migrate-moemoepoint     # 迁移 kungal/moyu 萌萌点日志
```

---

## 16.12 步骤 9 · 全栈拉起 + 校验

```bash
docker compose -f "$INFRA/docker-compose.prod.yml" up -d
docker compose -f "$FORUM/docker-compose.prod.yml" up -d api web
docker compose -f "$PATCH/docker-compose.prod.yml" up -d api web
```

校验:

```bash
# 合并后用户数 + 三库 user.id 对齐
docker exec "$PG" psql -U postgres -d kun_galgame_infra -tAc 'select count(*) from users'
docker exec "$PG" psql -U postgres -d kungalgame        -tAc 'select max(id) from "user"'
docker exec "$PG" psql -U postgres -d kungalgame_patch  -tAc 'select max(id) from "user"'
# 5 库齐全
docker exec "$PG" psql -U postgres -tAc \
  "select datname from pg_database where datistemplate=false order by 1"
# 各站点 healthz(端口见 12-dokploy 域名表 / 00-architecture)
curl -I https://oauth.kungal.com https://www.kungal.com https://www.moyu.moe https://wiki.kungal.com
```

迁移正确性的深度校验(反查原始 ID、计数核对)见 [docs/migration/user/07-verification.md](../migration/user/07-verification.md)。

---

## 16.13 出错 / 重跑

| 现象 | 处理 |
|---|---|
| step 1–4.1(连接/schema)失败 | 修好后**原地重跑**(OAuth 端写入是 idempotent skip) |
| `migrate-users`(4.2)中途失败 | 事务回滚源库;OAuth 可能已部分写入 → 恢复 `kun_galgame_infra` 备份(或手动清)后重跑 |
| 跑了一半发现数据不对 | **三库全恢复**:重跑步骤 1(重新 `pg_restore` 两个 dump)+ 恢复/重建 `kun_galgame_infra`,从头来 |
| `sync-vndb` 报找不到 tagMap | 漏了 `-tagmap docs/tagMap.ts` |
| `backfill-release-date` 连不上 wiki | galgame 服务没起 / 不在同一 `--network` |

详细恢复决策见 [docs/migration/user/06-recovery.md](../migration/user/06-recovery.md)。

---

## 16.14 本地 ↔ 容器 命令对照表(快速翻译)

| 本地(`cd apps/api &&`) | 服务器(容器) |
|---|---|
| `kun-oauth-admin: ./scripts/reset_all.sh` | §16.4 的 `docker exec … psql DROP/CREATE` + `pg_restore` |
| `redis-cli FLUSHALL` | `docker exec $REDIS redis-cli FLUSHALL` |
| `nuxt4: go run ./cmd/check-dup-email` | `$KUNGAL check-dup-email` |
| `nuxt4: go run ./cmd/migrate` | `$KUNGAL migrate` |
| `patch-next: go run ./cmd/migrate-oauth-prep -yes` | `$MOYU migrate-oauth-prep -yes` |
| `oauth-admin: go run ./cmd/migrate` | `$INFRA_OAUTH migrate` |
| `oauth-admin: go run ./cmd/migrate-users --kungal-dsn=… --moyu-dsn=…` | `$INFRA_OAUTH migrate-users --kungal-dsn="$KDSN" --moyu-dsn="$MDSN"` |
| `oauth-admin: go run ./cmd/migrate-galgame` | `$INFRA_WIKI migrate-galgame` |
| `oauth-admin: go run ./cmd/migrate-galgame-data --kungal-dsn=…` | `$INFRA_WIKI migrate-galgame-data --kungal-dsn="$KDSN"` |
| `oauth-admin: go run ./cmd/migrate-moyu-galgame --moyu-dsn=…` | `$INFRA_WIKI migrate-moyu-galgame --moyu-dsn="$MDSN"` |
| `oauth-admin: go run ./cmd/dedup-galgame-alias` | `$INFRA_WIKI dedup-galgame-alias` |
| `nuxt4: go run ./cmd/migrate --only=005/006/007/015/012` | `$KUNGAL migrate --only=005`(逐条,见 §16.8) |
| `nuxt4: go run ./cmd/backfill-provider-names` | `$KUNGAL backfill-provider-names` |
| `patch-next: go run ./cmd/migrate -dir=up [-only=001]` | `$MOYU migrate -dir=up [-only=001]` |
| `patch-next: go run ./cmd/remap-patch-ids` | `$MOYU remap-patch-ids` |
| `oauth-admin: go run ./cmd/sync-vndb --full` | `$INFRA_WIKI sync-vndb --full -tagmap docs/tagMap.ts` |
| `oauth-admin: go run ./cmd/cleanup-bogus-vndb-id [--delete]` | `$INFRA_WIKI cleanup-bogus-vndb-id [--delete]` |
| `oauth-admin: go run ./cmd/sync-vndb-relations` | `$INFRA_WIKI sync-vndb-relations -tagmap docs/tagMap.ts` |
| `patch-next/nuxt4: go run ./cmd/backfill-release-date` | `$MOYU backfill-release-date` / `$KUNGAL backfill-release-date`(需 galgame 在跑) |
| `oauth-admin: go run ./cmd/migrate-moemoepoint` | `$INFRA_OAUTH migrate-moemoepoint` |

---

**相关**:[03-bootstrap.md](./03-bootstrap.md)(顺序/原理)· [13-registry-ci.md](./13-registry-ci.md)(`*-tools` 镜像)·
[14-backup-restore.md](./14-backup-restore.md)(备份/还原)· [docs/migration/user/](../migration/user/)(身份统一机制)·
[docs/galgame_wiki/02-moyu-migration-design.md](../galgame_wiki/02-moyu-migration-design.md)(词条迁移)
