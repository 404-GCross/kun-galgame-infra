# Moderation 服务 — POST API 清单

> 服务: **moderation**（`apps/api/cmd/moderation`） · Base URL: `/api/v1` · 路由源: `cmd/moderation/main.go`
>
> 图例见 [README](./README.md)。配套: [moderation.get.md](./moderation.get.md)
>
> **未审计** —— 该服务尚未实现（仅占位），本轮按要求跳过，状态保持 ⏳。

## 统计

- 本服务 POST 端点：**1**

---

| 路径 | 鉴权 | Handler | 状态 | 备注 |
|---|---|---|---|---|
| `POST /api/v1/moderation/jobs/:id/review` | 🛡️ | `moderationH.ManualReview` | ⏳ | 人工复核某审核任务 |
