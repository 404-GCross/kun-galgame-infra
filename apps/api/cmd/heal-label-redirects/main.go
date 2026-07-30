// heal-label-redirects repairs the two places a label merge left behind: the
// wiki bridge column galgame_official.catalog_label_id, which the merge path
// never repoints, and the catalog_work_label brand edges the official→label
// wave projected off that stale pointer onto now soft-deleted labels.
//
// Both are pure repointing — every id is resolved through catalog_redirect
// (entity_type=label) to its fixpoint, and nothing whose destination is not a
// LIVE label is touched. Idempotent: a second run finds nothing to do.
//
// Two pools, exactly like register-galgame-officials: galgame_official lives on
// the galgame (wiki) database, catalog_* on the catalog database (the same
// physical database in every current deployment, but the code never assumes
// it). Dry-run is the DEFAULT (repo convention) and prints the exact counts an
// --apply would change.
//
// No search writes: the works whose attribution changes must be reindexed, but
// that is the track lead's separate reindex-catalog run, not this tool's job.
//
//	go run ./cmd/heal-label-redirects --catalog-dsn '...'            # dry run
//	go run ./cmd/heal-label-redirects --catalog-dsn '...' --apply    # repair
package main

import (
	"flag"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"api/internal/infrastructure/database"
	"api/internal/platform/catalog/model"
	"api/pkg/config"
	"api/pkg/logger"

	"github.com/joho/godotenv"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

// healChunk bounds one UPDATE/INSERT statement's VALUES list.
const healChunk = 1000

func main() {
	apply := flag.Bool("apply", false, "write changes (default: dry run, statistics only)")
	catalogDSN := flag.String("catalog-dsn", "", "catalog DSN override (default: cfg.CatalogDatabase — the LIVE db)")
	flag.Parse()

	_ = godotenv.Load("apps/api/.env") // allow running from the repo root

	cfg, err := config.Load()
	if err != nil {
		slog.Error("load config", "error", err)
		os.Exit(1)
	}
	logger.Init(cfg.Server.Env)

	wikiDB, err := database.NewPostgresDB(cfg.GalgameDatabase)
	if err != nil {
		slog.Error("wiki db connect", "error", err)
		os.Exit(1)
	}
	defer wikiDB.Close()

	catalogDB, err := openCatalog(cfg.CatalogDatabase, *catalogDSN)
	if err != nil {
		slog.Error("catalog db connect", "error", err)
		os.Exit(1)
	}
	if s, err := catalogDB.DB(); err == nil {
		defer s.Close()
	}

	h := &healer{wiki: wikiDB.DB(), catalog: catalogDB, apply: *apply}
	if err := h.run(); err != nil {
		slog.Error("heal failed", "error", err)
		os.Exit(1)
	}
	if !*apply {
		slog.Info("DRY RUN — nothing written; re-run with --apply")
	}
}

// openCatalog opens the catalog DB: --catalog-dsn override wins, else
// cfg.CatalogDatabase (the LIVE db).
func openCatalog(base config.DatabaseConfig, override string) (*gorm.DB, error) {
	dsn := override
	if dsn == "" {
		dsn = base.DSN()
	}
	return gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: gormlogger.Default.LogMode(gormlogger.Silent)})
}

type healer struct {
	wiki    *gorm.DB
	catalog *gorm.DB
	apply   bool
	res     *resolver
}

func (h *healer) run() error {
	res, err := loadResolver(h.catalog)
	if err != nil {
		return fmt.Errorf("load redirect map: %w", err)
	}
	h.res = res
	slog.Info("label redirect map loaded", "redirects", len(res.redirects), "soft_deleted_labels", len(res.deleted))

	if err := h.healOfficials(); err != nil {
		return fmt.Errorf("step 1 (galgame_official): %w", err)
	}
	if err := h.healEdges(); err != nil {
		return fmt.Errorf("step 2 (catalog_work_label): %w", err)
	}
	return h.reportOrphanEdges()
}

// --- step 1: the wiki bridge column ----------------------------------------

