// reindex-catalog (re)builds the catalog search indexes — credit names,
// characters, labels, works (wave 105) — from kun_catalog into Meilisearch, applying the doc-13
// config matrix. Read-side only; writes no Gold. Run after a bulk import wave
// (which skips write-through) or on a fresh Meilisearch instance.
//
//	go run ./cmd/reindex-catalog                                # all three
//	go run ./cmd/reindex-catalog --index=catalog_labels         # one
//	go run ./cmd/reindex-catalog --batch=5000
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"math"
	"os"
	"strings"
	"time"

	"api/internal/infrastructure/database"
	searchInfra "api/internal/infrastructure/search"
	"api/internal/platform/catalog/model"
	catalogSearch "api/internal/platform/catalog/search"
	"api/pkg/config"
	"api/pkg/logger"

	"gorm.io/gorm"
)

func main() {
	indexFlag := flag.String("index", "catalog_credit_names,catalog_characters,catalog_labels,catalog_works", "comma-separated index uids")
	batch := flag.Int("batch", 5000, "batch size per Meilisearch upsert")
	flag.Parse()

	cfg, err := config.Load()
	if err != nil {
		slog.Error("load config", "error", err)
		os.Exit(1)
	}
	logger.Init(cfg.Server.Env)

	db, err := database.NewPostgresDB(cfg.CatalogDatabase)
	if err != nil {
		slog.Error("catalog db connect", "error", err)
		os.Exit(1)
	}
	defer db.Close()
	client, err := searchInfra.NewClient(cfg.Meilisearch)
	if err != nil {
		slog.Error("meilisearch client", "error", err)
		os.Exit(1)
	}
	if err := client.Health(); err != nil {
		slog.Error("meilisearch unreachable", "error", err)
		os.Exit(1)
	}
	if err := catalogSearch.EnsureIndexes(client); err != nil {
		slog.Error("ensure indexes", "error", err)
		os.Exit(1)
	}

	idx := catalogSearch.NewIndexer(client)
	ctx := context.Background()
	start := time.Now()
	for _, t := range strings.Split(*indexFlag, ",") {
		t = strings.TrimSpace(t)
		var err error
		switch t {
		case catalogSearch.IndexCreditNames:
			err = reindexCreditNames(ctx, db.DB(), idx, *batch)
		case catalogSearch.IndexCharacters:
			err = reindexEntity(ctx, db.DB(), idx, *batch, catalogSearch.IndexCharacters, "catalog_character", "c", model.EntityTypeCharacter, "character_id", "character")
		case catalogSearch.IndexLabels:
			err = reindexLabels(ctx, db.DB(), idx, *batch)
		case catalogSearch.IndexWorks:
			err = reindexWorks(ctx, db.DB(), idx, *batch)
		case "":
			continue
		default:
			slog.Warn("unknown index, skipping", "index", t)
			continue
		}
		if err != nil {
			slog.Error("reindex failed", "index", t, "error", err)
			os.Exit(1)
		}
	}
	slog.Info("reindex-catalog complete", "duration", time.Since(start))
}

// --- shared preloads -------------------------------------------------------

// loadPopularity returns entity id → raw credit count for a credit column.
func loadPopularity(db *gorm.DB, col string) (map[int64]int, error) {
	var rows []struct {
		ID  int64 `gorm:"column:id"`
		Cnt int   `gorm:"column:cnt"`
	}
	where := ""
	if col != "credit_name_id" {
		where = "WHERE " + col + " IS NOT NULL"
	}
	if err := db.Raw(fmt.Sprintf(`SELECT %s AS id, count(*) AS cnt FROM catalog_credit %s GROUP BY %s`, col, where, col)).Scan(&rows).Error; err != nil {
		return nil, err
	}
	m := make(map[int64]int, len(rows))
	for _, r := range rows {
		m[r.ID] = r.Cnt
	}
	return m, nil
}

