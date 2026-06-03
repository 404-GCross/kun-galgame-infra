# 部署注意事项 / 踩坑速查

> 开荒 + 上线过程中**实际踩到、最容易翻车**的点集中在这里,每条都是 **现象 / 坑 → 结论 / 解法 → 详见**。
> 完整步骤看 [SERVER-SETUP.md](./SERVER-SETUP.md)(裸机开荒)和 [QUICKSTART.md](./QUICKSTART.md)(部署);本篇是「别再踩同一个坑」的速查。

---

## 一、SSH / 服务器登录

1. **改 hostname 后 `sudo: unable to resolve host kun-prod`**
   `hostnamectl set-hostname X` 必须**同步**把 `127.0.1.1 X` 写进 `/etc/hosts`,否则之后每条 `sudo` 都会报这个(只是警告、不致命,但会拖慢 sudo)。
   ```bash
   echo '127.0.1.1 kun-prod' | sudo tee -a /etc/hosts
   ```

2. **配了密钥却还要输密码 —— 这不是 sudo 的事**
   `kun@<ip>'s password:` 是 **SSH 登录**回落到了密码(= 公钥认证失败),和 `NOPASSWD sudo` **完全无关**(后者只管登录**之后**的 `sudo`)。

3. **⚠ 最常见的元凶:家目录权限太松**
   sshd 的 `StrictModes` 要求 `~` / `~/.ssh` / `authorized_keys` **不被同组或其他人可写**、且属主是本人;否则 sshd **直接忽略 key**、回落密码。很多 VPS 把 `/home/kun` 建成 `775`(g+w)就会中招。
   ```bash
   chmod go-w ~ ; chmod 700 ~/.ssh ; chmod 600 ~/.ssh/authorized_keys ; chown -R kun:kun ~/.ssh
   ```

4. **跳板机(ProxyJump)的 key 装在哪**
   ProxyJump 只是隧道,公钥认证发生在「**本地 ↔ 目标机**」之间 —— 公钥要装在**目标机**的用户上,**跳板机不持有它**。从本地装最稳(自动走跳板):`ssh-copy-id -i ~/.ssh/<key>.pub <目标别名>`。

5. **Debian 13 的 sshd 日志在 journald**(没有 `/var/log/auth.log`)
   `journalctl -t sshd -n 30 --no-pager`;`Authentication refused: bad ownership or modes …` = 权限(回第 3 条)。`ssh -v` 要在**本地笔记本**跑,不是服务器。

6. **顺序铁律(防锁门)**
   先建好 `kun` + 公钥 + sudo,**另开一个终端验证免密登录通过**,再去禁 root / 禁密码登录;每步保留旧会话、用新会话验证,通过了才关旧的;再留一个 VPS 厂商 Web Console 兜底。

## 二、防火墙 / 端口

