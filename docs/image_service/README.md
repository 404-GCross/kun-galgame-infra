# Image Service 设计文档

这里是 **kun-oauth-admin** 即将新增的集中式图片服务（临时代号 `image_service`）的设计与工程文档。服务目标是替代 kungal / moyu / galgame wiki 三个站点各自独立的图片处理逻辑，成为一个通用的 "hash-addressed blob store + 元数据 + 审核" 平台。

## 文档索引

| # | 文件 | 内容 |
|---|------|------|
| 01 | [design.md](./01-design.md) | 背景、目标、核心架构决策、关键权衡 |
| 02 | [storage-and-schema.md](./02-storage-and-schema.md) | 对象存储路径设计、数据库 schema、站点配置 |
| 03 | [api-design.md](./03-api-design.md) | 对外 API 接口规范（上传、元信息、变体 URL） |
| 04 | [migration-plan.md](./04-migration-plan.md) | 从三个旧站点迁移到新服务的分阶段计划 |
| 05 | [engineering-plan.md](./05-engineering-plan.md) | 工程里程碑 M1–M5、交付物、验收标准 |

## 一句话总结

> **图片服务不管业务实体、不存引用关系，只管 hash 对应的二进制 + 元数据 + 审核结果。派生尺寸由 imgproxy 按需生成。鉴权复用 OAuth。**

## 关键决策速查

- ✅ **复用 OAuth**：不新增 API Key 体系，沿用 `oauth_client` 作为"站点"的 source of truth
- ✅ **内容寻址**：存储 key = `sha256(content)`，天然去重、天然幂等
- ✅ **调用方管引用**：`users.avatar_image_hash` 放在各调用方库里，不在图片服务
- ✅ **按需派生**：imgproxy + 签名 URL，不在上传时预生成多尺寸
- ✅ **分层审核**：同步拦高置信度违规 + 异步深度审核
- ✅ **软清理**：靠 `last_referenced_at` + TTL，不用引用计数

## 非目标

- ❌ 不是通用 CDN / 文件仓库（只接图片，不接视频、PDF、任意文件）
- ❌ 不做图片编辑器（裁剪、滤镜、水印等由调用方自行处理后再上传，或通过 imgproxy 参数）
- ❌ 不做图床（不对外公开上传接口，仅服务已注册的 OAuth Client）
