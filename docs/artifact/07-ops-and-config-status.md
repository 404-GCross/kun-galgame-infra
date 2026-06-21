# 07 — 运维与配置现状（待办清单）

> 本篇是 artifact 服务「**还差什么、什么还没配**」的**唯一权威清单**。每完成一项就勾掉。最后更新：2026-06-21。

## 现状速览

| 项 | 本地 dev | 生产 |
|----|---------|------|
| 代码（Phase 1 服务）| 已完成：已实现、build/vet/test 绿 | 已完成：同（随仓库）|
| 设计/契约文档 01–07 | 已完成 | 已完成 |
| CI 镜像 `infra-artifact` | 已完成：已加入 `build.yml` 矩阵 | 进行中：待 push 触发构建 |
| 元数据库（`artifacts`/`manifests`）| 已完成：`kun_artifacts_dev` 已建表 | 未完成：待建 `kun_artifacts` |
| `oauth_clients.artifact_*` 列 | 已完成：已 `cmd/migrate` 加列 | 未完成：待随部署跑 `cmd/migrate` |
| 对象存储（B2 桶 + 密钥）| 已完成：`kungal-artifact-v1` @ `us-east-005`（presigner 单密钥）| 未完成：待配置 |
| 服务进程（`cmd/artifact`）| 已就绪：B2 已配，可 `go run ./cmd/artifact` 启动 | 未完成：待 Dokploy 部署 |
| 站点接入（OAuth client `artifact_*`）| 未完成：待按需配 | 未完成：待按需配 |
| Cloudflare Worker（公开下载，可选）| 未完成 | 未完成 |

> **代码已在 `main`（本轮 abort-fix / 清理待推送）；生产未部署**——生产完全未受影响。

## 已完成（本轮）

- 代码：`storage`(presign+multipart) / 配置 / `oauth_client.artifact_*` / `ClientAuth` / Init·Complete·Download·Get·List·Delete / Redis 配额 / `artifact-gc` / 错误码 50001–50017。
- 命令：`cmd/artifact`（HTTP，门控）、`cmd/migrate-artifact`（**DB-only 迁移**，无需 S3 即可建表；prod 经 `infra-tools` 镜像可用）。
- CI：`build.yml` 矩阵加 `infra-artifact`。
- 单测：纯函数（MIME 白名单 / 文件名清洗 / RFC5987 编码）。
- 文档：`docs/artifact/01–07`。
- **本地 dev 已打通（DB 侧）**：建库 `kun_artifacts_dev` → `cmd/migrate-artifact` 建表 → `cmd/migrate` 给本地 `oauth_clients` 加 `artifact_*` 列 → `.env` 加 artifact 段（`KUN_ARTIFACTS_PG_DATABASE=kun_artifacts_dev`，S3 段留 TODO）。
- **2026-06-21 加固 + 本地 B2 接通**：
  - **修 multipart 泄漏**：`CompleteMultipart` 失败时补 `AbortMultipart`（之前只置 `status=2`，泄漏的分片永不回收——孤儿 GC 只扫 `status=0`；B2 按未完成 multipart 分片计费）。对齐 [01 风险表](./01-design.md)「multipart 完成失败 → status=2 + Abort」。
  - **删未接线脚手架**：移除 `internal/platform/artifact/pipeline/`（`Checksum`/`VirusScan`/`ManifestValidator` 全是 TODO no-op、零引用），避免「以为扫了毒其实没扫」的误导；真要做时随 Phase 3 实装。
  - **`Get` 补空 uuid 守卫**（与 Complete/Download 一致）。
  - **本地 B2 已配**：bucket `kungal-artifact-v1` @ `us-east-005` 写入 `apps/api/.env`，`KUN_ARTIFACT_UPLOAD_ENABLED=true`。

## 待办 —— 本地 dev（B2 已配，剩 e2e 验证）

B2 凭证已写入 `apps/api/.env`（bucket `kungal-artifact-v1` @ `us-east-005`，`KUN_ARTIFACT_UPLOAD_ENABLED=true`）。剩余：

1. `go run ./cmd/artifact` 启动（启动时 `EnsureBucket` + AutoMigrate），`GET /healthz` 返回 ok。
2. 端到端验证 init→PUT→complete→download。
3. 前端直传还需在桶上配 **CORS**（允许来源 `PUT`/`GET`，`exposeHeaders` 含 `ETag`）。

## 待办 —— 生产（全部待你/运维）

