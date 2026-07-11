# Moderation 服务 — POST API 清单

> **更正（2026-06）**：moderation 骨架已删除（doc 18 P0，T&S 平台改由 cmd/trust 承载）；artifact **有独立服务与路由**（`cmd/artifact`）；下文「未实现」为旧表述。


> 服务: **moderation**（骨架已删除，doc 18 P0） · Base URL: `/api/v1`
>
> 图例见 [README](./README.md)。配套: [moderation.get.md](./moderation.get.md)
>
> **未审计** —— artifact 有独立 cmd（`cmd/artifact`，未接入 oauth 主进程），本轮字段审计未覆盖，状态 待审计；moderation 骨架已删除（doc 18 P0）。

## 统计

- 本服务 POST 端点：**1**

---

| 路径 | 鉴权 | Handler | 状态 | 备注 |
|---|---|---|---|---|
| `POST /api/v1/moderation/jobs/:id/review` | admin/mod | `moderationH.ManualReview` | 待审计 | 人工复核某审核任务 |
