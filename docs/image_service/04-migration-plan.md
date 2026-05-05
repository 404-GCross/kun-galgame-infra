# 04 — 旧系统迁移计划

## 旧路径清单

| 站点 | 旧路径 | 类型 | 迁移策略 |
|------|--------|------|---------|
| kungal | `topic/user_${uid}/${userName}-${unixMS}.webp` | 内容型，markdown 硬编码 | **老桶只读永久保留，不迁移** |
| kungal | `avatar/user_${uid}/avatar.webp` | 实体型，DB 可查 | 迁移（走 DB 字段） |
| kungal | `avatar/user_${uid}/avatar-100.webp` | 派生图 | 丢弃（新服务预生成 `_100` / `_256`） |
| moyu | `topic/user_${uid}/${userName}-${unixMS}.webp` | 同 kungal | 老桶只读永久保留 |
| moyu | `avatar/user_${uid}/avatar.webp` / `avatar.avif` | 实体型，DB 可查 | 主图迁移 |
| moyu | `avatar/user_${uid}/avatar-mini.webp` / `avatar-mini.avif` | 派生图 | **丢弃**（新服务以 `_256` / `_100` 变体重新生成；注意命名不同） |
| galgame wiki | `galgame/${gid}/banner/banner.webp` | 实体型，DB 可查 | 迁移 |
| galgame wiki | `galgame/${gid}/banner/banner-mini.webp` | 派生图 | 丢弃（新服务以 `_mini` 变体重新生成） |

> **变体命名差异注意**：三站历史命名各异（`avatar-100` / `avatar-mini` / `banner-mini`），新服务统一为 `_<variant>` 后缀（如 `_100` / `_256` / `_mini`）。**所有历史派生图一律丢弃**，不尝试映射文件名；新服务按 preset 重新生成。

## 迁移原则

1. **新旧 URL 共存**：旧 URL 保持可访问，直到各站调用方代码切换完成
2. **不物理删除旧对象**：至少保留 6 个月，用于回滚和审计；topic markdown 那部分永久保留
3. **增量可中断**：调用方脚本用 `WHERE *_image_hash IS NULL` 天然幂等，可中断重跑
4. **调用方代码切换各自独立**：kungal、moyu、galgame wiki 各自节奏，每站 3–4 个 PR 起步，1–2 个月落地
5. **派生图全部丢弃**：`avatar-100.webp`、`banner-mini.webp` 不迁移，由新服务预生成

## topic 图床的特殊处理

这是最省心的路径，单独拎出来说清：

- **用户发的 markdown 里有几十万条 `![](https://image.kungal.com/topic/user_X/xxx-1775xxx.webp)` 形式的硬编码链接**
- 这些 markdown 内容不变，URL 永远指向老 bucket
- 批量改库里的 markdown 文本是高风险、低价值的操作
- CDN rewrite 是一个需要永久维护的外部依赖

**最终方案**：

> 🔒 **topic 图床的老 URL 永久保留只读，不迁移、不 rewrite。新上传全部走新服务（preset=topic），新老数据自然分野。**

理由：
- R2 / S3 上几十 GB 的历史数据成本月度几块钱，不值得花工程时间折腾
- 不增加任何永久维护负担
- 新老 URL 用户不会同时看到（旧帖子看老 URL，新帖子看新 URL），无体验撕裂

## 阶段划分

### 阶段 0：新服务上线（V1 完成后）

- 图片服务独立运行于 `:9278`
- OAuth Client 为 kungal/moyu/galgame wiki 开通 `image:upload` scope + 对应 preset
- 各站点在**新功能**上先用新服务（新开模块、新注册用户的 avatar），旧数据不动
- 目标：验证新服务稳定性，收集真实流量数据

### 阶段 1：双写兼容期（1–2 周，按站点节奏）

- 旧代码保持不变，**旧 bucket 继续接收 topic 图床的上传**（直到该站 topic 切换，见阶段 4）
- avatar / banner 的新上传路径切到新服务
- 关键：确保新上传的图的 `hash` 被**同步写入调用方业务库**的新字段

