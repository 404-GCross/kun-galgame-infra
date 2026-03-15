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

### 代码风格

- 前端所有函数使用箭头函数编写，不使用 `function` 关键字声明
