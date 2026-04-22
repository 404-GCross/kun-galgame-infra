// reindex-search rebuilds the Meilisearch indexes (galgames / tags / officials)
// from the wiki Postgres database. Run this:
//   - after first deploy (no documents yet)
//   - after sync-vndb / migrate-* / bulk scripts that skip write-through
//   - when diagnostics show drift between DB and MS
//
// Usage:
//   go run ./cmd/reindex-search                       # all three indexes
//   go run ./cmd/reindex-search --index=galgames      # one
//   go run ./cmd/reindex-search --index=galgames,tags # comma-separated subset
//   go run ./cmd/reindex-search --batch=500           # tune batch size
package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"strings"
	"time"

	"api/internal/infrastructure/database"
	searchInfra "api/internal/infrastructure/search"
	"api/internal/platform/galgame/model"
	galgameSearch "api/internal/platform/galgame/search"
	"api/pkg/config"
	"api/pkg/logger"

	"gorm.io/gorm"
)

func main() {
	indexFlag := flag.String("index", "galgames,tags,officials", "Comma-separated index uids to rebuild")
	batchSize := flag.Int("batch", 1000, "Batch size per Meilisearch upsert")
	flag.Parse()

	cfg, err := config.Load()
	if err != nil {
		slog.Error("load config", "error", err)
		os.Exit(1)
	}
	logger.Init(cfg.Server.Env)

	db, err := database.NewPostgresDB(cfg.GalgameDatabase)
	if err != nil {
		slog.Error("connect wiki database", "error", err)
		os.Exit(1)
	}

	client, err := searchInfra.NewClient(cfg.Meilisearch)
	if err != nil {
		slog.Error("meilisearch client", "error", err)
		os.Exit(1)
	}
	if err := client.Health(); err != nil {
		slog.Error("meilisearch health", "error", err)
		os.Exit(1)
	}

	if err := galgameSearch.EnsureIndexes(client); err != nil {
		slog.Error("ensure indexes", "error", err)
		os.Exit(1)
	}

	indexer := galgameSearch.NewIndexer(client)
	targets := parseTargets(*indexFlag)
	ctx := context.Background()

	start := time.Now()
	for _, t := range targets {
		switch t {
		case "galgames":
			if err := reindexGalgames(ctx, db.DB(), indexer, *batchSize); err != nil {
				slog.Error("reindex galgames", "error", err)
				os.Exit(1)
			}
		case "tags":
			if err := reindexTags(ctx, db.DB(), indexer, *batchSize); err != nil {
				slog.Error("reindex tags", "error", err)
				os.Exit(1)
			}
		case "officials":
			if err := reindexOfficials(ctx, db.DB(), indexer, *batchSize); err != nil {
				slog.Error("reindex officials", "error", err)
				os.Exit(1)
			}
		default:
			slog.Warn("unknown index, skipping", "index", t)
		}
	}

	slog.Info("reindex complete", "duration", time.Since(start))
}

func parseTargets(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// ─────────────────────────── per-index reindexers ───────────────────────────

func reindexGalgames(ctx context.Context, db *gorm.DB, idx *galgameSearch.Indexer, batch int) error {
	var total int64
	db.Model(&model.Galgame{}).Count(&total)
	slog.Info("reindex galgames start", "total", total, "batch", batch)

	// Manual keyset pagination (id-based), streaming to avoid loading all 60k at once.
	// Simpler and more predictable than FindInBatches.
	processed := 0
	lastID := 0
	for {
		var rows []model.Galgame
		if err := db.
			Preload("Alias").
			Preload("Tag.Tag").
			Preload("Official.Official").
			Preload("Engine.Engine").
			Where("id > ?", lastID).
			Order("id ASC").
			Limit(batch).
			Find(&rows).Error; err != nil {
			return err
		}
		if len(rows) == 0 {
			break
		}

		ptrs := make([]*model.Galgame, len(rows))
		for i := range rows {
			ptrs[i] = &rows[i]
		}
		if err := idx.UpsertGalgamesBatch(ctx, ptrs); err != nil {
			return err
		}

		processed += len(rows)
		lastID = rows[len(rows)-1].ID
		slog.Info("galgames progress", "processed", processed, "total", total)

		if len(rows) < batch {
			break
		}
	}

	slog.Info("reindex galgames done", "processed", processed)
	return nil
}

func reindexTags(ctx context.Context, db *gorm.DB, idx *galgameSearch.Indexer, batch int) error {
	// Tags are small (~3k); just load all with galgame_count.
	var items []model.GalgameTag
	if err := db.Preload("Alias").
		Select("galgame_tag.*, COALESCE(tc.cnt, 0) AS cnt").
		Joins("LEFT JOIN (SELECT r.tag_id, COUNT(*) AS cnt FROM galgame_tag_relation r JOIN galgame g ON g.id = r.galgame_id AND g.status = 0 GROUP BY r.tag_id) tc ON tc.tag_id = galgame_tag.id").
		Find(&items).Error; err != nil {
		return err
	}
	slog.Info("reindex tags", "count", len(items))

	ptrs := make([]*model.GalgameTag, len(items))
	for i := range items {
		ptrs[i] = &items[i]
	}

	// Push in batches
	for i := 0; i < len(ptrs); i += batch {
		end := min(i+batch, len(ptrs))
		if err := idx.UpsertTagsBatch(ctx, ptrs[i:end]); err != nil {
			return err
		}
	}
	slog.Info("reindex tags done")
	return nil
}

func reindexOfficials(ctx context.Context, db *gorm.DB, idx *galgameSearch.Indexer, batch int) error {
	var items []model.GalgameOfficial
	if err := db.Preload("Alias").
		Select("galgame_official.*, COALESCE(oc.cnt, 0) AS cnt").
		Joins("LEFT JOIN (SELECT r.official_id, COUNT(*) AS cnt FROM galgame_official_relation r JOIN galgame g ON g.id = r.galgame_id AND g.status = 0 GROUP BY r.official_id) oc ON oc.official_id = galgame_official.id").
		Find(&items).Error; err != nil {
		return err
	}
	slog.Info("reindex officials", "count", len(items))

	ptrs := make([]*model.GalgameOfficial, len(items))
	for i := range items {
		ptrs[i] = &items[i]
	}

	for i := 0; i < len(ptrs); i += batch {
		end := min(i+batch, len(ptrs))
		if err := idx.UpsertOfficialsBatch(ctx, ptrs[i:end]); err != nil {
			return err
		}
	}
	slog.Info("reindex officials done")
	return nil
}
