# 04 — 旧系统迁移计划

## 旧路径清单

| 站点 | 旧路径 | 类型 | 特征 |
|------|--------|------|------|
| kungal | `topic/user_${uid}/${userName}-${unixMS}.webp` | 内容型 | 每张独立，不覆盖，文件名含时间戳 |
| kungal | `avatar/user_${uid}/avatar.webp` | 实体型 | 就地覆盖，固定文件名 |
| kungal | `avatar/user_${uid}/avatar-100.webp` | 派生图 | 头像缩略图，就地覆盖 |
| moyu | `topic/user_${uid}/${userName}-${unixMS}.webp` | 内容型 | 同 kungal |
| moyu | `avatar/user_${uid}/avatar{,-100}.webp` | 实体型 | 同 kungal |
| galgame wiki | `galgame/${gid}/banner/banner.webp` | 实体型 | 就地覆盖 |
| galgame wiki | `galgame/${gid}/banner/banner-mini.webp` | 派生图 | banner 缩略图 |

> 在开始迁移前，需要从各 bucket 导出完整对象清单（`aws s3 ls --recursive`），统计总量、总大小、类型分布。

## 迁移原则

1. **新旧 URL 共存**：旧 URL 保持可访问，直到所有调用方代码切换完成
2. **不物理删除旧对象**：至少保留 6 个月，用于回滚和审计
3. **增量可中断**：迁移脚本用 `migration_progress` 表记录位置，随时续跑
4. **调用方代码切换各自独立**：kungal、moyu、galgame wiki 的前端/后端各自节奏推进
5. **派生图直接丢弃**：`avatar-100.webp`、`banner-mini.webp` 不迁移，由 imgproxy 按需重建

## 阶段划分

### 阶段 0：新服务上线（M1–M3 完成后）

- 图片服务独立运行于 `:9278`
- OAuth Client 为 kungal/moyu/galgame wiki 开通 `image:upload` scope
- 各站点在**新功能**上先用新服务（如新开的模块），旧数据不动
- 目标：验证新服务稳定性，收集真实流量数据

### 阶段 1：双写兼容期（1–2 周）

- 旧代码保持不变，**旧 bucket 继续接收上传**
- 新代码写入新服务
- 关键：确保新上传的图的 `hash` 被**同步写入调用方业务库**的新字段

例如 kungal 用户改头像：

```
// 旧代码（保留）
uploadToOldBucket(file) → 写 users.avatar_url
// 新代码（并行）
uploadToImageService(file) → 写 users.avatar_image_hash
```

前端读取优先 `avatar_image_hash`，缺失时回退到 `avatar_url`。

### 阶段 2：离线批量迁移

迁移脚本 `cmd/migrate-images/` 执行：

```
对每个旧 bucket 的每个对象:
  1. 下载对象到内存
  2. 计算 sha256
  3. 从旧路径解析元信息（site / entity_type / entity_id / variant）
  4. 检查 images 表：hash 是否已存在
     - 若存在：跳过对象复制，仅插入 images 行（deduplicated）
     - 不存在：通过 S3 CopyObject 复制到新路径（不重新下载上传，快）
  5. 插入 images 表（site, hash, storage_key, metadata...）
  6. 更新调用方业务库：
     - kungal/moyu:
       UPDATE users SET avatar_image_hash = ? WHERE id = ?
       UPDATE topic SET images_jsonb = ... WHERE id = ?
     - galgame_wiki:
       UPDATE galgame SET banner_image_hash = ? WHERE id = ?
  7. 记录 migration_progress（site, old_key, new_key, image_id）
```

#### 迁移脚本特性

- **分站点选择**：`go run ./cmd/migrate-images --site=kungal`
- **分类型选择**：`--type=avatar` / `--type=topic` / `--type=banner`
- **dry-run**：`--dry-run` 只扫描不写入，打印统计
- **断点续跑**：读 `migration_progress` 表 `WHERE site=? AND old_key > last_key`
- **速率限制**：`--rps=100`（避免压垮对象存储）
- **并发数**：`--workers=10`

#### 迁移用的临时表

```sql
CREATE TABLE migration_progress (
    id          BIGSERIAL PRIMARY KEY,
    site        VARCHAR(32) NOT NULL,
    old_key     VARCHAR(512) NOT NULL,
    new_key     VARCHAR(512),
    hash        CHAR(64),
    image_id    BIGINT,
    status      VARCHAR(16) NOT NULL,   -- pending / copied / failed / skipped
    error       TEXT,
    migrated_at TIMESTAMPTZ,
    CONSTRAINT migration_progress_uniq UNIQUE (site, old_key)
);

CREATE INDEX idx_migration_status ON migration_progress(site, status);
```

迁移完成后此表可归档。

#### 预估耗时

假设三站共 50 万对象，平均每个 200KB，总量约 100GB：

- 纯 S3 对象复制（服务器端）：约 2k 对象/秒 → 约 4 分钟
- 加上 DB 写入 + 业务库更新：约 500 对象/秒 → 约 17 分钟
- 加上速率限制保护 → 1–2 小时内完成

如果要跨云迁移（下载 + 上传），时间会显著增加，建议走 `rclone` 先把数据搬到新桶，再跑迁移脚本只更新 DB。

