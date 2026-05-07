# Moyu → Galgame Wiki 数据迁移设计

> 2026-04-13

## 1. 迁移范围

### 迁移的表

| moyu 源表 | wiki 目标表 | 策略 |
|---|---|---|
| `patch` | `galgame` | vndb_id 去重：重复的保留 kungal 数据，仅补 `bid`/`released` |
| `patch_alias` | `galgame_alias` | 直接迁移（patch_id 重映射后） |
| `patch_link` | `galgame_link` | user_id 填 patch 创建者 |
| `patch_tag` (provider='vndb') | `galgame_tag` | 按 name 合并去重，name_en_us 作 galgame_tag_alias |
| `patch_tag_relation` | `galgame_tag_relation` | tag_id + galgame_id 双重重映射 |

### 不迁移的表

| 表 | 原因 |
|---|---|
| `patch_company` / `patch_company_relation` | 数据质量差 |
| `patch_cover` / `patch_screenshot` | 留 moyu 本地，后续废弃 |
| `patch_char` 相关 (4 张表) | 留 moyu 本地，后续废弃 |
| `patch_person` 相关 (3 张表) | 留 moyu 本地，后续废弃 |
| `patch_release` | 可通过 vndb_id 从 VNDB API 获取 |
| `patch_resource` / `patch_comment` | moyu 站点交互数据，不属于 wiki |

## 2. Wiki 表结构变更

### galgame 表新增字段

```sql
ALTER TABLE galgame ADD COLUMN bid INT UNIQUE;           -- Bangumi Subject ID
ALTER TABLE galgame ADD COLUMN released VARCHAR(107) DEFAULT 'unknown';  -- 发行日期
```

两个字段都可选（bid 为 nullable，released 有默认值），不影响现有 kungal 数据。

## 3. Galgame ID 分配与去重

### vndb_id 去重规则

```
对于 moyu 的每个 patch:
1. 如果 patch.vndb_id 已存在于 wiki 的 galgame 表中:
   → 不创建新 galgame
   → 仅补充 bid 和 released（如果 wiki 中为空）
   → 记录 patch.id → 已有 galgame.id 的映射（用于关联表迁移）

2. 如果 patch.vndb_id 不存在于 wiki 中（或为 NULL）:
   → 创建新 galgame，ID 从 kungal max_id + 1 开始
   → 记录 patch.id → 新 galgame.id 的映射
```

### moyu patch_id 重映射

迁移完成后，moyu 库中所有引用 `patch_id` 的表需要做 remap，使其指向 wiki 中的 galgame.id。

**需要 remap 的 moyu 表和列：**

```
patch.id
patch_alias.patch_id
patch_link.patch_id
patch_tag_relation.patch_id
patch_company_relation.patch_id
patch_cover.patch_id
patch_screenshot.patch_id
patch_resource.patch_id
patch_comment.patch_id
patch_char_relation.patch_id
patch_person_relation.patch_id
patch_release.patch_id
user_patch_favorite_relation.patch_id
user_patch_contribute_relation.patch_id
```

## 4. Tag 合并策略

```
对于 moyu 的每个 patch_tag（provider='vndb'）:
1. 在 wiki 的 galgame_tag 中按 name 查找:
   → 找到: 使用已有 tag.id，记录 patch_tag.id → galgame_tag.id 映射
   → 未找到: 创建新 galgame_tag（category 从 patch_tag.category 获取），记录映射

2. 如果 patch_tag.name_en_us 非空且与 name 不同:
   → 在 galgame_tag_alias 中创建别名（如果不重复）

3. patch_tag 的 introduction、count、alias(JSONB) 等字段全部忽略
```

## 5. 字段映射

### patch → galgame

| moyu patch | wiki galgame | 说明 |
|---|---|---|
| id | — | 不直接使用，通过映射分配新 ID |
| vndb_id | vndb_id | 去重主键 |
| bid | bid | **新字段** |
| name_en_us | name_en_us | |
| name_zh_cn | name_zh_cn | |
| name_zh_cn | name_zh_tw | moyu 无 zh_tw，用 zh_cn 填充 |
| name_ja_jp | name_ja_jp | |
| banner | banner | |
| introduction_zh_cn | intro_zh_cn | 字段名不同 |
| introduction_ja_jp | intro_ja_jp | |
| introduction_en_us | intro_en_us | |
| — | intro_zh_tw | 用 introduction_zh_cn 填充 |
| released | released | **新字段** |
| content_limit | content_limit | |
| status | status | |
| view | view | |
| resource_update_time | resource_update_time | |
| user_id | user_id | 已经过 user migration remap |
| — | series_id | NULL |
| — | original_language | 默认 'ja-jp' |
| — | age_limit | 默认 'r18' |

**不迁移的 patch 字段：** `download`、`type`、`language`、`engine`、`platform`（均为聚合/JSONB），`favorite_count`、`contribute_count`、`comment_count`、`resource_count`（各站自维护）。

### patch_link → galgame_link

| moyu | wiki | 说明 |
|---|---|---|
| patch_id | galgame_id | 通过映射转换 |
| name | name | |
| url | link | 字段名不同 |
| — | user_id | 填 patch.user_id（创建者） |

## 6. 执行顺序