1. **⚠ Docker 绕过 ufw 的本质:它直接改 iptables**
   `-p 宿主:容器` 发布端口时,Docker 在 `nat` 表做 **DNAT**、在 `filter` 表的 **FORWARD** 路径(`DOCKER` 链)放行;而 ufw 的规则在 **INPUT** 链。发往容器的流量是被**转发**进容器、**不经过 host 的 INPUT** → `ufw deny X` 管不到(且 Docker 规则还插在 ufw 之前)。
   - 真正管控:**云厂商防火墙**(在 Docker iptables 之前生效),或 [`ufw-docker`](https://github.com/chaifeng/ufw-docker)(往 Docker 预留的 `DOCKER-USER` 链写规则,FORWARD 里先于 `DOCKER` 链执行)。
   - **关键:只有 `ports:`(发布到宿主)才有此坑;`expose:`(仅容器网络)不映射宿主端口、外网根本到不了,与 ufw 无关。**

2. **本项目三仓:线上(prod compose)不受此坑影响,dev compose 受**
   - **prod `docker-compose.prod.yml`(线上用)**:oauth/image/galgame/web/wiki/api 全用 **`expose:`**;postgres/redis/minio/meili 连 expose 都没有 → **没有任何应用端口映射到宿主、不暴露公网**。生产唯一对外的是 Dokploy 的 Traefik(80/443)+ 面板 3000 → 需要「绕 ufw」处理的**只有 3000**(见三.4,用 `--publish-rm` 关最干净)。
   - ⚠ **dev `docker-compose.yml`(本地/测试用)用 `ports: 15xxx`** → 发布宿主端口、会绕过 ufw。**绝不要在公网服务器上跑 dev compose**,否则 `15000(pg)` / `15001(redis)` / `15002(minio)` … 一串会直接暴露公网且 ufw 拦不住。线上只用 prod compose。

3. **入站只需要 SSH / 80 / 443**(+ 初装临时的 3000)
   其余 postgres / redis / oauth / galgame / image / meili / minio 线上全是容器内网(`expose`),无需任何入站规则。临时从笔记本连库走 **SSH 隧道** `ssh -L`,别长期开端口。

4. **邮件不需要开任何入站**
   本项目发信走**外部 SMTP 中继**(mxroute `tuesday.mxrouting.net:587`),是**出站**连接;源站**不收信、不跑邮件服务**(MX 指向 mxroute)。ufw 默认放行出站即可。唯一留意:个别 VPS 封**出站 25**,但本项目用 **587(submission)**,基本不受影响。

## 三、Dokploy

1. **安装命令:`sudo` 要加在 `sh` 上**
   ```bash
   curl -sSL https://dokploy.com/install.sh | sudo sh
   ```
   `sudo curl … | sh` 是**错的** —— `sudo` 只管 `curl`,管道后的 `sh` 仍以普通用户跑 → 脚本报 `This script must be run as root`。更稳:`curl -fsSL …/install.sh -o /tmp/d.sh && sudo sh /tmp/d.sh`(还能先看一眼脚本)。

2. **装前自查**
   ① 内存 ≥ 2G、磁盘 ≥ 30G(本项目走 GHCR 预构建已大减构建负载);② **80/443/3000 必须空闲**,别先装别的 web 服务;③ 必须 **root 直接装、不要在 LXC 容器内**;④ 脚本用 `curl ifconfig.me` 取公网 IP 来 `docker swarm init` —— 该服务不可达会装失败(国内/出站受限尤其注意),可装后手动 `docker swarm init --advertise-addr <IP>`;⑤ 磁盘写满会让 Dokploy 内置 DB 进 recovery、面板打不开 → 定期 `docker system prune`。

3. **它装的 Docker 靠谱,但启用了 Swarm**
   装的是**官方 Docker**(`get.docker.com`),可靠。但会 `docker swarm init`,**应用都跑成 Swarm 服务**(不是普通 `docker compose`)。Swarm 小坑:Deploy 偶尔不更新运行中的容器 → 用 **Stop + Deploy**;挂载路径非法会「部署成功但其实没跑」。

4. **⚠ 面板 3000 默认对全网开放、ufw 管不住**(见二.1)
   收口三选一:**挂域名 + HTTPS**(下条,推荐)/ **SSH 隧道**(`ssh -L 3000:127.0.0.1:3000 …`)+ 云防火墙封 3000 / [`ufw-docker`](https://github.com/chaifeng/ufw-docker)。Cloudflare 只代理 80/443 的域名,**3000 不在其保护内**。
   彻底禁止 3000 端口只需要 `docker service update --publish-rm "published=3000,target=3000,mode=host" dokploy` （官方推荐），再次开启只需要 `docker service update --publish-add "published=3000,target=3000,mode=host" dokploy`

5. **面板 HTTP → HTTPS**
   Settings → **Server** → 填 **Web Server Domain** = `panel.kungal.com` + 管理员 Email + 开 **HTTPS(Let's Encrypt)** → Save。前提:A 记录 `panel.kungal.com → 服务器IP`,且签发时 80/443 对 LE 可达(走 CF 橙云则签发期临时 **DNS-only** 或用 **DNS-01**)。之后用 `https://panel.kungal.com`,再封掉直连 3000。

6. **用户 / 注册控制**
   **首个注册账号 = Owner(最高管理员)**;此后是**邀请制** —— `/register` 不再自助注册(建好 Owner 后访问会跳登录),新成员由 Owner 在 **Settings → Users** 邀请并分配角色。免费版角色 **Owner / Admin / Member**(Member 可按权限点精细授权;自定义角色是 Enterprise)。**默认已无开放注册,无需额外禁**;稳妥起见隐身窗口访问 `/register` 确认跳登录。

7. **移除 Dokploy ≠ Docker 变孤儿**
   Docker 是独立系统包(`docker-ce`),**不会被连带卸、也不会变孤儿**;但 Dokploy 会留下 Swarm 模式 + `dokploy`/`dokploy-traefik`/`dokploy-postgres`/`dokploy-redis` 服务 + `dokploy-network` + 卷,按[官方卸载](https://docs.dokploy.com/docs/core/uninstall)清:`docker service rm …` → `docker system prune --all --volumes` → `docker swarm leave --force`。注意应用是 Swarm 服务,迁出要改回普通 compose。

## 四、Cloudflare / 源站 IP

1. **不用 Cloudflare Tunnel**
   全部流量经**单个 `cloudflared` 进程**汇聚,是吞吐 / 连接瓶颈(每隧道默认 100 连接上限;CF 官方建议高流量跑 ≥2 个 4C4G replica),**并发一高就易 [Error 1033](https://developers.cloudflare.com/support/troubleshooting/http-status-codes/cloudflare-1xxx-errors/error-1033/)(找不到健康连接器)**。本项目改用 **Dokploy + Cloudflare 代理(橙云 / CDN)**。

2. **隐藏源站 = 橙云代理 + ufw 只放 Cloudflare 段**
   所有公网记录开 **Proxy(橙云)**(顺带 CDN/缓存/DDoS);防火墙只放行 `cloudflare.com/ips-v4|v6` 访问 80/443(用 [ufw-cf](https://github.com/Malith-Rukshan/ufw-cf) 定时同步,因为 CF 段会变)。详见 [QUICKSTART §10](./QUICKSTART.md)。

3. **⚠ 橙云下 Traefik 的 Let's Encrypt 签不出**
   橙云会拦截 LE 的 HTTP-01 验证 → Traefik 改用 **DNS-01**(填 Cloudflare API token)或挂 **Cloudflare Origin CA 证书**,CF 的 SSL 模式设 **Full (strict)**。**面板域名(三.5)同理**。

4. **防泄漏清单**
   DNS 审计(所有 A/AAAA/MX/TXT/旧子域都不能含源站 IP,别留 `dev.`/`staging.`);曾公开过的 IP 上 CF 后**换新 IP**(DNS 历史改不掉);邮件走外部中继(见二.3)。

## 五、数据库 / 迁移

1. **⚠ pg18 的卷路径变了**
   官方镜像把 `VOLUME` 从 `/var/lib/postgresql/data` 改到 `/var/lib/postgresql`(PGDATA=`…/18/docker`),所有 compose 的 pg 卷挂载点要同步改,否则数据不持久(本仓已改)。大版本升级见 [06-operations.md](./06-operations.md)(dump → 临时容器恢复)。

2. **迁移是「每个库各跑一次」,新迁移不用单独执行**
   `migrate -dir=up` 靠该库自己的 `_migrations` 表去重、按文件名顺序补跑未应用的。新增迁移(如 `013`)**不用在总流程里单独运行**,跟标准 `migrate` 步骤走即可;但**每套库都要各自跑一次**。非交互加 `-yes`。

3. **本地 dev 库 ≠ docker 库 ≠ 生产库,互相独立**
   air 本地 api 连 `localhost:5432`,docker 栈连 `localhost:15000`,生产又是另一套。**改了 schema,每套用到的库都要分别迁**(否则会报 `relation … does not exist`)。

4. **Meili 跨版本不兼容旧卷**
   升级清 `…_meili` 卷 + `go run ./cmd/reindex-search` 重建(索引是从 Postgres 派生的,可重建)。

## 六、依赖版本(2026-06 核对结论)

- **GitHub Actions** 已全升最新:`actions/checkout@v6`、`docker/setup-buildx-action@v4`、`docker/login-action@v4`、`docker/build-push-action@v7`。
- **镜像**:`postgres:18-alpine`、`redis:8-alpine`、`getmeili/meilisearch:v1.45` 均为最新大版本。
- **Node 24 是当前 LTS**(Node 26 已发但 2026-10 才转 LTS,**勿提前上 26**)。
- **⚠ MinIO 官方社区镜像已停更 / 归档**(约 2026-04);生产图床走 **Cloudflare R2**,影响小;长期自托管可换 Chainguard 或其它 S3 实现。