// loadSources returns entity id → ("key:ext" list, distinct key list).
func loadSources(db *gorm.DB, entityType int16) (map[int64][]string, map[int64][]string, error) {
	var rows []struct {
		EntityID int64  `gorm:"column:entity_id"`
		Key      string `gorm:"column:key"`
		Ext      string `gorm:"column:external_id"`
	}
	if err := db.Raw(`SELECT r.entity_id, s.key, r.external_id FROM catalog_external_ref r
		JOIN catalog_source s ON s.id = r.source_id WHERE r.entity_type = ? ORDER BY r.entity_id`, entityType).Scan(&rows).Error; err != nil {
		return nil, nil, err
	}
	srcs := map[int64][]string{}
	keys := map[int64][]string{}
	keySeen := map[int64]map[string]bool{}
	for _, r := range rows {
		srcs[r.EntityID] = append(srcs[r.EntityID], r.Key+":"+r.Ext)
		if keySeen[r.EntityID] == nil {
			keySeen[r.EntityID] = map[string]bool{}
		}
		if !keySeen[r.EntityID][r.Key] {
			keySeen[r.EntityID][r.Key] = true
			keys[r.EntityID] = append(keys[r.EntityID], r.Key)
		}
	}
	return srcs, keys, nil
}

// --- per-index reindexers --------------------------------------------------

func reindexCreditNames(ctx context.Context, db *gorm.DB, idx *catalogSearch.Indexer, batch int) error {
	pop, err := loadPopularity(db, "credit_name_id")
	if err != nil {
		return err
	}
	srcs, keys, err := loadSources(db, model.EntityTypeCreditName)
	if err != nil {
		return err
	}
	aliases, err := loadAliases(db)
	if err != nil {
		return err
	}
	processed, lastID := 0, int64(0)
	for {
		var rows []struct {
			ID       int64  `gorm:"column:id"`
			Name     string `gorm:"column:name"`
			Lang     string `gorm:"column:lang"`
			Latin    string `gorm:"column:latin"`
			PersonID *int64 `gorm:"column:person_id"`
		}
		if err := db.Raw(`SELECT id, name, lang, coalesce(latin,'') AS latin, person_id FROM catalog_credit_name WHERE id > ? ORDER BY id LIMIT ?`, lastID, batch).Scan(&rows).Error; err != nil {
			return err
		}
		if len(rows) == 0 {
			break
		}
		docs := make([]catalogSearch.EntityDoc, len(rows))
		for i, r := range rows {
			d := catalogSearch.EntityDoc{
				ID: "n" + fmt.Sprint(r.ID), EntityType: "credit_name", Latin: r.Latin,
				Sources: srcs[r.ID], SourceKeys: keys[r.ID], PersonID: r.PersonID,
				Popularity: catalogSearch.Popularity(pop[r.ID]),
			}
			d.SetName(r.Lang, r.Name)
			for _, a := range aliases[r.ID] {
				d.AddAlias(a.lang, a.name)
			}
			docs[i] = d
		}
		if err := idx.UpsertBatch(ctx, catalogSearch.IndexCreditNames, docs); err != nil {
			return err
		}
		processed += len(rows)
		lastID = rows[len(rows)-1].ID
	}
	slog.Info("reindexed", "index", catalogSearch.IndexCreditNames, "docs", processed)
	return nil
}

func reindexLabels(ctx context.Context, db *gorm.DB, idx *catalogSearch.Indexer, batch int) error {
	pop, err := loadPopularity(db, "label_id")
	if err != nil {
		return err
	}
	srcs, keys, err := loadSources(db, model.EntityTypeLabel)
	if err != nil {
		return err
	}
	processed, lastID := 0, int64(0)
	for {
		var rows []struct {
			ID    int64  `gorm:"column:id"`
			Name  string `gorm:"column:display_name"`
			Lang  string `gorm:"column:lang"`
			Latin string `gorm:"column:latin"`
			Kind  int16  `gorm:"column:kind"`
		}
		if err := db.Raw(`SELECT id, display_name, lang, coalesce(latin,'') AS latin, kind FROM catalog_label WHERE id > ? ORDER BY id LIMIT ?`, lastID, batch).Scan(&rows).Error; err != nil {
			return err
		}
		if len(rows) == 0 {
			break
		}
		docs := make([]catalogSearch.EntityDoc, len(rows))
		for i, r := range rows {
			kind := r.Kind
			d := catalogSearch.EntityDoc{
				ID: "b" + fmt.Sprint(r.ID), EntityType: "label", Latin: r.Latin,
				Sources: srcs[r.ID], SourceKeys: keys[r.ID], Kind: &kind,
				Popularity: catalogSearch.Popularity(pop[r.ID]),
			}
			d.SetName(r.Lang, r.Name)
			docs[i] = d
		}
		if err := idx.UpsertBatch(ctx, catalogSearch.IndexLabels, docs); err != nil {
			return err
		}
		processed += len(rows)
		lastID = rows[len(rows)-1].ID
	}
	slog.Info("reindexed", "index", catalogSearch.IndexLabels, "docs", processed)
	return nil
}