// healOfficials repoints galgame_official.catalog_label_id onto the canonical
// label id. An official whose id resolves to a soft-deleted label with no
// redirect is left alone and only reported — deciding what such an official
// should point at is a curation call, not a mechanical one.
func (h *healer) healOfficials() error {
	var rows []struct {
		ID      int64 `gorm:"column:id"`
		LabelID int64 `gorm:"column:catalog_label_id"`
	}
	if err := h.wiki.Raw(`SELECT id, catalog_label_id FROM galgame_official
	                      WHERE catalog_label_id IS NOT NULL AND catalog_label_id <> 0`).
		Scan(&rows).Error; err != nil {
		return err
	}
	type pair struct{ officialID, newLabelID int64 }
	var repoint []pair
	dead := 0
	for _, row := range rows {
		canonical, live := h.res.resolve(row.LabelID)
		if !live {
			dead++
			continue
		}
		if canonical != row.LabelID {
			repoint = append(repoint, pair{row.ID, canonical})
		}
	}
	slog.Info("step 1: galgame_official.catalog_label_id",
		"mapped_officials", len(rows), "would_repoint", len(repoint), "dead_label_no_redirect", dead)
	if !h.apply || len(repoint) == 0 {
		return nil
	}
	updated := int64(0)
	for start := 0; start < len(repoint); start += healChunk {
		end := min(start+healChunk, len(repoint))
		chunk := repoint[start:end]
		err := h.wiki.Transaction(func(tx *gorm.DB) error {
			var sb strings.Builder
			sb.WriteString("UPDATE galgame_official AS o SET catalog_label_id = v.lid FROM (VALUES ")
			args := make([]any, 0, len(chunk)*2)
			for i, p := range chunk {
				if i > 0 {
					sb.WriteString(",")
				}
				sb.WriteString("(?::bigint,?::bigint)")
				args = append(args, p.officialID, p.newLabelID)
			}
			sb.WriteString(") AS v(oid, lid) WHERE o.id = v.oid")
			res := tx.Exec(sb.String(), args...)
			updated += res.RowsAffected
			return res.Error
		})
		if err != nil {
			return err
		}
	}
	slog.Info("step 1 applied", "officials_repointed", updated)
	return nil
}

// --- step 2: the attribution edges -----------------------------------------

// healEdges moves every catalog_work_label edge sitting on a redirected label
// onto the canonical label: INSERT the repointed edge first (ON CONFLICT DO
// NOTHING — the survivor may already carry the same (work, kind) edge), then
// DELETE the stale one. Insert-before-delete so a crash between the two leaves
// a duplicate edge, never a lost attribution. Both statements run in ONE
// transaction per chunk of redirected ids.
func (h *healer) healEdges() error {
	pairs := h.res.livePairs()
	if len(pairs) == 0 {
		slog.Info("step 2: catalog_work_label", "redirected_label_ids", 0, "stale_edges", 0)
		return nil
	}
	if !h.apply {
		var row struct {
			Edges int64 `gorm:"column:edges"`
			Works int64 `gorm:"column:works"`
		}
		if err := h.catalog.Raw(
			`SELECT count(*) AS edges, count(DISTINCT work_id) AS works
			 FROM catalog_work_label WHERE label_id IN ?`, oldIDs(pairs)).Scan(&row).Error; err != nil {
			return err
		}
		slog.Info("step 2: catalog_work_label",
			"redirected_label_ids", len(pairs), "would_delete_edges", row.Edges, "affected_works", row.Works)
		return nil
	}
	var inserted, deleted int64
	for start := 0; start < len(pairs); start += healChunk {
		end := min(start+healChunk, len(pairs))
		chunk := pairs[start:end]
		values, args := valuesList(chunk)
		err := h.catalog.Transaction(func(tx *gorm.DB) error {
			ins := tx.Exec(`INSERT INTO catalog_work_label (work_id, label_id, kind, source_id, created_at)
				SELECT e.work_id, v.new_id, e.kind, e.source_id, e.created_at
				FROM catalog_work_label e JOIN (VALUES `+values+`) AS v(old_id, new_id) ON e.label_id = v.old_id
				ON CONFLICT DO NOTHING`, args...)
			if ins.Error != nil {
				return ins.Error
			}
			del := tx.Exec(`DELETE FROM catalog_work_label e USING (VALUES `+values+`) AS v(old_id, new_id)
				WHERE e.label_id = v.old_id`, args...)
			if del.Error != nil {
				return del.Error
			}
			inserted += ins.RowsAffected
			deleted += del.RowsAffected
			return nil
		})
		if err != nil {
			return err
		}
	}
	slog.Info("step 2 applied", "edges_inserted", inserted, "edges_deleted", deleted)
	return nil
}

// --- step 3: what is left ---------------------------------------------------

