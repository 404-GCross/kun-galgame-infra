# 10 — OpenAPI 与生成式客户端(Huma code-first)

> 本篇定下 artifact(及后续 moderation)**对外契约的机器可读形态**:用 **Huma 叠在现有 Fiber v3 上做 code-first**,从 Go 类型自动导出 **OpenAPI 3.1**,再据此**生成** Flutter(Dart)客户端与跨仓 Go 客户端。背景:即将开发 **Flutter + Riverpod** app,而 Flutter 需要类型化 Dart 模型 + Dio client —— 这是 Markdown 给不了、OpenAPI 才能给的(codegen)。

## 0. 决策摘要

- **artifact + moderation = OpenAPI-native(code-first)**;spec 从 Go 类型导出,**永不漂移**。
- 工具链:**Huma**(router-agnostic,有 **Fiber v3** 适配器)叠在现有 `internal/app` Fiber 构建器上 —— **不换框架**。
- 产物:`/openapi.json`(OAS 3.1)→ 生成 **Dart**(dart-dio / retrofit,包进 Riverpod)+ **Go**(oapi-codegen,替代手写的跨仓 client)。
- prose 契约(本 `docs/artifact/` 全套)与 OAS **并存**:OAS 管类型/codegen/校验,prose 管理由/BREAKING/口径。两者一起登记进 `../kungal-docs`。
- **范围纪律**:只让 artifact + moderation OpenAPI-native;**不**去 spec moyu 那批马上迁走的 upload/admin 端点;老 Fiber 全量 retrofit 当慢节奏可选后续。

## 1. 一个必须纠正的前提:artifact 不是 greenfield

"新服务 code-first 零成本"的论断**对 moderation 成立**(真 greenfield),**对 artifact 不成立** —— artifact 的 Phase 1 已用 **Fiber v3 写完、提交、推送**(见 [05](./05-engineering-plan.md)、[07](./07-ops-and-config-status.md))。

**但救命的一点:artifact 目前零下游接入**(见 README"跨仓契约说明"),而本次正要给它接第一批消费方([08](./08-migration-forum-moyu.md))。所以 **code-first 的窗口仍开着,且这是它最便宜的时刻 —— 一旦 forum/moyu 集成上去窗口就关。** 结论:**现在改,作为本次迁移的 P0,在写集成客户端之前改。**

## 2. 方案:Huma 叠在 Fiber v3 上

不是换框架,是给**一个服务的 6 个端点**加适配器:

```go
// humafiber 适配器 import 的是 github.com/gofiber/fiber/v3(已核实)
api := humafiber.New(application.Fiber, huma.DefaultConfig("Artifact", "1.0.0"))
// 或挂在现有带 ClientAuth 的组上:
api := humafiber.NewWithGroup(application.Fiber, grp, cfg)

huma.Register(api, huma.Operation{
    OperationID: "initUpload",
    Method:      http.MethodPost, Path: "/api/v1/artifacts",
}, func(ctx context.Context, in *InitUploadInput) (*InitUploadOutput, error) { … })
```

- **保留共享的 `internal/app` Fiber 构建器 + 中间件**(`ClientAuth` / CORS / health / logger)全不动。
- handler 从 `func(fiber.Ctx) error` 改写成 Huma 的 `func(ctx, *Input)(*Output, error)`;**改造面只在 handler + dto + main.go 接线**。
- **service / storage / quota / GC 等重逻辑(含已修的 F12 multipart-abort)完全不碰。**
- **OAS 3.1 从 Go 类型自动导出**(`/openapi.json`),与代码同源 → 不可能漂移(等价于 `docs:verify`,但在机器层免费)。
- 校验、路径/查询/body 绑定、错误码映射从**手写变声明式**(struct tag)→ handler 代码反而更短。

### 为什么 Huma 而不是 Fuego
Fuego 是它自己的框架(net-http/Gin/Echo,**不支持 Fiber**),用它 artifact 会成为全 infra 唯一不走共享 Fiber 构建器的异类;Huma 是 router-agnostic 且有真正的 **Fiber v3** 适配器 → 全部保留。moderation 既是 greenfield 也用 Huma-on-Fiber,使全 infra 服务共享同一套 app 构建器。