例如 kungal 用户改头像：

```
// 旧代码（保留一段时间作为回退）
uploadToOldBucket(file) → 写 users.avatar_url_legacy

// 新代码
uploadToImageService(file) → 写 users.avatar_image_hash
```

前端读取优先 `avatar_image_hash`，缺失时回退到 `avatar_url_legacy`。

### 阶段 2：调用方侧迁移（唯一路径）

> 🔧 **平台侧不提供迁移脚本**。原因：迁移的本质是"翻转业务库的外键"，而**只有调用方知道哪个老 URL 属于哪个 user/galgame**。平台再多扫桶 / 写 `images` 表也替代不了调用方那条 `UPDATE users SET avatar_image_hash = ? WHERE id = ?`。让调用方直接走标准 `POST /image/upload` API 一步到位最简单。
>
> image_service 的 sha256 内容寻址 + 跨站去重已经保证：第二次以同样 bytes 调 `Upload` 是 dedup-hit（不重处理、不重存）。所以三站独立各跑一遍互不影响。

每个调用方在自己的仓库内编写一次性迁移脚本。

#### 业务库 schema 准备

在 `users` / `galgame` 等表上**同时**加 hash 外键 + 迁移状态列，避免死链反复重试：

```sql
ALTER TABLE "user"
    ADD COLUMN avatar_image_hash CHAR(64),
    ADD COLUMN avatar_migration_status   SMALLINT NOT NULL DEFAULT 0,  -- 0=未尝试 1=成功 2=永久失败
    ADD COLUMN avatar_migration_attempts SMALLINT NOT NULL DEFAULT 0;

ALTER TABLE "user" RENAME COLUMN avatar TO avatar_url_legacy;

CREATE INDEX idx_user_pending_migration
    ON "user"(id)
    WHERE avatar_image_hash IS NULL
      AND avatar_url_legacy IS NOT NULL
      AND avatar_migration_status != 2;
```

galgame wiki 同形：`banner_image_hash` + `banner_migration_status` + `banner_migration_attempts`。

#### 脚本骨架（以 kungal `users.avatar` 为例）