// reportOrphanEdges counts the edges still pointing at a soft-deleted label
// that has NO redirect to follow. Nothing mechanical can fix those (there is no
// survivor to move them to), so they are reported — with a sample — and left.
func (h *healer) reportOrphanEdges() error {
	var total int64
	if err := h.catalog.Raw(`SELECT count(*) FROM catalog_work_label wl
		JOIN catalog_label l ON l.id = wl.label_id
		WHERE l.deleted_at IS NOT NULL
		  AND NOT EXISTS (SELECT 1 FROM catalog_redirect r
		                  WHERE r.entity_type = ? AND r.old_id = wl.label_id)`,
		model.EntityTypeLabel).Scan(&total).Error; err != nil {
		return err
	}
	slog.Info("step 3: edges on a deleted label with no redirect", "edges", total)
	if total == 0 {
		return nil
	}
	var sample []struct {
		WorkID  int64  `gorm:"column:work_id"`
		LabelID int64  `gorm:"column:label_id"`
		Name    string `gorm:"column:display_name"`
	}
	if err := h.catalog.Raw(`SELECT wl.work_id, wl.label_id, l.display_name FROM catalog_work_label wl
		JOIN catalog_label l ON l.id = wl.label_id
		WHERE l.deleted_at IS NOT NULL
		  AND NOT EXISTS (SELECT 1 FROM catalog_redirect r
		                  WHERE r.entity_type = ? AND r.old_id = wl.label_id)
		ORDER BY wl.label_id, wl.work_id LIMIT 10`, model.EntityTypeLabel).Scan(&sample).Error; err != nil {
		return err
	}
	for _, s := range sample {
		slog.Warn("orphan edge", "work_id", s.WorkID, "label_id", s.LabelID, "display_name", s.Name)
	}
	return nil
}

// --- the redirect map -------------------------------------------------------

// resolver is the label redirect map plus the soft-deleted set, both loaded
// whole (a redirect row per merge, a small deleted tail).
type resolver struct {
	redirects map[int64]int64
	deleted   map[int64]bool
}

func loadResolver(catalog *gorm.DB) (*resolver, error) {
	res := &resolver{redirects: map[int64]int64{}, deleted: map[int64]bool{}}
	var reds []struct {
		OldID     int64 `gorm:"column:old_id"`
		CurrentID int64 `gorm:"column:current_id"`
	}
	if err := catalog.Raw(`SELECT old_id, current_id FROM catalog_redirect WHERE entity_type = ?`,
		model.EntityTypeLabel).Scan(&reds).Error; err != nil {
		return nil, err
	}
	for _, r := range reds {
		res.redirects[r.OldID] = r.CurrentID
	}
	var deleted []int64
	if err := catalog.Raw(`SELECT id FROM catalog_label WHERE deleted_at IS NOT NULL`).
		Scan(&deleted).Error; err != nil {
		return nil, err
	}
	for _, id := range deleted {
		res.deleted[id] = true
	}
	return res, nil
}

// resolve follows the redirect chain to its fixpoint and reports whether the
// destination is a live label. Merges flatten chains to length one, but the
// walk iterates anyway (with a seen set, so corrupt cyclic data terminates).
func (res *resolver) resolve(id int64) (int64, bool) {
	seen := map[int64]bool{id: true}
	for {
		next, ok := res.redirects[id]
		if !ok || seen[next] {
			break
		}
		seen[next] = true
		id = next
	}
	if res.deleted[id] {
		return 0, false
	}
	return id, true
}

// redirectPair is one old→canonical move.
type redirectPair struct{ oldID, newID int64 }

// livePairs is every redirected id whose fixpoint is a DIFFERENT, live label —
// the exact set of ids the edge step may move.
func (res *resolver) livePairs() []redirectPair {
	var out []redirectPair
	for oldID := range res.redirects {
		canonical, live := res.resolve(oldID)
		if live && canonical != oldID {
			out = append(out, redirectPair{oldID, canonical})
		}
	}
	return out
}

func oldIDs(pairs []redirectPair) []int64 {
	out := make([]int64, len(pairs))
	for i, p := range pairs {
		out[i] = p.oldID
	}
	return out
}

// valuesList renders the (old_id, new_id) VALUES body and its arguments.
func valuesList(pairs []redirectPair) (string, []any) {
	var sb strings.Builder
	args := make([]any, 0, len(pairs)*2)
	for i, p := range pairs {
		if i > 0 {
			sb.WriteString(",")
		}
		sb.WriteString("(?::bigint,?::bigint)")
		args = append(args, p.oldID, p.newID)
	}
	return sb.String(), args
}