```
1. ALTER galgame 表：添加 bid、released 字段
2. 读取 moyu patch 数据
3. 读取 wiki 已有 galgame（建 vndb_id → id 映射）
4. 去重 + 分配新 ID → 构建 patch.id → galgame.id 映射
5. 插入新 galgame（仅 vndb_id 不重复的）
6. 补充已有 galgame 的 bid/released
7. 迁移 patch_alias → galgame_alias
8. 迁移 patch_link → galgame_link
9. 合并 patch_tag → galgame_tag（按 name 去重）
10. 迁移 patch_tag_relation → galgame_tag_relation（双重 ID 重映射）
11. 为新 galgame 创建 revision 1
12. 重置 wiki 序列
13. remap moyu 库的 patch_id（使用 patch.id → galgame.id 映射）
```

## 7. 幂等性与失败恢复

### galgame_migrations 表

这张表是迁移的"账本"，与 `auth.user_migrations` 平行。每行记录"wiki 的某个 galgame 来源于哪个站点的哪个原始 ID"：

```sql
CREATE TABLE galgame_migrations (
  source_db   VARCHAR(20) NOT NULL,   -- 'kungal' or 'moyu'
  source_id   INT          NOT NULL,  -- pre-remap id in source DB
  galgame_id  INT          NOT NULL,  -- wiki.galgame.id
  created_at  TIMESTAMPTZ  DEFAULT NOW(),
  PRIMARY KEY (source_db, source_id),
  INDEX (galgame_id)
);
```

**写入时机**：

- `migrate-galgame-data`：每行 kungal galgame 写一条 `(kungal, source_id=galgame_id)`
- `migrate-moyu-galgame` step 4（vndb_id 合并）：写一条 `(moyu, p.id, 已有 wiki id)`
- `migrate-moyu-galgame` step 5（新建 galgame）：写一条 `(moyu, p.id, 新分配 id)`

**读取场景**：

1. **重跑前的安全检查**：`migrate-moyu-galgame` 启动时查 `WHERE source_db='moyu'`。任何已有记录都触发拒绝重跑（除非加 `--resume-remap`）。这是"无 vndb_id 重插"的根本防御 —— 不让脚本第二次跑到 step 5。
2. **失败恢复**：`--resume-remap` 模式从这张表加载映射，跳过 wiki 写入，只跑 step 13。这是"wiki 已 commit、moyu 没 commit"分裂状态的恢复路径。
3. **未来反查**：avatar / 其他迁移脚本可以查 `WHERE galgame_id=X` 反推此 galgame 的原始 kungal/moyu id（与 user_migrations 用法对称）。

### 失败恢复流程

| 失败位置 | 现象 | 恢复方法 |
|----------|------|----------|
| step 5（wiki 插入失败） | wiki 已部分插入 + galgame_migrations 部分写入 | 修 bug → 备份恢复 wiki → 重跑 |
| step 13（moyu remap 失败） | wiki 已 commit、galgame_migrations 完整、moyu 未改 | 修 bug → `--resume-remap`（不需要恢复任何库） |
| step 13 执行了一半挂了 | **不会发生** —— step 13 整体在事务里，rollback 自动回退 | — |

**Note**：恢复 wiki 备份后想完全重跑，需要先把 `galgame_migrations` 里 `source_db='moyu'` 的行删掉（脚本错误信息里也提示了 SQL）。

## 8. Step 13 — moyu patch_id remap 实现细节

整个 step 13 在**单个 transaction** 内完成，与 `migrate-users` 的 step 7 风格一致：

```
BEGIN
  ALTER TABLE … DISABLE TRIGGER ALL    (patch + 13 个子表)
  CREATE TEMP TABLE _patch_id_map (...) ON COMMIT DROP
  INSERT INTO _patch_id_map VALUES …    (批量灌入映射)
  -- Pass 1: 所有 id 移到 +10_000_000 偏移区，避免 PK 冲突
  UPDATE patch_alias SET patch_id += 10_000_000 FROM _patch_id_map …
  …(13 张子表)
  UPDATE "patch"     SET id        += 10_000_000 FROM _patch_id_map …
  -- Pass 2: 从偏移区映射到最终 new_id
  UPDATE "patch"     SET id        = _patch_id_map.new_id FROM _patch_id_map …
  …(13 张子表)
  SELECT setval('patch_id_seq', MAX(id))
  ALTER TABLE … ENABLE TRIGGER ALL
COMMIT  -- 任一步失败 → ROLLBACK，DISABLE TRIGGER 也随之回滚
```

### 覆盖的 13 个 FK 列

完整对照 prisma/moyu schema 里所有 `references: [id]` 指向 patch 模型的列：

```
patch_alias.patch_id, patch_link.patch_id,
patch_cover.patch_id, patch_screenshot.patch_id,
patch_resource.patch_id, patch_comment.patch_id,
patch_release.patch_id,
patch_tag_relation.patch_id, patch_company_relation.patch_id,
patch_char_relation.patch_id, patch_person_relation.patch_id,
user_patch_favorite_relation.patch_id, user_patch_contribute_relation.patch_id
```

外加 `patch.id` 自身 = 14 列，全部 14 个 UPDATE 在同一事务里。

> `user_patch_comment_like_relation` 和 `user_patch_resource_like_relation` 的 FK 指向 `patch_comment.id` / `patch_resource.id`（不是 `patch.id`），因此不需要 patch_id remap。

### 为什么 +10M offset 够用

moyu 当前的 patch.id 范围远小于 10M（实际 < 30k）；wiki 在 kungal 数据迁完之后的 max id 也远小于 10M。所以"实际 id ∪ (实际 id + 10_000_000)"两个范围互不重叠 —— 两阶段不可能撞 PK。

将来如果 patch 数据量逼近 10M（不可能在可见的未来），需要把 offset 调大到 100M（与 migrate-users 同款）。