```go
// kungal/apps/api/cmd/migrate-avatars-to-image-service/main.go

const maxAttempts = 3

func main() {
    cfg := mustLoadConfig()
    db := mustConnectDB(cfg)
    cli := imageclient.New(imageclient.Config{
        BaseURL:      cfg.ImageServiceBaseURL,
        ClientID:     cfg.ImageOAuthClientID,
        ClientSecret: cfg.ImageOAuthClientSecret,
    })
    ctx := context.Background()
    start := time.Now()

    var (
        processed, succeeded, failed int64
        migratedHashes               []string
    )

    rows := db.Query(`
        SELECT id, avatar_url_legacy, avatar_migration_attempts
        FROM "user"
        WHERE avatar_image_hash IS NULL
          AND avatar_url_legacy IS NOT NULL
          AND avatar_migration_status != 2
        ORDER BY id ASC
    `)

    for rows.Next() {
        var uid int64
        var legacy string
        var attempts int16
        rows.Scan(&uid, &legacy, &attempts)
        processed++

        body, err := fetchOldObject(legacy)
        if err != nil {
            failed++
            recordFailure(db, uid, attempts, err)
            slog.Warn("fetch old", "uid", uid, "url", legacy, "err", err)
            goto progress
        }

        result, err := cli.Upload(ctx, bytes.NewReader(body), "avatar.bin", "avatar")
        if err != nil {
            failed++
            recordFailure(db, uid, attempts, err)
            // 配额耗尽时退出整个脚本（提示运维 raise 配额或换天再跑）
            if errors.Is(err, imageclient.ErrQuotaExceeded) {
                slog.Error("quota exceeded; stopping", "processed", processed)
                break
            }
            slog.Warn("upload", "uid", uid, "err", err)
            goto progress
        }

        _, err = db.Exec(`
            UPDATE "user"
               SET avatar_image_hash = ?,
                   avatar_migration_status = 1
             WHERE id = ?
        `, result.Hash, uid)
        if err != nil {
            slog.Error("update db", "uid", uid, "err", err)
            failed++
            goto progress
        }
        succeeded++
        migratedHashes = append(migratedHashes, result.Hash)
        slog.Debug("migrated", "uid", uid, "hash", result.Hash, "dedup", result.Deduplicated)

    progress:
        // 每 1000 行打一次 summary（可观测性）
        if processed%1000 == 0 {
            elapsed := time.Since(start)
            rate := float64(processed) / elapsed.Seconds()
            slog.Info("progress",
                "processed", processed,
                "succeeded", succeeded,
                "failed", failed,
                "rate_per_sec", fmt.Sprintf("%.1f", rate),
                "elapsed", elapsed.Truncate(time.Second),
            )
        }
    }

    slog.Info("migration finished",
        "processed", processed, "succeeded", succeeded, "failed", failed,
        "elapsed", time.Since(start).Truncate(time.Second),
    )

    // 收尾：把刚迁的 hash 主动 ping 一次。否则首次 ref-ping cron
    // 跑到之前，新写入的 last_referenced_at 就是 NOW()，没问题；
    // 但显式 ping 一次让"迁移日 = 首次 ping 日"对齐，便于事后追账。
    if len(migratedHashes) > 0 {
        for _, batch := range chunk(migratedHashes, 1000) {
            if _, err := cli.ReferencePing(ctx, batch); err != nil {
                slog.Warn("final ping", "err", err)
            }
        }
    }
}

// recordFailure 累加 attempts；超过 maxAttempts 标记为永久失败。
func recordFailure(db *sql.DB, uid int64, attempts int16, cause error) {
    newAttempts := attempts + 1
    status := 0
    if int(newAttempts) >= maxAttempts {
        status = 2 // 永久失败 — 后续 SELECT 用 status != 2 跳过
    }
    db.Exec(`
        UPDATE "user"
           SET avatar_migration_attempts = ?,
               avatar_migration_status = CASE WHEN ? = 2 THEN 2 ELSE avatar_migration_status END
         WHERE id = ?
    `, newAttempts, status, uid)

    // 详细 error 写到独立 log 表，方便事后人工核查。
    db.Exec(`
        INSERT INTO image_migration_log(entity_id, entity_type, error_msg, attempted_at)
        VALUES (?, 'user.avatar', ?, NOW())
    `, uid, cause.Error())
}
```

**骨架特性**：

- ✅ **死链跳过**：`avatar_migration_status != 2` 把重复失败 ≥ 3 次的行排除；不会无限重试
- ✅ **断点续跑**：`avatar_image_hash IS NULL` + 状态过滤是天然幂等谓词
- ✅ **配额超限自动退出**：`errors.Is(err, imageclient.ErrQuotaExceeded)` 命中就 break，避免在 429 上空转 1 万次
- ✅ **进度可观测**：每 1000 行 INFO 一行，含速率和 elapsed
- ✅ **收尾自动 ping**：迁移结束前把所有刚成功的 hash 显式 `ReferencePing` 一次
- ✅ **不删 `avatar_url_legacy`**：保留作前端回退兜底（阶段 3）；半年观察期后再决定是否清字段
- ✅ **失败明细表**：`image_migration_log` 留事后排查（必要时人工 fix 后清 status 让脚本重试）

**topic 图床的特殊处理**：调用方脚本**不处理** topic（老 markdown URL 永久指向老桶只读，见上一节）。

**预估耗时（kungal avatar 示例）**：

- 假设 ~10 万头像，平均每张 80KB → 8GB 总流量
- 受 image_service 流水线吞吐限制（含 decode / resize / encode WebP）：约 50–200 张/秒
- 实际跑完：30 分钟 – 1 小时；可分段、可中断

