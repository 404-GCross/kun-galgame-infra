# galgame 读面 · 过渡面参考

> **⚠️ 这是一个过渡面（transitional face），不是长期契约。**
>
> galgame 富读面现由 **catalog 服务的 internal tier 读面**（`/internal/*`，devapi `nm_` key）承载。
> 它是**开放 API Phase 2 路线 B** 的过渡桥:
> **终态 = `/v1` 富化(05-open-api step 07)后,下游读迁 `/v1` 同形契约、本 internal 富面退役。**
> (路线 B,2026-07-21 用户裁定。)
>
> 因此**不要**为这个面投入长期集成/文档工程——只把它当作"迁到 `/v1` 之前的临时读源"。
> 字段级形状的真值在**代码**与**机器可读 OpenAPI spec**里(见下「路由集指针」),本文只给指针与承诺。

本目录曾是「Galgame Wiki API」的整套人读契约手册。**wiki 前端 / `wiki.kungal.com` 域 / 独立 galgame 服务 / legacy `/api` 读面 / Basic-auth feeds / `*_WIKI_BASE_URL` env 名**均已在**开放 API Phase 2 · W5(2026-07-21)**退役,故整套手册已失真,收容为本页过渡参考。

---

## 面与基址

| 项 | 值 |
|---|---|
| 服务 | catalog(container `catalog`,端口 `9281`) |
| 基址 env | `KUN_NEXTMOE_API_BASE`(prod `http://catalog:9281`,dev 默认 `http://127.0.0.1:19281`) |
| 富读面前缀 | `/internal`(客户端在基址之后拼 `/internal/...`) |
| S2S feeds | `GET /internal/galgame/messages/feed`、`GET /internal/galgame/revisions/recent`(W5 由 legacy `/api` + Basic 迁入本面) |

> legacy `/api` 前缀的 galgame **读**路由已在 W5 退役(A2);`/api` 现只承载**写 / 投稿 / staff / 图片上传**面(06 波领地,永久 S2S,不进本参考)。

## 鉴权(硬依赖 key,无回退)

- 头:`X-API-Key: nm_...`
- key 需 **internal tier** + scope **`galgame:read`**;流量计量于 `galgame_internal`。
- **无** OAuth-client Basic、**无** 匿名读:key 即身份。无效 / 缺失 key → `401`;tier 不足 → `403`。

> **回退阀已死。** 配了 `KUN_NEXTMOE_API_BASE` 却把 `KUN_NEXTMOE_API_KEY` 留空 = **启动 fail-fast**(下游 forum/patch/letmoe 均已改为硬依赖),**不是**静默回落到旧的无鉴权 legacy `/api`。旧 env 名 `GALGAME_WIKI_BASE_URL` / `KUN_GALGAME_WIKI_BASE_URL` / `KUN_WIKI_API_BASE` 已全部退役。

## 响应格式与字节兼容承诺

统一信封,分页 `data` 为 `{ items, total }`:

```json
{ "code": 0, "message": "成功", "data": { "items": [], "total": 0 } }
```

**字节兼容承诺**:`/internal/*` 的读响应与 W5 退役前的 legacy `/api` **逐字节一致**(同一批 handler、同一信封)——下游只需切基址(`/api`→`/internal`)+ 换鉴权(Basic→`X-API-Key`),既有解析代码无需改。W5 迁移前已用双态字节回放(`jq -S` 0 差异)证明。

## 路由集指针(真值在代码 + spec,本文不重复形状)

读面 = **44 条 galgame 读路由 + 2 条 S2S feed**,全部挂在 `/internal/galgame/...`。字段级形状请取:

- **机器可读 OpenAPI**(门户发布,code-first 从 Huma 导出,永不漂移):
  - 读 API:`https://docs-kungal.nextmoe.dev/specs/galgame-wiki.openapi.yaml`(列表 / 详情 / batch / 搜索 / 用户主页 / 关系 / check / 活动流 / mine / 消息 / 修订与 PR)
  - 发售月历:`https://docs-kungal.nextmoe.dev/specs/galgame-wiki-calendar.openapi.yaml`
  - 生成客户端:`pnpm dlx openapi-typescript@7 <spec-url> -o galgame.ts`
- **代码**(单一真源):infra `apps/api/internal/galgameapp/`(`readroutes.go` = 44 读的注册序;`devapiface.go` = internal 面 devapi 链 + feeds 挂载)。

> 注:spec 路径以 `/galgame/...` 描述端点**形状**;实际调用时前缀为 internal 面的 `/internal/galgame/...`。两个面注册的是同一套 `reads.register`,形状相同、仅前缀 + 鉴权不同。

## 终态(路线 B)

`/v1` 富化(05-open-api step 07)落地后,下游读迁 `/v1` 的**同形**公开契约,本 internal 富面随之退役,平台展示的 API = 全部真实 `/v1` API。在那之前,本面是下游 galgame 读的唯一来源。
