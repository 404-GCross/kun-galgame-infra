# Galgame Wiki 服务 — PATCH API 清单

> 服务: **galgame 面**（宿主 `cmd/catalog`,装配 `apps/api/internal/galgameapp`;独立 `galgame` 二进制已于 wiki 退役 W5 移除） · Base URL: `/api` · 路由源: `internal/galgameapp/mount.go`
>
> 图例见 [README](./README.md)。配套: [galgame.get.md](./galgame.get.md) · [galgame.post.md](./galgame.post.md) · [galgame.put.md](./galgame.put.md) · [galgame.delete.md](./galgame.delete.md)
>
> **审计完成** —— 已修 / 已审计无问题（本轮字段对齐/越权/SQL注入/副作用扫描未发现可处理问题）。详见 [README 审计结果](./README.md#审计结果2026-05-29)。

## 统计

- 本服务 PATCH 端点：**1**

---

| 路径 | 鉴权 | Handler | 状态 | 备注 |
|---|---|---|---|---|
| `PATCH /api/galgame/:gid` | 登录 | `submissionH.PatchDraft` | 已修 | 编辑自己的草稿投稿；#23 PatchDraft vndb_id 格式/唯一校验 |