| # | 步骤 | 说明 |
|---|------|------|
| 1 | 建库 `kun_artifacts` | `CREATE DATABASE kun_artifacts;` |
| 2 | 建 **私有** B2 桶 + 三把密钥 | `presigner`(put/multipart/sign-get/head) · `cleanup`(仅 delete) · worker(只读，给 CF Worker) |
| 2b | **加桶生命周期规则**：自动 Abort 超期（≥1d）未完成的 multipart 上传 | 兜底回收任何泄漏分片，独立于代码路径（代码已在完成失败时 Abort，这是双保险）|
| 3 | 跑 `cmd/migrate-artifact` | 经 `infra-tools` 镜像；建 `artifacts`/`manifests` 表 |
| 4 | 跑 `cmd/migrate` | 给 `oauth_clients` 加 `artifact_*` 列（**见下方部署安全**）|
| 5 | 给 **oauth 服务**补 artifact env | `KUN_ARTIFACTS_PG_*` + `KUN_ARTIFACT_S3_*`（尤其 cleanup 密钥）——因为 `artifact-gc` 跑在 oauth 进程；重部 oauth |
| 6 | 部署 `infra-artifact` | Dokploy 新服务，端口 9279，注入 DB+S3 env；先 `UPLOAD_ENABLED=false` 验证 list/get/download/healthz |
| 7 | 配站点 OAuth Client | 目标站 `artifact_enabled=true`、`artifact_site_key`、配额、scope 加 `artifact:upload`、可选 `artifact_cdn_base`。**授予 `artifact:upload` 仅 ren（莲）可操作**（前端对非 ren 隐藏、后端 ren-gate 兜底）；`artifact_*` 配置列 SQL-only，由 ren 运维设置。见 [01 决策 9](./01-design.md#决策-9权限控制--artifact-能力全部-ren莲-only默认关闭) |
| 8 | 配 B2 桶 CORS | 前端直传需要（同本地 §2）|
| 9 | （可选）Cloudflare Worker | 公开/热门内容免流量下载，见 [04](./04-cloudflare-worker.md)。**Worker 必须强制 `Content-Disposition: attachment`（或安全 Content-Type）**：直传 PUT 不固定 Content-Type，公开制品若被存成 `text/html` 经 Worker 可能在 CDN 域名内联渲染（预签名 GET 路径已固定 attachment，无此问题）|
| 10 | 灰度 `KUN_ARTIFACT_UPLOAD_ENABLED=true` | 端到端验证后放量 |

环境变量全表见 [05 §环境变量](./05-engineering-plan.md#环境变量)。

## 生产安全：不影响 oauth / image / wiki 等现有服务

本轮改动对共享代码（`pkg/config`、`pkg/errors`、`oauth_client` 模型、`internal/jobs`）均**增量**，但部署时有一处要注意：

- **`oauth_clients` 加列的时机**：现有服务对 `oauth_clients` 的**读**是 GORM `SELECT *`，即使列还没加也**不报错**（新字段读成零值，`artifact_enabled=false`）。但**写**（admin 编辑 client 走 `db.Save`）会带上 `artifact_*` 列，**列不存在则 UPDATE 失败**。
  → **部署顺序**：跑 `cmd/migrate`（加列，步骤 4）**先于或同步于**部署带本轮代码的 oauth/image/artifact。这与图床当初加 `image_*` 列同理。
- **`artifact-gc` 已做空配置保护**：该任务注册在 oauth 进程，但 `RunArtifactGC` 在 `cfg.ArtifactS3.AccessKeyID == ""` 时直接返回 `{"skipped":...}`，**不连库、不报错**。所以即使 oauth 还没配 artifact env，每天 05:30 的调度也只是 no-op，不会产生错误噪音或拖垮 oauth。
- **`cmd/migrate` 移除了 artifact 模型**：主库 `kun_galgame_infra` 不再 AutoMigrate `artifacts`/`manifests`；早期脚手架在主库建的两张**空表**保持原样（AutoMigrate 从不删表），确认 0 行后可手动 `DROP TABLE` 清理。
- **OAuth API 响应新增 `artifact_*` 字段**：client 元数据 JSON 多了这些字段（默认 false/空），下游解析器忽略未知字段即可，无破坏。

结论：**只要部署时按上面的顺序加列，oauth / image / wiki 等服务零影响**；artifact 自身在 B2 配好前保持「DB 就绪、服务待启」状态。

## Phase 3（后续增强）

可插拔病毒扫描 worker（ClamAV / 云）、服务端全量 checksum 复算、从压缩包解析 manifest、断点续传、管理端「全站制品」视图。届时把对外契约（03/06）登记进 `../kungal-docs` 并 `docs:sync` 下发 forum/patch 镜像。

---

← 返回 [README 索引](./README.md)
