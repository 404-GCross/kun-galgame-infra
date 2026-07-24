# apps/web 管理控制台轨 — pi 值守文件

**使命**:拥有并重构 `apps/web`(NextMoe 平台统一管理控制台,Nuxt4 + @kungal/ui 2.3 + TW4)。三驱动:后端已有功能未接线/前端过时/存量问题修复。
**Claude memory 参考(只读)**:`web-admin-console-track.md`(含 API 层架构:house envelope/dual-base/三 proxy relay/single-flight refresh;dev 端口 :9420)。
**底稿**:无 gitignored 底稿——**本文件即状态源**,流水在 git(建议 commit 前缀 `feat(web)`/`fix(web)`)。

## 边界(勿碰)

- KunUI 本体绝不改(bug 报用户);无渐变背景;只用项目色板;全箭头函数。
- refs/proj(数据聚合)、internal/platform/editing、refs/qa 是别的轨。
- 改 `components/kun/` 下组件先问用户。

## 交接快照(2026-07-23)

- 批次 1+2(bug/安全/error-surfacing/jobs 页/user-detail drawer)**已全推上产**。
- **待验清单(优先做,零新代码)**:①OAuth authorize 修复上产后未验(deny/正常登录/prompt=none 三链);②前端视觉从未亲验——跑起来点一遍(KunSelect 观感/网格/关闭动画/**首个 KunDrawer** 用户详情抽屉)。
- **剩余尾波**:Wave2 二特性(admin edit-user / self-avatar-upload);Wave3 尾(KunDatePicker 一致化/SubNav 共享刷新/**类型生成不对称**=oauth/image/users/sites/moemoepoint 仍手写类型是契约漂移风险/EmailChange 重启用/creator-apps 双提交守卫)。

## 纪律要点(本轨特有)

- **KunModal 关闭动画陷阱**:父组件别 `v-if` 挂卸,用 `v-model:open` 保持挂载(样板=RoleModal);否则开有动画关无动画。
- TW4 动态拼接 class 不会被 emit——用 KunChip/静态映射。
- Nuxt 页面单一真实根元素;SSR 日期用 fixed 'zh-CN' locale 防 hydration 漂移。
- 共享 main 上想 hold 的改动必须开独立分支(连带推上产实爆过)。

## pi 值守状态(就地更新,一行一钩子)

- (待 pi 填)
