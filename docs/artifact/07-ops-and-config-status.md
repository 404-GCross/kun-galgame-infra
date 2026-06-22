# 07 — 运维与配置现状

> 本篇是 artifact 服务运维/配置现状清单。最后更新：2026-06-22。
>
> **🚀 生产已全量上线（2026-06-22）。** `infra-artifact` 服务已部署（:9279，healthy）、`kun_artifacts` 已建库建表、B2 桶 `kungal-artifact-v1` + 密钥已配、`oauth_clients.artifact_*` 列已迁移、专用下载域 **`dl.imoe.uk`**（Cloudflare Worker）已上线。**首个生产下游 = moyu**：补丁文件已**全量回填**进 artifact（统一 opaque key `{site}/{uuid}.<ext>` + 原文件名经 `Content-Disposition` 保留），经 `dl.imoe.uk` 提供下载。平台本身已就绪——新下游（forum / letmoe / …）只差**各自 client 的 ren 运维开通**（见「生产部署 — 已完成」表步骤 7），**不是 infra 阻塞项**。

## 现状速览

| 项 | 本地 dev | 生产 |
|----|---------|------|
| 代码（Phase 1 服务 + opaque key + 原名 CD）| ✅ 已完成 | ✅ 已完成 |
| 设计/契约文档 01–10 | ✅ 已完成 | ✅ 已完成 |
| CI 镜像 `infra-artifact` | ✅ 已完成 | ✅ 已构建 + 部署 |
| 元数据库（`artifacts`/`manifests`）| ✅ `kun_artifacts` 已建表 | ✅ `kun_artifacts` 已建库建表（8200+ 制品）|
| `oauth_clients.artifact_*` 列 | ✅ 已 `cmd/migrate` 加列 | ✅ 已 `cmd/migrate` 加列 |
| 对象存储（B2 桶 + 密钥）| ✅ `kungal-artifact-v1` @ `us-east-005` | ✅ 同桶 + 密钥已配 |
| 服务进程（`cmd/artifact`）| ✅ 可本地启动 | ✅ Dokploy 已部署（:9279 healthy）|
| 站点接入（OAuth client `artifact_*`）| 按需 | ✅ moyu 已开通；其余下游按需 ren 开通 |
| Cloudflare Worker（公开下载，专用域 `dl.imoe.uk`）| — | ✅ `dl.imoe.uk` 已上线（cdn_base 已配）|

> **代码在 `main`；生产已部署并上线（2026-06-22），首个下游 moyu 已切换 + 存量已回填。**

## forum/moyu 接管路线 —— moyu 已完成（2026-06-22），forum 暂缓

把 kungal(forum)工具资源 + moyu 补丁资源的上传/下载收口到 artifact。设计见 [08](./08-migration-forum-moyu.md) / [09](./09-download-domain-and-worker.md) / [10](./10-openapi-and-clients.md)。**moyu 已走完全程**：

1. ✅ **artifact code-first**(Huma 叠 Fiber v3 + house 信封)→ 导出 OAS 3.1。([10](./10-openapi-and-clients.md)) （登记进 `../kungal-docs` 仍待办。）
2. ✅ **专用下载域 `dl.imoe.uk`** + Cloudflare Worker(缓存公开下载)。([09](./09-download-domain-and-worker.md))
3. ✅ **配 moyu client**:`artifact_enabled` + `site_key=moyu` + `allowed_mime` + `max_file_size` + `cdn_base=https://dl.imoe.uk`。
4. ✅ **集成(forward-only)**:moyu 后端改调 artifact(生成的 Go client);存 `artifact_uuid`;dual-read 下载。前端 `/upload/*` 契约不变。
5. ✅ **回填**:`adopt-moyu-resources` 服务端 CopyObject 搬 8200+ 存量(同 region/账号)+ 回写 `artifact_uuid`(统一 opaque key + 原名 CD)。
6. **退役(剩余)**:老桶 `kun-galgame-patch` 验证期后置只读再删 + 退 `oss.moyu.moe`;清理 moyu 侧已替换的内联 upload service。