## 3. 唯一要诚实说的成本:house 信封

全生态用自定义响应信封 `{code, message, …}`(整数 `code`,见 [03 错误响应](./03-api-design.md));Huma 默认走 RFC7807。故 **artifact-on-Huma 必须配自定义 transformer / error formatter,让它继续吐 house 信封**,否则破坏与 03 + 其它服务的线缆兼容。

- 实现:`huma.Config` 自定义 `CreateHooks` / 响应序列化 + 自定义错误模型,把 50001–50017 这套整数码映射进 house 信封。
- 这是**唯一一处麻烦**;其余净收缩。把 house 信封定义成响应 schema,Huma 会照样把它写进 spec。

## 4. 生成式客户端

### 4.1 Go(立刻兑现,与 Flutter 无关)
moyu/kungal 现在**手写**各 infra 服务的薄 Go client(`userclient`/`imageclient`/`galgameClient`)。artifact(及 moderation)的跨仓 client **改为从 spec 用 `oapi-codegen` 生成**,不再手写、不再各自漂移。**这直接降 [08] 迁移的风险** —— §3.2 里"在两站后端各写一个 artifact S2S client"那步变成"生成"。

### 4.2 Dart / Flutter
- 在 Flutter 仓 CI 用 `openapi-generator`(**dart-dio**,稳健)或 `openapi_retrofit_generator`(retrofit + dio + json_serializable,更地道)从 spec 生成**类型化模型 + Dio client**。
- **Riverpod 接线**:一个 Provider 提供 Dio client(拦截器注入 OAuth bearer / session)→ 每个生成的 API 组一个 provider → `@riverpod` AsyncNotifier/FutureProvider 发起调用。生成的 client 保持"哑",Riverpod 管状态/缓存/鉴权。

## 5. prose 与 spec 并存

- **OAS**:类型、codegen、请求/响应校验。
- **prose(本 docs/ 全套)**:背景、决策、BREAKING 史、口径与边界(如 `content_limit` 口径、status 语义、key 方案、配额双轴)。
- 两者都**登记进 `../kungal-docs`** 的 ownership 并 `docs:sync`(artifact 接入 forum/moyu 后,这是它"从纯本仓设计文档"升级为 Tier-A 跨仓契约的时点)。OAS 是 prose 的机器孪生,不是替代。

## 6. 防漂移(CI)

- code-first 下 spec 从代码导出,**不会与代码漂移**;仍在 CI 加 **`oasdiff`**,在合并前**标出 BREAKING 变更**(改名/删字段)。
- 对将来才 spec 的**老 Fiber 端点**(本次范围外、属 spec-first):用契约测试(kin-openapi 校验真实响应 / Schemathesis 跑活栈)兜住,等价于 `docs:verify` 之于线缆。

## 7. 落地顺序(P0,artifact 仍零消费 = 安全)

1. **artifact code-first 改造**(Huma-on-Fiber + house 信封 transformer)→ 导出 OAS 3.1 → 登记进 `../kungal-docs`(prose + OAS)。
2. **生成** moyu/kungal 的 artifact Go client(oapi-codegen)。
3. **[08] P1 集成** 用生成的 client(而非手写)。
4. **moderation** 出生即 Huma-native(同套做法)。
5. **规则确立**:今后**每个新 infra 契约都随出一份 OAS**(与 prose 并列);老 Fiber retrofit 慢节奏可选。

## 8. 注意 / 待确认

- house 信封 transformer 是改造的关键风险点,先用一个端点打通验证再铺开。
- Huma 的 input struct 同时承载 header(如 `Authorization` / `X-Kun-Artifact-Client-Id`)、path、query、body —— `ClientAuth` 中间件仍在 Fiber 层跑,Huma 端点从 `c.Locals` 取已认证的 client/site(或改成 Huma resolver)。落地时统一其中一种取法。
- 大文件**字节不经服务**(直传 B2),故 artifact 的 OAS 只描述 init/complete/download/get/list/delete 这些小 JSON,**不涉及流式 body** —— Huma 完全适配,无大小/流式顾虑。

---

← 返回 [README 索引](./README.md) · 迁移全局见 [08](./08-migration-forum-moyu.md) · API 契约见 [03](./03-api-design.md)