// reindexEntity handles the simple display_name entities (characters).
func reindexEntity(ctx context.Context, db *gorm.DB, idx *catalogSearch.Indexer, batch int, uid, table, prefix string, entityType int16, popCol, etype string) error {
	pop, err := loadPopularity(db, popCol)
	if err != nil {
		return err
	}
	srcs, keys, err := loadSources(db, entityType)
	if err != nil {
		return err
	}
	processed, lastID := 0, int64(0)
	for {
		var rows []struct {
			ID    int64  `gorm:"column:id"`
			Name  string `gorm:"column:display_name"`
			Lang  string `gorm:"column:lang"`
			Latin string `gorm:"column:latin"`
		}
		if err := db.Raw(fmt.Sprintf(`SELECT id, display_name, lang, coalesce(latin,'') AS latin FROM %s WHERE id > ? ORDER BY id LIMIT ?`, table), lastID, batch).Scan(&rows).Error; err != nil {
			return err
		}
		if len(rows) == 0 {
			break
		}
		docs := make([]catalogSearch.EntityDoc, len(rows))
		for i, r := range rows {
			d := catalogSearch.EntityDoc{
				ID: prefix + fmt.Sprint(r.ID), EntityType: etype, Latin: r.Latin,
				Sources: srcs[r.ID], SourceKeys: keys[r.ID], Popularity: catalogSearch.Popularity(pop[r.ID]),
			}
			d.SetName(r.Lang, r.Name)
			docs[i] = d
		}
		if err := idx.UpsertBatch(ctx, uid, docs); err != nil {
			return err
		}
		processed += len(rows)
		lastID = rows[len(rows)-1].ID
	}
	slog.Info("reindexed", "index", uid, "docs", processed)
	return nil
}

type alias struct{ lang, name string }

func loadAliases(db *gorm.DB) (map[int64][]alias, error) {
	var rows []struct {
		CreditNameID int64  `gorm:"column:credit_name_id"`
		Name         string `gorm:"column:name"`
		Lang         string `gorm:"column:lang"`
	}
	if err := db.Raw(`SELECT credit_name_id, name, lang FROM catalog_name_alias`).Scan(&rows).Error; err != nil {
		return nil, err
	}
	m := map[int64][]alias{}
	for _, r := range rows {
		m[r.CreditNameID] = append(m[r.CreditNameID], alias{lang: r.Lang, name: r.Name})
	}
	return m, nil
}

// --- works lane (wave 105: public catalog title search) ---------------------

// loadWorkPopularitySignal returns work id → log-damped max(bgm_collect,
// dlsite dl_count) — a cross-population ranking signal (claimed works carry
// bgm shelves since T2b; bodyless dlsite works carry dl_count).
func loadWorkPopularitySignal(db *gorm.DB) (map[int64]float64, error) {
	var rows []struct {
		WorkID int64 `gorm:"column:work_id"`
		V      int64 `gorm:"column:v"`
	}
	if err := db.Raw(`SELECT work_id, max(value) AS v FROM catalog_work_popularity
		WHERE metric IN (?, ?) GROUP BY work_id`,
		model.PopularityMetricBgmCollect, model.PopularityMetricDownloads).Scan(&rows).Error; err != nil {
		return nil, err
	}
	m := make(map[int64]float64, len(rows))
	for _, r := range rows {
		m[r.WorkID] = math.Log1p(float64(r.V))
	}
	return m, nil
}

type workTitle struct {
	lang, title, latin string
	kind               int16
}

