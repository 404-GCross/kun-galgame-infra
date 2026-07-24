---
name: db-migration
description: Decision table and checklist for database schema changes in kun-galgame-infra (GORM models, cmd/migrate*, SQL/constraints/indexes). Use whenever a change touches the database schema, before finishing the task.
---

# DB Migration Checklist (kun-galgame-infra)

Deployment (push → CI → Dokploy redeploy) does **not** run `cmd/migrate` automatically. A skipped migration means live code reads a missing column; GORM `SELECT *` silently yields zero values → **silent failure**. Lesson: 2026-06, unmigrated `oauth_clients.moemoepoint_awarder` broke moemoepoint awarding site-wide for ~29 h.

## Which migration for which models

| Models touched | Command | Target database |
|---|---|---|
| oauth + site models (main family) | `go run ./cmd/migrate` | `kun_galgame_infra` — **never auto-run by deploy** |
| galgame (wiki-family) and/or catalog models | `go run ./cmd/migrate-catalog` (single entry for both families since W5) | galgame models → `KUN_GALGAME_PG_DATABASE` (prod `kun_catalog`, local dev `kun_galgame_wiki`); catalog models → `KUN_CATALOG_PG_DATABASE` (`kun_catalog`) | 
| `cmd/image` / `cmd/artifact` models | none — `AutoMigrate` runs at service startup | (automatic with deploy) |

Prod runs `migrate-catalog` on every deploy (compose `depends_on`), but outage-class schema changes still follow manual migrate-first ordering.

## Mandatory end-of-task report

Whenever the change touches schema, end the task by telling the user explicitly:
1. whether a migration must run,
2. the exact command,
3. against which database,
4. any ordering constraint (migrate before deploy for outage-class changes).

Production execution: `infra-tools` image + env-file dumped from the corresponding container's `.Config.Env` (see prod ops notes).
