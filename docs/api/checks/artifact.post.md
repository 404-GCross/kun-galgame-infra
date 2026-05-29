# Artifact 服务 — POST API 清单

> 服务: **artifact**（`apps/api/cmd/artifact`） · Base URL: `/api/v1` · 路由源: `cmd/artifact/main.go`
>
> 图例见 [README](./README.md)。配套: [artifact.get.md](./artifact.get.md) · [artifact.delete.md](./artifact.delete.md)
>
> **inventory 阶段** —— 状态列 ⏳。本服务无 PUT / PATCH。

## 统计

- 本服务 POST 端点：**1**

---

| 路径 | 鉴权 | Handler | 状态 | 备注 |
|---|---|---|---|---|
| `POST /api/v1/artifacts` | 🔒 | `artifactH.Create` | ⏳ | 创建制品 |
