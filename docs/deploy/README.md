# 鲲 Galgame 生态 — Docker 部署与运维文档

三仓一体的容器化部署指南。本目录按章节分文件编写,**建议按顺序阅读**。

| 章节 | 文件 | 内容 |
|---|---|---|
| 0 | [00-architecture.md](./00-architecture.md) | 架构总览:三仓、服务拓扑、网络、端口、数据库映射 |
| 1 | [01-prerequisites.md](./01-prerequisites.md) | 前置条件:Docker、构建网络、buildx 现状、仓库布局 |
| 2 | [02-build.md](./02-build.md) | 镜像构建:hub 的 cgo/distroless 拆分、moyu/kungal、构建参数 |
| 3 | [03-bootstrap.md](./03-bootstrap.md) | **首次启动**:基础设施、建库、跨仓迁移顺序、OAuth 客户端注册 |
| 4 | [04-run.md](./04-run.md) | 日常启停:增量启动 / 伞状编排、kungal 的 hub override |
| 5 | [05-configuration.md](./05-configuration.md) | 配置参考:各服务 env、前端 public 配置烘焙、密钥 |
| 6 | [06-operations.md](./06-operations.md) | 运维:健康/日志、升级、备份恢复、迁移 job、扩缩容 |
| 7 | [07-troubleshooting.md](./07-troubleshooting.md) | 故障排查:实跑中踩到的每一个坑 + 解法 |
| 12 | [12-dokploy.md](./12-dokploy.md) | **Dokploy 部署(线上推荐)**:单服务器自托管 PaaS,内置 Traefik 反代 + 自动 SSL + 编排;含真实域名映射与改造清单 |
| 13 | [13-registry-ci.md](./13-registry-ci.md) | **镜像 Registry + CI 构建**:GHCR + GitHub Actions 在 CI build → 推 GHCR → Dokploy 拉预构建镜像(生产机零构建);镜像清单 / workflow / tag 回滚 / prod compose 用 `image:` |
| 9 | [09-edge-caddy.md](./09-edge-caddy.md) | 手动边缘反代 · Caddy(**不用 Dokploy 时**):自动 HTTPS、域名映射、§9.0 共同前提 |
| 10 | [10-edge-nginx.md](./10-edge-nginx.md) | 手动边缘反代 · Nginx:手动 TLS(certbot)、WS 升级头、容器名回源 |
| 11 | [11-edge-cloudflare-tunnel.md](./11-edge-cloudflare-tunnel.md) | 手动边缘反代 · Cloudflare Tunnel:纯出站、零入站端口(NAT/dae 后首选) |
| 附录 | [08-dae-dev-proxy.md](./08-dae-dev-proxy.md) | **仅开发机**:dae 透明代理下让容器走代理(生产纯净,勿叠加) |

> **线上采用单服务器 + Dokploy**(见 [12-dokploy.md](./12-dokploy.md)):它内置 Traefik 已是反代,**与 09-11 三选一互斥,勿叠加**。线上域名:kungal=`kungal.com`/`www.kungal.com`、moyu=`moyu.moe`/`www.moyu.moe`、wiki=`wiki.kungal.com`、oauth=`oauth.kungal.com`、image=`image.kungal.iloveren.link`。

## 30 秒速览

- **三个仓库**:`kun-galgame-infra`(枢纽 / hub)、`kun-galgame-nuxt4`(kungal / 论坛)、`kun-galgame-patch-next`(moyu / 补丁站)。
- **枢纽拥有共享基础设施**:一套 Postgres(5 个库)、Redis、MinIO(S3)、Meilisearch。kungal/moyu 按服务名连过来。
- **每仓 = 无状态 api + web 容器**;Go 服务多阶段编译,Nuxt 出自包含 `.output`。
- **全部 host 端口在 `1xxxx` 段**,与本机 `air` 开发服务共存。
- 整套在测试机上**已实跑通过**:13 个容器全 healthy,跨仓服务名连通已验证。

## 一条命令看全局

```bash
docker ps --format '{{.Names}}\t{{.Status}}' | grep -E 'kun-galgame-infra-|moyu-|kungal-' | sort
```

> 文档里所有密钥、密码均为**测试值**(`191007` / `kun-docker-test-*` / `minioadmin`)。生产部署见 [05-configuration.md](./05-configuration.md) 的「密钥」一节,务必全部轮换。