### 阶段 3：URL 兼容层

**目的**：旧 URL 短期内依然要能访问（浏览器缓存、外链、邮件等）。

#### 方案 A（推荐）：CDN/Nginx Rewrite

在 CDN 或 Nginx 前置层加 rewrite 规则，把旧路径映射到新路径：

```nginx
# /avatar/user_123/avatar.webp → 查 DB → 302 到 /img/kungal/ab/cd/abcd...ef.webp
location ~ ^/avatar/user_(\d+)/avatar(-100)?\.webp$ {
    set $uid $1;
    set $size $2;  # 空 or "-100"

    # 方案 A1: 直接代理到图片服务，由图片服务查 DB 后 302
    proxy_pass http://image-service/legacy/kungal/avatar/$uid$size;

    # 方案 A2: 在 Nginx 里用 lua + redis 查 hash 做 rewrite（更快但更复杂）
}
```

图片服务提供 `/legacy/:site/:type/:id[/:variant]` 端点，内部查业务库拿 hash，302 到新 URL。

#### 方案 B：DB 冗余字段

在业务库里保留 `avatar_url_legacy` 字段，前端访问时走一个 URL 重写函数：

```ts
function resolveImageUrl(user) {
  if (user.avatar_image_hash) return buildNewURL(user.avatar_image_hash)
  if (user.avatar_url_legacy) return user.avatar_url_legacy
  return DEFAULT_AVATAR
}
```

前端完全切换后，可以删 `avatar_url_legacy` 字段。

**建议混用**：
- 前端新页面用方案 B
- 外部链接（邮件、RSS 等无法改代码的）靠方案 A 兜底

### 阶段 4：业务代码切换

各站点独立推进：

| 阶段 | 工作 | 验收 |
|------|------|------|
| 4.1 | 上传逻辑改调图片服务 | 监控显示旧 bucket 上传 QPS 归零 |
| 4.2 | 读取逻辑改读 `*_image_hash` | 监控显示旧 URL 访问量 < 1% |
| 4.3 | 删除 `avatar_url_legacy` 字段 | DB 审计确认无查询 |

### 阶段 5：旧对象清理（可选，6 个月后）

观察旧 URL 访问日志：

- 访问量持续 0 → 可以考虑移到冷存储
- 想彻底删除：再等 6 个月 + 全站通告

很多生产环境选择永久保留旧路径，反正对象存储便宜。

## 特殊情况处理

### galgame banner 的原图问题

galgame wiki 之前没有保留原图，只存了压缩版。迁移后：

- 原压缩版（1920×1080 webp）直接作为新系统的"主图"
- `is_original = false`
- 未来新上传的 galgame banner 配置 `image_allow_original = true`，保留高清原图
- 历史 galgame 如需补高清原图，需要从 VNDB 或其他源重新采集（这是业务工作，与迁移无关）

### 用户改名导致的 topic 图路径混淆

kungal 旧路径 `topic/user_123/alice-1700000000.webp`：`alice` 是上传当时的用户名，用户后来改名成 `bob` 路径也不变。

迁移脚本解析路径时：
- 不要用文件名里的用户名 `alice` 做任何业务操作
- 用 `user_123` 里的 uid 作为外键
- 新路径完全不再包含用户名，用 hash 代替

### 重复内容的 hash 碰撞

迁移过程中会发现大量重复图（同一张头像被不同用户传过）：

- `images` 表以 `(hash, site)` 为唯一键，同站内不重复
- 跨站去重：不同 site 各存一行（审计分离），但 `storage_key` 可以相同（省对象存储空间）

实现时有两种选择：
- **严格去重**：`storage_key` 相同，对象存储只存一份。存储省但删除要小心（不能删别的 site 还在用的）
- **站内去重**：每 site 独立一份 `storage_key`。简单但浪费

**推荐站内去重**：简单、安全。存储便宜，几 GB 重复不算事。

## 回滚策略

迁移中任何阶段出错，回滚路径：

| 阶段 | 回滚动作 |
|------|---------|
| 1（双写期） | 直接停掉新代码的写入，旧代码自持续工作 |
| 2（批量迁移） | 迁移失败的对象 `migration_progress.status = failed`，不影响已成功的；整体出错可 TRUNCATE 该表重跑 |
| 3（CDN rewrite） | 撤下 rewrite 规则，旧 URL 仍然能直接访问（旧对象一直没删） |
| 4（代码切换） | 调用方有 fallback 逻辑（优先新 URL，回退旧 URL），撤回切换不丢数据 |

## 风险检查清单

- [ ] 旧 bucket 总对象数统计完毕（用于进度条）
- [ ] 调用方业务库的 `*_image_hash` 字段 migration 已上线
- [ ] 图片服务能承接真实流量（经过 M3 阶段的压测）
- [ ] 审核（即使是 noop 占位）已经接通，避免批量迁移跑完后才发现违规图洗白进来
- [ ] imgproxy 和 CDN 的变体 URL 生成已联调（至少 thumbnail/avatar 两个 preset）
- [ ] 回滚演练至少走过一次

下一篇：[05 — 工程计划](./05-engineering-plan.md)
