// sync-vndb-links enriches published galgames with curated VNDB store/official
// links (Source="vndb"), reconciled idempotently against the current VNDB truth
// and committed through the canonical ApplySnapshot path (a minor system
// revision per change — no snapshot drift). User-added links are never touched.
//
// See internal/platform/galgame/vndb (curation) + service.SyncVndbLinks (commit).
// Two VNDB API calls per galgame, rate-limited by --gap, so a full backfill is a
// long background job — chunk it with --limit/--offset if needed.
//
//	go run ./cmd/sync-vndb-links --limit 5                 # dry run, 5 games
//	go run ./cmd/sync-vndb-links --apply --gap 1s          # apply
package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"time"

	"api/internal/infrastructure/database"
	"api/internal/platform/galgame/model"
	"api/internal/platform/galgame/service"
	"api/internal/platform/galgame/vndb"
	"api/pkg/config"
	"api/pkg/logger"
)

func main() {
	apply := flag.Bool("apply", false, "write changes (default: dry run, fetch + diff only)")
	limit := flag.Int("limit", 0, "max galgames to process (0 = all)")
	offset := flag.Int("offset", 0, "skip this many galgames (for chunking)")
	gap := flag.Duration("gap", time.Second, "min delay between VNDB API calls")
	samples := flag.Int("samples", 6, "number of per-game link previews to print")
	flag.Parse()

	cfg, err := config.Load()
	if err != nil {
		slog.Error("load config", "error", err)
		os.Exit(1)
	}
	logger.Init(cfg.Server.Env)

	db, err := database.NewPostgresDB(cfg.GalgameDatabase)
	if err != nil {
		slog.Error("connect galgame db", "error", err, "dbname", cfg.GalgameDatabase.DBName)
		os.Exit(1)
	}
	defer db.Close()

	var ids []int
	q := db.DB().Model(&model.Galgame{}).
		Where("status = 0 AND vndb_id ~ '^v[0-9]+$'").
		Order("id").Offset(*offset)
	if *limit > 0 {
		q = q.Limit(*limit)
	}
	if err := q.Pluck("id", &ids).Error; err != nil {
		slog.Error("list galgame ids", "error", err)
		os.Exit(1)
	}
	slog.Info("sync-vndb-links start", "candidates", len(ids), "apply", *apply, "gap", gap.String())

	vc := vndb.New(*gap)
	ctx := context.Background()
	var changed, failed, shown int
	for i, id := range ids {
		didChange, links, err := service.SyncVndbLinks(ctx, db.DB(), vc, id, *apply)
		if err != nil {
			failed++
			slog.Error("sync galgame", "id", id, "error", err)
			continue
		}
		if didChange {
			changed++
			if shown < *samples {
				shown++
				printPreview(id, links)
			}
		}
		if (i+1)%500 == 0 {
			slog.Info("progress", "processed", i+1, "of", len(ids), "changed", changed, "failed", failed)
		}
	}

	slog.Info("sync-vndb-links done",
		"processed", len(ids), "changed", changed, "failed", failed, "applied", *apply)
	if !*apply {
		slog.Info("DRY RUN — nothing written; re-run with --apply")
	}
}

func printPreview(id int, links []model.SnapshotLink) {
	slog.Info("would set vndb links", "galgame", id, "count", len(links))
	for _, l := range links {
		slog.Info("  link", "site", l.SourceKey, "name", l.Name, "url", l.Link)
	}
}