// loadWorkTitles returns work id → title rows (official first, then alias /
// abbreviation / search-hint — kind ASC), scoped to the index population.
func loadWorkTitles(db *gorm.DB) (map[int64][]workTitle, error) {
	var rows []struct {
		WorkID int64  `gorm:"column:work_id"`
		Lang   string `gorm:"column:lang"`
		Title  string `gorm:"column:title"`
		Latin  string `gorm:"column:latin"`
		Kind   int16  `gorm:"column:kind"`
	}
	if err := db.Raw(`SELECT t.work_id, t.lang, t.title, coalesce(t.latin,'') AS latin, t.kind
		FROM catalog_work_title t
		JOIN catalog_work w ON w.id = t.work_id AND w.deleted_at IS NULL
			AND w.medium_id = (SELECT id FROM catalog_medium WHERE key = 'galgame') AND w.status = 0
		ORDER BY t.work_id, t.kind, t.lang`).Scan(&rows).Error; err != nil {
		return nil, err
	}
	m := map[int64][]workTitle{}
	for _, r := range rows {
		m[r.WorkID] = append(m[r.WorkID], workTitle{lang: r.Lang, title: r.Title, latin: r.Latin, kind: r.Kind})
	}
	return m, nil
}

// guessLang buckets a bare display_name (no lang column on catalog_work) for
// the CJK-locale index fields: kana → ja; han-only → zh (a kanji-only Japanese
// title tokenizes acceptably under cmn — CJK ideographs segment alike); else
// latin/other.
func guessLang(s string) string {
	hasHan := false
	for _, r := range s {
		if (r >= 'ぁ' && r <= 'ん') || (r >= 'ァ' && r <= 'ヶ') || r == 'ー' {
			return "ja"
		}
		if r >= 0x4E00 && r <= 0x9FFF {
			hasHan = true
		}
	}
	if hasHan {
		return "zh"
	}
	return "en"
}

// reindexWorks builds the catalog_works index (wave 105): every LIVE galgame
// registry work — claimed AND bodyless — searchable by display_name + all
// title rows (search hints included: findability-only, never displayed), with
// content_rating filterable (the public nsfw gate) and a cross-population
// popularity tiebreaker.
func reindexWorks(ctx context.Context, db *gorm.DB, idx *catalogSearch.Indexer, batch int) error {
	pop, err := loadWorkPopularitySignal(db)
	if err != nil {
		return err
	}
	srcs, keys, err := loadSources(db, model.EntityTypeWork)
	if err != nil {
		return err
	}
	titles, err := loadWorkTitles(db)
	if err != nil {
		return err
	}
	processed, lastID := 0, int64(0)
	for {
		var rows []struct {
			ID            int64  `gorm:"column:id"`
			DisplayName   string `gorm:"column:display_name"`
			ContentRating int16  `gorm:"column:content_rating"`
		}
		if err := db.Raw(`SELECT id, display_name, content_rating FROM catalog_work
			WHERE id > ? AND deleted_at IS NULL AND status = 0
				AND medium_id = (SELECT id FROM catalog_medium WHERE key = 'galgame')
			ORDER BY id LIMIT ?`, lastID, batch).Scan(&rows).Error; err != nil {
			return err
		}
		if len(rows) == 0 {
			break
		}
		docs := make([]catalogSearch.EntityDoc, len(rows))
		for i, r := range rows {
			cr := r.ContentRating
			d := catalogSearch.EntityDoc{
				ID: "w" + fmt.Sprint(r.ID), EntityType: "work",
				Sources: srcs[r.ID], SourceKeys: keys[r.ID],
				ContentRating: &cr, Popularity: pop[r.ID],
			}
			seen := map[string]bool{r.DisplayName: true}
			d.SetNameOrAlias(guessLang(r.DisplayName), r.DisplayName)
			for _, t := range titles[r.ID] {
				if d.Latin == "" && t.latin != "" {
					d.Latin = t.latin
				}
				if seen[t.title] {
					continue
				}
				seen[t.title] = true
				lang := t.lang
				if lang == "" {
					lang = guessLang(t.title)
				}
				d.SetNameOrAlias(lang, t.title)
			}
			docs[i] = d
		}
		if err := idx.UpsertBatch(ctx, catalogSearch.IndexWorks, docs); err != nil {
			return err
		}
		processed += len(rows)
		lastID = rows[len(rows)-1].ID
	}
	slog.Info("reindexed", "index", catalogSearch.IndexWorks, "docs", processed)
	return nil
}
