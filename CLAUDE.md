# Project Guidelines

## 前端规范（apps/web）

### UI 组件

- 所有 UI 组件均位于 `components/kun/` 目录，项目必须使用这些 UI 组件，不要自造组件
- 如果需要修改 `components/kun/` 中的组件，必须先询问用户确认

### 页面与组件拆分

- `pages/` 目录只负责路由定义，每个页面文件只包含 `definePageMeta` 和一个容器组件引用
- 每个页面对应的业务组件放在 `components/` 对应文件夹中，例如：
  - `/users` 页面 → `components/users/`
  - `/auth/login` 页面 → `components/auth/login/`
  - `/sites` 页面 → `components/sites/`
- 组件文件名不要重复目录前缀（Nuxt 自动导入会拼接目录名）：
  - `components/users/Container.vue` → 自动导入为 `UsersContainer`
  - `components/users/Table.vue` → 自动导入为 `UsersTable`
  - ❌ 不要写成 `components/users/UsersContainer.vue`（会变成 `UsersContainer` 但容易混淆）

### 常量与类型

- 所有常量放在 `app/constants/` 目录
- 所有接口类型放在 `shared/types/` 目录（Nuxt 4 自动导入第一层导出）
- `shared/` 目录下的 `types/` 和 `utils/` 会被 Nuxt 自动导入

### 颜色系统

- 使用 `app/styles/tailwindcss.css` 中定义的自定义颜色，不使用 Tailwind 固有颜色（gray、indigo、blue、green、red 等）
- 自定义颜色自动适配浅色/深色模式，不需要 `dark:` 前缀
- 颜色映射：
  - 文字：`text-foreground`（主文字）、`text-default-500`（次要）、`text-default-400`（辅助）、`text-default-300`（弱化）
  - 边框：`border-default-200`
  - 语义色：`primary`（蓝，主操作）、`success`（绿）、`danger`（红）、`warning`（黄/橙）、`default`（灰/紫）、`secondary`（粉）、`info`（青）
  - 每种语义色都有 50-950 色阶，如 `bg-primary-100`、`text-danger-600`

### 代码风格

- 前端所有函数使用箭头函数编写，不使用 `function` 关键字声明

## 跨仓契约文档（Tier A，本仓为唯一源）

`docs/integration/oauth`、`docs/image_service`、`docs/integration/galgame_wiki` 是 OAuth / 图床 / galgame-wiki 三套**跨服务契约的唯一源**。forum / patch 仓里的 `docs/{oauth,image_service,galgame_wiki}` 是 **kungal-docs 的 `pnpm docs:sync` 生成的带 banner 镜像**，**不要去手改下游副本**（下次 sync 会覆盖）。

- **改契约**：只改本仓这些源文件 → 到 `../kungal-docs` 跑 `pnpm docs:sync --write`（下发镜像到 forum/patch）→ `pnpm docs:audit`（`docs:check` 验镜像一致 + `docs:verify` 验源==代码）应 0 error。
- 这些契约的**真值在代码里**（`cmd/oauth`、`cmd/image`、`cmd/galgame` 等 handler）——改了代码就在同 PR 改这里的源文档，`docs:verify` 会抓「文档与代码现实不符」。
- 统一文档门户：`docs-kungal.nextmoe.dev`；完整所有权模型（Tier A/B/C）见 `../kungal-docs/docs/_meta/ownership.md`。

## 数据库 schema 变更 → 必须提醒迁移

**只要本次改动动了数据库 schema（GORM model 加/改字段或表、`cmd/migrate*` 里的 raw SQL/约束/索引），就必须在任务结束时明确告诉用户：是否需要跑迁移、跑哪个命令、对哪个库。** 部署（push → CI → Dokploy 重部署）**不会自动跑迁移**——漏跑会让线上代码读到不存在的列（GORM `SELECT *` 静默读成零值）→ **静默故障**。

- 主库 `kun_galgame_infra`（oauth + 各 site model）→ `go run ./cmd/migrate`（**部署不自动跑**）。
- wiki 库 `kun_galgame_wiki`（galgame model）→ `go run ./cmd/migrate-galgame`（**部署不自动跑**）。
- `cmd/image` / `cmd/artifact` → 服务启动时自带 `AutoMigrate`（随部署自动，无需手动）。
- 生产执行：`infra-tools` 镜像 + 从对应容器 `.Config.Env` dump 出的 env-file（见 prod 运维笔记）。
- 教训：2026-06 `oauth_clients.moemoepoint_awarder` 列没迁移 → 全站 ~29h 发不出萌萌点。