**forum**:同一套路线,因 forum 重构**暂缓**(`galgame_toolset_resource`;artifact 平台已就绪)。**letmoe / 其它新下游**:只差各自 client 的 ren 开通(步骤 3,一条 SQL)。touchgal 不在范围内。

## 已完成（本轮）

- 代码：`storage`(presign+multipart) / 配置 / `oauth_client.artifact_*` / `ClientAuth` / Init·Complete·Download·Get·List·Delete / Redis 配额 / `artifact-gc` / 错误码 50001–50017。
- 命令：`cmd/artifact`（HTTP，门控）、`cmd/migrate-artifact`（**DB-only 迁移**，无需 S3 即可建表；prod 经 `infra-tools` 镜像可用）。
- CI：`build.yml` 矩阵加 `infra-artifact`。
- 单测：纯函数（MIME 白名单 / 文件名清洗 / RFC5987 编码）。
- 文档：`docs/artifact/01–07`。
- **本地 dev 已打通**：本地库名已与生产对齐（`kun_artifacts`，不再用 `_dev`/`_test` — 见 `reset_all.sh` / `initdb`），`cmd/migrate-artifact` 建表 + `cmd/migrate` 加 `oauth_clients.artifact_*` 列；`apps/api/.env` artifact 段已配（`KUN_ARTIFACTS_PG_DATABASE=kun_artifacts` + B2）。
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

## 生产部署 —— 已完成（2026-06-22）

下列步骤已在生产执行（`infra-tools` 镜像 + trust-psql + Dokploy）：

| # | 步骤 | 状态 |
|---|------|------|
| 1 | 建库 `kun_artifacts` | ✅ |
| 2 | 私有 B2 桶 `kungal-artifact-v1` + 密钥 + 生命周期（Abort ≥1d multipart）| ✅（**当前服务/presigner 复用同一把账号级 key**；待办：拆专用 `cleanup`/worker 只读 key + 轮换早期共享 key）|
| 3 | `cmd/migrate-artifact`（建 `artifacts`/`manifests`）| ✅ |
| 4 | `cmd/migrate`（`oauth_clients` 加 `artifact_*` 列）| ✅ |
| 5 | 部署 `infra-artifact`（Dokploy，:9279，DB+S3 env）| ✅ Up healthy |
| 6 | 专用下载域 `dl.imoe.uk` + Cloudflare Worker | ✅ 上线（e2e 验证：opaque URL + 原名 + cache HIT）|
| 7 | 配站点 OAuth Client（**ren-only**）| ✅ **moyu**（`artifact_enabled` / `site_key=moyu` / 配额 / `allowed_mime` / `cdn_base=https://dl.imoe.uk`）；**forum / letmoe 等新下游：各自 client 待 ren 按需开通**（一条 SQL，同 moyu）+ 授予 `artifact:upload` scope。见 [01 决策 9](./01-design.md#决策-9权限控制--artifact-能力全部-ren莲-only默认关闭)、文件浏览/存储配置管理 UI 亦 ren-only |
| 8 | B2 桶 CORS（前端直传）| ✅ |
| 9 | moyu 存量回填（`adopt-moyu-resources`，服务端 CopyObject）| ✅ 8200+ 文件（opaque key + 原名 CD）|

**剩余（非阻塞）**：① `dl.imoe.uk` 加 response-header 规则剥 `x-bz-*`（**不要加静态 `Content-Disposition`** — 会覆盖每文件原名）。② 启用 `artifact-gc`（给 oauth 进程补 `KUN_ARTIFACTS_PG_DATABASE` + `KUN_ARTIFACT_S3_*`，含 cleanup key）。③ 轮换早期共享 B2 key + 拆专用 key。④ 把对外契约（03/06 + OAS）登记进 `../kungal-docs` + `docs:sync`。⑤ forum 接入（设计就绪，因 forum 重构暂缓）。⑥ 自托管 UGC 文件的审核/合规决策（[09 §18.2](./09-download-domain-and-worker.md)，产品侧）。

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
