// sync-vndb-links enriches published galgames with curated VNDB store/official
// links (Source="vndb"), reconciled idempotently against the current VNDB truth.
// User-added links are never touched.
//
// Links are fetched in BATCHES of up to 100 VNs (one /vn call + paginated
// /release calls per batch — see vndb.FetchGameLinksBatch), so a full backfill
// is ~one round of API calls per 100 games instead of two per game. Each game is
// then reconciled and committed via APPROACH B (rewrite galgame_link + jsonb-
// patch the latest revision snapshot, no new revision — see
// service.ReconcileVndbLinks).
//
//	go run ./cmd/sync-vndb-links --limit 100               # dry run, 1 batch
//	go run ./cmd/sync-vndb-links --apply --gap 2s          # apply, all
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

const batchSize = 100

func main() {
	apply := flag.Bool("apply", false, "write changes (default: dry run, fetch + diff only)")
	limit := flag.Int("limit", 0, "max galgames to process (0 = all)")
	offset := flag.Int("offset", 0, "skip this many galgames (for chunking)")
	gap := flag.Duration("gap", 2*time.Second, "min delay between VNDB API calls")
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

	type candidate struct {
		ID     int
		VNDBID string `gorm:"column:vndb_id"`
	}
	var cands []candidate
	q := db.DB().Model(&model.Galgame{}).
		Select("id", "vndb_id").
		Where("status = 0 AND vndb_id ~ '^v[0-9]+$'").
		Order("id").Offset(*offset)
	if *limit > 0 {
		q = q.Limit(*limit)
	}
	if err := q.Scan(&cands).Error; err != nil {
		slog.Error("list galgame candidates", "error", err)
		os.Exit(1)
	}
	slog.Info("sync-vndb-links start", "candidates", len(cands), "batch", batchSize, "apply", *apply, "gap", gap.String())

	vc := vndb.New(*gap)
	ctx := context.Background()
	var changed, failed, shown int
	for start := 0; start < len(cands); start += batchSize {
		end := min(start+batchSize, len(cands))
		batch := cands[start:end]

		ids := make([]string, 0, len(batch))
		for _, c := range batch {
			ids = append(ids, c.VNDBID)
		}
		linksByVNDB, err := vc.FetchGameLinksBatch(ids)
		if err != nil {
			failed += len(batch)
			slog.Error("fetch batch", "from", batch[0].ID, "to", batch[len(batch)-1].ID, "error", err)
			continue
		}

		for _, c := range batch {
			fresh := linksByVNDB[c.VNDBID]
			didChange, err := service.ReconcileVndbLinks(ctx, db.DB(), c.ID, fresh, *apply)
			if err != nil {
				failed++
				slog.Error("reconcile galgame", "id", c.ID, "error", err)
				continue
			}
			if didChange {
				changed++
				if shown < *samples {
					shown++
					printPreview(c.ID, fresh)
				}
			}
		}
		slog.Info("progress", "processed", end, "of", len(cands), "changed", changed, "failed", failed)
	}

	slog.Info("sync-vndb-links done",
		"processed", len(cands), "changed", changed, "failed", failed, "applied", *apply)
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
