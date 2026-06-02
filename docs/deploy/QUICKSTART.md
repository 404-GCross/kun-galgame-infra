# 全新 Debian 服务器 · 一键部署三站(精简版 · Dokploy)

> 从一台空 Debian 到 **kungal / moyu / wiki / oauth** 全部上线的步骤,每步带一句说明。
> 用 **Dokploy**(自托管 PaaS):它**内置 Traefik 反代 + 自动 Let's Encrypt SSL + 编排**,所以**不需要 Caddy/Nginx**,也别再叠加。
> 深入原理见 [12-dokploy.md](./12-dokploy.md)(部署/路由)+ [13-registry-ci.md](./13-registry-ci.md)(镜像从哪来)。

**线上域名**:`kungal.com`/`www.kungal.com`、`moyu.moe`/`www.moyu.moe`、`wiki.kungal.com`、`oauth.kungal.com`、`image.kungal.iloveren.link`(走 Cloudflare R2,**不经本机**)。

---

## 1. DNS

把下列域名的 **A 记录指向服务器公网 IP**(`image.*` 指向 Cloudflare,不指本机):
`kungal.com`、`www.kungal.com`、`moyu.moe`、`www.moyu.moe`、`wiki.kungal.com`、`oauth.kungal.com`

> 🔒 **要隐藏源站 IP**(强烈建议)见 [§10](#10-隐藏源站-ip--防泄漏强烈建议):用 Cloudflare Tunnel 则这些记录改 **CNAME 到隧道、不建 A 记录**;用 Cloudflare 代理则给它们开 **Proxy(橙云)**。下面 §1–§9 是"公网直连"基线,§10 在其上加固。

## 2. 装 Dokploy(自动装 Docker)

```bash
sudo apt update && sudo apt -y install ufw            # 防火墙
sudo ufw allow OpenSSH && sudo ufw allow 80 && sudo ufw allow 443 && sudo ufw allow 3000 && sudo ufw --force enable
#   80/443 = Traefik 对外,3000 = Dokploy 面板(初装用,§9 稳定后收掉)
#   注:§10 隐藏源站后,80/443 改为"只放 Cloudflare 段"(方案 B)或"完全关闭"(方案 A 隧道)
curl -sSL https://dokploy.com/install.sh | sh         # 一键装 Docker + Dokploy(含 Traefik)
```
装完浏览器开 `http://<服务器IP>:3000` 注册管理员。

## 3. 镜像:CI 构建 → GHCR(不在生产机 build)

本生态 13 个重镜像(cgo + 4×Nuxt),**别在生产机构建**(会拖垮单机)。各仓已带 `.github/workflows/build.yml`:

```bash
# 本机:三仓各自 push 到默认分支(infra=main,kungal/moyu=master)
git push        # → GitHub Actions 自动 build 并推 ghcr.io/kun1007/*(:latest + :<sha>)
```
- 到 GitHub 各仓 **Packages**,把镜像设为 **public** → Dokploy 免凭证拉;私有则在 Dokploy 配 `read:packages` 的 PAT。
- *起步捷径*:不想配 CI → 第 4 步应用直接选 **Git source** 让 Dokploy 在服务器上 build(简单,但重镜像有拖垮单机风险)。

## 4. Dokploy 建 3 个 Compose 应用

面板 → **Create → Compose**,各对应一个 Git 仓库,Compose 文件指向各仓 **`docker-compose.prod.yml`**(已用 `image: ghcr.io/kun1007/*` + `expose` + `dokploy-network`):

| 应用 | 仓库 | Compose 文件 |
|---|---|---|
| `kun-galgame-infra` | infra | `docker-compose.prod.yml` |
| `kun-galgame-nuxt4` | kungal | `docker-compose.prod.yml` |
| `kun-galgame-patch-next` | moyu | `docker-compose.prod.yml` |

三应用共享 Dokploy 提供的 `dokploy-network`(external),跨应用按枢纽**唯一服务名**(`postgres`/`redis`/`oauth`/`galgame`/`image`)互通。

## 5. 填环境变量(Dokploy 各应用 Environment · 真实域名 + 轮换密钥)

对应各仓 `docker/*.env` 的内容,在 Dokploy 应用的 **Environment** 填(**务必轮换所有测试密钥**):

- **infra**:`POSTGRES_PASSWORD` / `MINIO_ROOT_USER` / `MINIO_ROOT_PASSWORD` / `MEILI_MASTER_KEY`(prod compose 用必填插值,不设起不来);`oauth.env` 的 `KUN_SITE_URL`=`KUN_FRONTEND_URL`=`https://oauth.kungal.com`、`KUN_FRONTEND_CORS_ORIGIN`=全部 https 域名、SMTP;`image.env` 的 R2 凭证 + `KUN_IMAGE_PUBLIC_BASE_URL=https://image.kungal.iloveren.link`
- **kungal**:`CORS_ALLOW_ORIGINS=https://www.kungal.com,https://kungal.com`、`OAUTH_CLIENT_ID/SECRET`(§6 拿到后填)、web 的 `NUXT_PUBLIC_*`=真实域名、`NUXT_API_BASE_URL=http://api:2334`(SSR 走服务名,勿改)
- **moyu**:`CORS_ALLOW_ORIGINS=https://www.moyu.moe,https://moyu.moe`、`OAUTH_*`、web 的 `NUXT_PUBLIC_*`=真实域名、`NUXT_API_BASE_SSR=http://api:5214/api/v1`

> 后端→后端 base 用容器服务名(`http://oauth:9277/api/v1` 等)保持不变。完整清单见 [12-dokploy §12.2](./12-dokploy.md) + [05-configuration.md](./05-configuration.md)。

## 6. 部署顺序 + 建库 + 注册 OAuth client

1. **先部署 infra**,等 `postgres`/`redis`/`minio`/`meili` healthy。
2. 在 infra 应用的 **Terminal** 跑首启迁移:
   ```bash
   docker compose -f docker-compose.prod.yml run --rm migrate           # kun_galgame_infra:表 + 站点/角色种子
   docker compose -f docker-compose.prod.yml run --rm migrate-galgame   # kun_galgame_wiki:表 + 约束
   ```
3. **注册 OAuth client**(否则前端登录走不通,不在任何 migrate 种子里):登录 infra 管理端建 **论坛 / 补丁 / wiki** 三个 client(`redirect_uri` 填各自 https 回调),把生成的 **secret 写回** kungal/moyu 的 Environment。见 [03-bootstrap §A.5](./03-bootstrap.md) + [12-dokploy §12.3](./12-dokploy.md)。
4. **再部署 kungal、moyu**;各自 Terminal 跑 `docker compose -f docker-compose.prod.yml run --rm migrate`(清理型迁移,空库打印「无迁移」即正常)。

> 要导入旧 Node 站点真实数据 → [03-bootstrap §B](./03-bootstrap.md) + `docs/migration`(`migrate-users` 是分水岭,**严格按序**)。

## 7. 挂域名(Traefik 自动签 SSL)

每个对外服务在应用的 **Domains** 标签加记录(域名 + 路径 + 目标服务 + 容器内端口);同域 `/api*` 与 `/` 各一条(更具体的路径优先)。Dokploy 自动注入 Traefik labels 并签 Let's Encrypt 证书:

> Traefik 的 LE 用 HTTP 验证,要求该域名签发时是 **DNS-only(灰云)**;若按 [§10](#10-隐藏源站-ip--防泄漏强烈建议) 开了橙云/隧道,改用 **DNS-01** 验证或**关掉 LE 由 Cloudflare 出证**(别一边橙云一边等 HTTP 验证,会签不出)。

| 公网域名 | 路径 | 应用 | 目标服务:端口 |
|---|---|---|---|
| `oauth.kungal.com` | `/api/v1` | infra | `oauth:9277` |
| `oauth.kungal.com` | `/` | infra | `web:3000`(管理端) |
| `wiki.kungal.com` | `/api` | infra | `galgame:9280` |
| `wiki.kungal.com` | `/` | infra | `wiki:3000` |
| `kungal.com` + `www.kungal.com` | `/api` | kungal | `api:2334` |
| `kungal.com` + `www.kungal.com` | `/` | kungal | `web:7777` |
| `moyu.moe` + `www.moyu.moe` | `/api/v1` | moyu | `api:5214` |
| `moyu.moe` + `www.moyu.moe` | `/` | moyu | `web:3000` |

> `image.kungal.iloveren.link` 走 Cloudflare R2(由 CF 直供 blob),**不在 Dokploy 挂域名**;`image` 服务(`:9278`)是 s2s 内部,不对外。

## 8. 验证上线

```bash
curl -I https://oauth.kungal.com     # 302→登录 + 有效证书
curl -I https://wiki.kungal.com
curl -I https://www.kungal.com
curl -I https://www.moyu.moe
```
Dokploy 各应用看 **Logs / 健康状态**。**回滚** = 镜像引用从 `:latest` 临时改某个 `:<git-sha>` 再 redeploy。

## 9. 收尾

- **收掉面板端口**:稳定后 `sudo ufw delete allow 3000`,改用 SSH 隧道或给 Dokploy 面板挂独立域名 + 鉴权访问。
- **图床**:走 Cloudflare R2(`image.env` 配 R2 凭证,Cloudflare 直供,不经服务器);自托管 MinIO 才需在 Dokploy 给 `image.*` 挂域名回源 `minio:9000`。
- **持续更新**:push 代码 → CI 重 build 推 GHCR → CI 的 `deploy` job(Dokploy webhook,放 GitHub Secret 的 `DOKPLOY_WEBHOOK_*`)触发拉新镜像滚动更新(见 [13-registry-ci.md](./13-registry-ci.md))。
- **备份**:用 Dokploy 自带备份,或 Terminal 跑 `pg_dumpall`,见 [06-operations.md](./06-operations.md)。

## 10. 隐藏源站 IP / 防泄漏(强烈建议)

§1–§9 默认在公网 80/443 上跑 Traefik + LE,**A 记录直指服务器 → 源站 IP 暴露**(LE 证书也进 CT 日志)。若 IP 泄漏,攻击者可绕过 Cloudflare 直打源站(DDoS/扫描)。二选一加固:

### 方案 A · Cloudflare Tunnel(最强,推荐)

源站**只出不进、零入站端口**,IP 永不进 DNS,端口扫描器看不到。Dokploy [官方支持](https://docs.dokploy.com/docs/core/guides/cloudflare-tunnels)。
```bash
# 1) Cloudflare Zero Trust → Networks → Tunnels 建隧道,拿 token
# 2) 服务器跑 cloudflared,加入 Dokploy 网络以解析 Traefik:
docker run -d --name cloudflared --restart unless-stopped --network dokploy-network \
  cloudflare/cloudflared:latest tunnel --no-autoupdate run --token <TUNNEL_TOKEN>
# 3) CF 隧道里把各 hostname 的 Service 指向 http://dokploy-traefik:80(由 Traefik 按域名分流)
# 4) Dokploy 各应用域名【关掉 Let's Encrypt】,内部走 http(TLS 由 CF 边缘终止)
# 5) 关掉所有入站(只留 SSH):
sudo ufw delete allow 80 && sudo ufw delete allow 443 && sudo ufw delete allow 3000
```
DNS 此时是 **CNAME 到 `<id>.cfargotunnel.com`,无 A 记录指向源站**。详见 [11-edge-cloudflare-tunnel.md](./11-edge-cloudflare-tunnel.md)。

### 方案 B · Cloudflare 代理(橙云)+ 防火墙锁 CF IP

保留 §1–§9 的公网 80/443,但:① 所有公网记录开 **Proxy(橙云)**;② 防火墙**只放 Cloudflare 段**访问 80/443(即便 IP 泄漏也直连不进):
```bash
for ip in $(curl -s https://www.cloudflare.com/ips-v4) $(curl -s https://www.cloudflare.com/ips-v6); do
  sudo ufw allow from "$ip" to any port 80,443 proto tcp comment 'cloudflare'
done
sudo ufw delete allow 80 && sudo ufw delete allow 443     # 删掉对所有人开放的 80/443(SSH 必须保留!)
```
> CF 段会变,用 [ufw-cf](https://github.com/Malith-Rukshan/ufw-cf) 或 [cloudflare-ufw-updater](https://github.com/jakejarvis/cloudflare-ufw-updater) 定时同步。
> **SSL**:橙云会拦截 LE 的 HTTP 验证 → Traefik 改用 **DNS-01**(填 Cloudflare API token)或挂 **Cloudflare Origin CA 证书**,CF 的 SSL 模式设 **Full (strict)**。

### 防泄漏清单(两方案都要做)

- **DNS 审计**:所有 `A/AAAA/MX/TXT/SPF/`旧子域都不能含源站 IP;别留 `dev.`/`staging.` 等直指源站的记录(会被 DNS 数据集 / CT 日志搜出)。
- **邮件**:用外部 SMTP 中继发信(本项目用 mxroute ✓),别在源站跑邮件服务;确认邮件头不含源站 IP(否则给不存在地址发信会退信暴露 IP)。
- **历史 IP**:若该 IP 曾公开过(DNS 历史 / SecurityTrails / Shodan),上 CF 后**换一个新服务器 IP**——旧记录是公开档案,改不掉。
- **证书**:方案 A 与 B(Origin CA)源站不对外暴露公网 LE 证书 → 不进 CT;B(DNS-01)证书只暴露域名(本就公开),IP 仍被防火墙挡住。
- **持续监控**:隐藏源站是长期事,定期用 [Cloudflare 官方指南](https://developers.cloudflare.com/fundamentals/security/protect-your-origin-server/) 自查。
