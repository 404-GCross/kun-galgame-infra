# Moderation 服务 — POST API 清单

> ⚠️ **更正（2026-06）**：moderation / artifact **已有独立服务与路由**（`cmd/moderation` / `cmd/artifact`）；下文「未实现」为旧表述，仅指本轮未做字段对齐审计。


> 服务: **moderation**（`apps/api/cmd/moderation`） · Base URL: `/api/v1` · 路由源: `cmd/moderation/main.go`
>
> 图例见 [README](./README.md)。配套: [moderation.get.md](./moderation.get.md)
>
> **未审计** —— 该服务已有独立 cmd（`cmd/moderation` / `cmd/artifact`，未接入 oauth 主进程），本轮字段审计未覆盖，状态 ⏳。

## 统计

- 本服务 POST 端点：**1**

---

| 路径 | 鉴权 | Handler | 状态 | 备注 |
|---|---|---|---|---|
| `POST /api/v1/moderation/jobs/:id/review` | 🛡️ | `moderationH.ManualReview` | ⏳ | 人工复核某审核任务 |
