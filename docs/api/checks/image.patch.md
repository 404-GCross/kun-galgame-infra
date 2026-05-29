# Image 服务 — PATCH API 清单

> 服务: **image** — 管理端，跑在 oauth 进程（`cmd/oauth/main.go` 的 `registerImageAdmin`），`/api/v1/admin/image`
>
> 图例见 [README](./README.md)。配套: [image.get.md](./image.get.md) · [image.post.md](./image.post.md) · [image.delete.md](./image.delete.md)
>
> **inventory 阶段** —— 状态列 ⏳。

## 统计

- 本服务 PATCH 端点：**1**（管理端审核）

---

| 路径 | 鉴权 | Handler | 状态 | 备注 |
|---|---|---|---|---|
| `PATCH /api/v1/admin/image/:hash/review` | ⚙️ | `imgHandler.Admin.Review` | ⏳ | 人工审核（通过/拒绝/复核）|