### 阶段 3：avatar URL 兼容层（可选，2–4 周）

**目的**：阶段 2 之后，业务库里 `users.avatar_image_hash` 已经有值，但可能还有：
- 浏览器缓存里的老 URL
- 第三方外链引用老 URL（很少）
- 部分未更新的前端代码

**方案（推荐最简单的）**：业务库保留 `avatar_url_legacy` 字段，前端 URL 解析函数：

```ts
function resolveAvatarUrl(user) {
  if (user.avatar_image_hash) return imageMainUrl(user.avatar_image_hash)
  if (user.avatar_url_legacy) return user.avatar_url_legacy
  return DEFAULT_AVATAR
}
```

前端代码全部切换完成后，可以删 `avatar_url_legacy` 字段（或永久保留也无妨，字段本身不占钱）。

> **不建议**在 CDN / Nginx 层写 rewrite 规则把 `/avatar/user_123/avatar.webp` 映射到新 URL——因为要永久维护一个"查业务库 → 拼 hash URL"的外部服务，复杂度远超收益。直接靠业务库字段回退就够。

### 阶段 4：业务代码切换（各站独立 1–2 个月）

这是最费工程时间的阶段，每站点 3–4 个 PR 起步。以 kungal 为例：

| PR | 工作 | 耗时 |
|----|------|------|
| PR-1 | 业务库 migration：加 `users.avatar_image_hash` / `topic.images_hash_jsonb` 字段 + GORM model | 半天 |
| PR-2 | avatar 上传逻辑改调图片服务；上传成功同步写 `avatar_image_hash` | 1–2 天 |
| PR-3 | topic 图床上传逻辑改调图片服务（`preset=topic`） | 1–2 天 |
| PR-4 | 读取逻辑全面切换（前端组件 / API response / CDN URL 构造函数） | 2–3 天 |
| PR-5 | 删 `avatar_url_legacy` 字段 / 清理回退代码（上线 N 周后） | 半天 |

moyu、galgame wiki 结构类似。三站合计 9–15 个 PR，跨 1–2 个月。

#### 阶段 4.x 验收（每站独立）

- [ ] 监控显示旧 bucket 的**新上传** QPS 归零（topic 也切了）
- [ ] 监控显示 `users.avatar_image_hash` 覆盖率 > 99%
- [ ] 监控显示旧 URL 访问量 < 1%（除 topic 历史 URL 外）

### 阶段 5：旧对象生命周期管理（≥ 12 个月后）

#### 老 avatar / banner 桶的保留期承诺

调用方接入完成（PR-2 / PR-3 上线之日）起，**老 avatar / banner 桶的公开可读权限**：

- **最低保留 12 个月**（不是 6 个月——给出现疑难杂症 / 边缘 case 的浏览器和外链充分缓冲）
- **下线触发条件（必须同时满足）**：
  1. 老 URL 访问日志显示 **连续 30 天 < 1%** 的总流量比例
  2. 调用方业务库 `*_image_hash` 非空覆盖率 > 99.5%
  3. 平台 + 调用方任一方都没有 veto
- **下线前 30 天预告**：调用方在内部公告 + 邮件 / Slack 通知任何已知第三方依赖；30 天后无 veto 才执行

#### 老 topic 桶

- **永久保留只读，不进入下线议程**。R2/S3 几十 GB 几块钱/月，不值得为这点钱让历史 markdown 帖全部裂图

#### 桶的物理对象不删

即便桶下线（停止公开访问），对象本身建议**继续保留**至少再 12 个月以备审计 / 回滚。最便宜的方式是转 IA / Glacier。这是调用方的决定，平台不强制。

## 特殊情况处理

### galgame banner 的原图问题

galgame wiki 之前没有保留高清原图，只存了压缩版（与新服务的 `webp@82 fit 1920×1080` 大同小异）：

- 迁移后 `images.width/height` 用实际旧图尺寸
- `is_original = false`（实际上 V1 根本没这个字段，这里留作对比说明）
- 未来需要高清原图时，再增加新的 preset + 重新从 VNDB 采集

