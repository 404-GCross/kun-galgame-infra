# Artifact 服务 — DELETE API 清单

> **更正（2026-06）**：moderation / artifact **已有独立服务与路由**（`cmd/moderation` / `cmd/artifact`）；下文「未实现」为旧表述，仅指本轮未做字段对齐审计。


> 服务: **artifact**（`apps/api/cmd/artifact`） · Base URL: `/api/v1` · 路由源: `cmd/artifact/main.go`
>
> 图例见 [README](./README.md)。配套: [artifact.get.md](./artifact.get.md) · [artifact.post.md](./artifact.post.md)
>
> **未审计** —— 该服务已有独立 cmd（`cmd/moderation` / `cmd/artifact`，未接入 oauth 主进程），本轮字段审计未覆盖，状态 待审计。

## 统计

- 本服务 DELETE 端点：**1**

---

| 路径 | 鉴权 | Handler | 状态 | 备注 |
|---|---|---|---|---|
| `DELETE /api/v1/artifacts/:id` | 登录 | `artifactH.Delete` | 待审计 | 删除制品 |
