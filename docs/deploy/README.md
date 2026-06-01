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
| 附录 | [08-dae-dev-proxy.md](./08-dae-dev-proxy.md) | **仅开发机**:dae 透明代理下让容器走代理(生产纯净,勿叠加) |

## 30 秒速览

- **三个仓库**:`kun-oauth-admin`(枢纽 / hub)、`kun-galgame-nuxt4`(kungal / 论坛)、`kun-galgame-patch-next`(moyu / 补丁站)。
- **枢纽拥有共享基础设施**:一套 Postgres(5 个库)、Redis、MinIO(S3)、Meilisearch。kungal/moyu 按服务名连过来。
- **每仓 = 无状态 api + web 容器**;Go 服务多阶段编译,Nuxt 出自包含 `.output`。
- **全部 host 端口在 `1xxxx` 段**,与本机 `air` 开发服务共存。
- 整套在测试机上**已实跑通过**:13 个容器全 healthy,跨仓服务名连通已验证。

## 一条命令看全局

```bash
docker ps --format '{{.Names}}\t{{.Status}}' | grep -E 'kun-oauth-admin-|moyu-|kungal-' | sort
```

> 文档里所有密钥、密码均为**测试值**(`191007` / `kun-docker-test-*` / `minioadmin`)。生产部署见 [05-configuration.md](./05-configuration.md) 的「密钥」一节,务必全部轮换。