### 用户改名导致的 topic 图路径混淆

kungal 旧路径 `topic/user_123/alice-1700000000.webp`：`alice` 是上传当时的用户名。

由于 **topic 整体不迁移**，这个问题自然不存在。

### 重复内容的 hash 碰撞

迁移过程中会发现大量重复（同一张头像被不同用户传过）：

- `images` 以 `UNIQUE(hash)` 单行存在
- 迁移脚本遇到已有 hash：只 INSERT 一行 `image_site_usage`（或 UPDATE upload_count）+ 更新业务库外键
- 对象存储天然只有一份

## 回滚策略

迁移中任何阶段出错，回滚路径：

| 阶段 | 回滚动作 |
|------|---------|
| 1（双写期） | 停掉新代码的写入，旧代码自持续工作 |
| 2（批量迁移） | 调用方脚本失败行不影响已成功行（`avatar_image_hash IS NULL` 仍代表未迁，再跑就续上）；整体出错则停脚本观察 |
| 3（URL 回退） | 撤下前端读取的 "优先新 URL" 逻辑，切回 `avatar_url_legacy`；旧桶从未删过，直接可用 |
| 4（代码切换） | 调用方有 fallback 逻辑，撤回切换不丢数据 |

## 调用方 cron 清单（每站必备）

接入后，每个调用方**必须**在自己的后端部署一个每日 cron，否则 60 天后图片会被转冷存储。

### 需要实现的内容

```go
// 例：apps/api/internal/infrastructure/cron/image_ping.go

// 每日凌晨 3 点触发
c.AddFunc("0 3 * * *", func() {
    ctx := context.Background()

    // 1. 从业务库聚合所有 *_image_hash 非空字段
    hashes, err := collectAllReferencedHashes(db)
    if err != nil {
        slog.Error("collect hashes failed", "err", err)
        return
    }

    // 2. 按 1000 一批发到 image_service
    for _, batch := range chunkBy(hashes, 1000) {
        resp, err := imageClient.ReferencePing(ctx, batch)
        if err != nil {
            slog.Error("reference ping failed", "err", err)
            continue
        }

        // 3. not_found 的可以清自己的外键（可选，防止挂空引用）
        for _, h := range resp.NotFound {
            slog.Warn("image not found, clearing local ref", "hash", h)
            clearLocalRefsForHash(db, h)
        }
    }
})
```

### SQL 聚合模板（各调用方自行填字段）

```sql
-- kungal / moyu
SELECT DISTINCT avatar_image_hash FROM "user" WHERE avatar_image_hash IS NOT NULL
UNION
SELECT DISTINCT hash FROM unnest_topic_images_hashes() WHERE hash IS NOT NULL

-- galgame wiki
SELECT DISTINCT banner_image_hash FROM galgame WHERE banner_image_hash IS NOT NULL
UNION
SELECT DISTINCT cover_image_hash FROM galgame WHERE cover_image_hash IS NOT NULL
```

### 验收标准

- [ ] 每日 cron 上线后跑满 3 天无失败
- [ ] image_service 侧观察到目标站点的 `POST /image/reference-ping` 每日一次、hash 数合理
- [ ] `not_found` 返回数长期趋近 0（偶尔有是正常的，持续高说明本地库有挂空引用）

## 风险检查清单

- [ ] 旧 bucket 中 avatar / banner 总对象数统计完毕（用于进度条）
- [ ] 调用方业务库的 `*_image_hash` / `*_url_legacy` 字段 migration 已上线
- [ ] 图片服务能承接真实流量（V1 验收通过）
- [ ] `image_site_usage` 写入幂等（`ON CONFLICT DO UPDATE`）
- [ ] topic 图床的老桶公开可读配置不变（别意外改成私有）
- [ ] 回滚演练至少走过一次（至少在 dev 环境）

下一篇：[05 — 工程计划](./05-engineering-plan.md)
