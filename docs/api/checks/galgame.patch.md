# Galgame Wiki 服务 — PATCH API 清单

> 服务: **galgame**（`apps/api/cmd/galgame`） · Base URL: `/api` · 路由源: `cmd/galgame/main.go`
>
> 图例见 [README](./README.md)。配套: [galgame.get.md](./galgame.get.md) · [galgame.post.md](./galgame.post.md) · [galgame.put.md](./galgame.put.md) · [galgame.delete.md](./galgame.delete.md)
>
> **inventory 阶段** —— 状态列 ⏳。

## 统计

- 本服务 PATCH 端点：**1**

---

| 路径 | 鉴权 | Handler | 状态 | 备注 |
|---|---|---|---|---|
| `PATCH /api/galgame/:gid` | 🔒 | `submissionH.PatchDraft` | ⏳ | 编辑自己的草稿投稿 |
