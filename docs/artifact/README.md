# Artifact 服务 设计文档

这里是 **kun-galgame-infra** 即将补全的集中式**大文件**上传/下载服务（临时代号 `artifact`）的设计与工程文档。服务目标是替代 kungal / moyu / touchgal / galgame wiki 各站各自的大文件直传逻辑，成为一个通用的「**私有 blob 存储 + 预签名直传直下 + 可选游戏启动清单**」平台。

它与 [`image_service`](../image_service/README.md)（图床）互补：图床只接 `image/*`、内容寻址、小文件、公开 CDN；artifact 接任意大文件、uuid 寻址、GB 级、**私有桶 + 预签名**。两者共享 OAuth Client 租户模型，但对象存储桶 / 数据库 / CDN 拓扑独立。

设计基线 = 作者已在生产验证的 **B2 + Cloudflare** 方案：[kungal.com/topic/2732](https://www.kungal.com/topic/2732)、[soft.moe r2-cf-worker](https://www.soft.moe/topic/technology-deploy-r2-cf-worker)。

## 文档索引

| # | 文件 | 内容 | 状态 |
|---|------|------|------|
| 01 | [design.md](./01-design.md) | 背景、目标、服务边界（图床 vs artifact）、整体架构、上传/下载流程、核心设计决策、技术栈、风险 | ✅ |
| 02 | [storage-and-schema.md](./02-storage-and-schema.md) | B2 对象存储布局、私有桶 + 双密钥、生命周期/GC、`kun_artifacts` schema、OAuth Client 扩展、配额 | ✅ |
| 03 | [api-design.md](./03-api-design.md) | 对外 API：Init / Complete / Download / Delete / List / Get（含 multipart 契约） | ⏳ 待写 |
| 04 | [cloudflare-worker.md](./04-cloudflare-worker.md) | 私有桶经 CF Worker 分发：token 刷新、缓存、header 剥离、防盗链 | ⏳ 待写 |
| 05 | [engineering-plan.md](./05-engineering-plan.md) | 工程里程碑、交付物、迁移（空表下沉独立库）、CI/部署 | ⏳ 待写 |
| 06 | [integration-guide.md](./06-integration-guide.md) | **调用方视角**：OAuth 注册、直传 SDK、前端 multipart 分片、降级 | ⏳ 待写 |

## 一句话总结

> **artifact 服务只管「uuid 对应的一个私有大文件 + 元数据」。上传/下载都靠服务端短时效预签名 URL，客户端直连 B2、API 不过字节；桶恒私有；生命周期由状态机 + 孤儿 GC + 软删 TTL 驱动；游戏制品可选挂 manifest。**

## 关键决策速查

- ✅ **桶恒私有 + 预签名直传直下** —— 绝不公开桶；上传 30m–1h、下载 ~1h 短时效签发，防直链盗刷（决策 0）
- ✅ **复用 OAuth** —— 不新增凭证，沿用 `oauth_client` 作「站点」registry，加 `artifact_*` 字段（决策 1）
- ✅ **uuid 寻址 + site 前缀** —— `key={site}/{uuid}/{name}`，**不**内容寻址、不跨站去重（刻意区别于图床；决策 2）
- ✅ **两段式直传** —— `<50MB` 单段 presigned PUT；`≥50MB` S3 multipart 分片 presigned 并发上传（决策 3）
- ✅ **Complete 时 HeadObject 校验大小** —— 挡配额欺骗；全量校验/病毒扫描可插拔、v1 noop（决策 4）
- ✅ **生命周期** —— `status` 0/1/2 + 孤儿 GC（`status=0` 超时）+ 软删 TTL 物理回收（决策 5）
- ✅ **最小权限密钥** —— `presigner`（签发）/ `cleanup`（仅删，只给 GC）分离（决策 6）
- ✅ **下载分发** —— 默认私有预签名 GET；`public` + 站点 `artifact_cdn_base` 时走 CF Worker 缓存域名（B2→CF 出流量免费；决策 7）
- ✅ **manifest 可选** —— 调用方在 Complete 时提交结构化清单，不从压缩包解（决策 8）

## V1 必要性下限（不可拆）

V1 上线**必须**包含：

1. OAuth Client 鉴权（Basic S2S + Bearer JWT `artifact:upload`，fail-closed site 匹配）
2. `kun_artifacts` 库 + `artifacts`（+ 可选 `manifests`）表
3. storage 客户端：presigned PUT / GET + S3 multipart（Create/PresignPart/Complete/Abort）+ HeadObject + Delete
4. Init / Complete / Download / Delete / List / Get 写实（替换现有 `// TODO` 桩）
5. Complete 时 `HeadObject` 大小校验
6. 每站每日「文件数 + 字节数」配额 + 单文件上限
7. 孤儿 + 软删 GC（`internal/jobs`）

> 缺配额 / 私有桶 → 上线即被刷爆账单（`GetObject` 计费）
> 缺孤儿 GC → 直传未完成的对象越积越多，存储漏计

## 非目标

- ❌ 不接管图片（`image/*` 走 `image_service`）
- ❌ 不做在线解压 / 转码 / 改写文件内容（原样存取）
- ❌ 不做面向匿名用户的公开上传端点（仅服务已注册 OAuth Client）
- ❌ v1 不做服务端全量校验 / 病毒扫描（可插拔延后）
- ❌ v1 不做断点续传（multipart 重传由前端处理）

## 跨仓契约说明

artifact 目前**尚无下游接入**，故未纳入 `kungal-docs` 的 `docs:sync` 镜像体系。待 03（API）/ 06（接入指南）完成、下游开始接入时，再把对外契约部分登记进 `../kungal-docs` 的 ownership 并下发镜像（参照 `image_service` 的做法）。本目录现阶段是**纯本仓设计文档**。
