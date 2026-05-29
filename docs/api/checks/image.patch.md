# Image 服务 — PATCH API 清单

> 服务: **image** — 管理端，跑在 oauth 进程（`cmd/oauth/main.go` 的 `registerImageAdmin`），`/api/v1/admin/image`
>
> 图例见 [README](./README.md)。配套: [image.get.md](./image.get.md) · [image.post.md](./image.post.md) · [image.delete.md](./image.delete.md)
>
> **审计完成** —— 🔧 已修 / ✅ 已审计无问题（本轮字段对齐/越权/SQL注入/副作用扫描未发现可处理问题）。详见 [README 审计结果](./README.md#审计结果2026-05-29)。

## 统计

- 本服务 PATCH 端点：**1**（管理端审核）

---

| 路径 | 鉴权 | Handler | 状态 | 备注 |
|---|---|---|---|---|
| `PATCH /api/v1/admin/image/:hash/review` | ⚙️ | `imgHandler.Admin.Review` | 🔧 | 人工审核（通过/拒绝/复核）；#18 jsonb_set 合并 manual_reason，保留自动审核标签 |
