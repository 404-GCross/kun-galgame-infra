// reindex-catalog (re)builds the catalog search indexes — credit names,
// characters, labels, works (wave 105), tags (A2-1d) — from kun_catalog into
// Meilisearch, applying the doc-13 config matrix. Read-side only; writes no
// Gold. Run after a bulk import wave (which skips write-through) or on a fresh
// Meilisearch instance.
//
//	go run ./cmd/reindex-catalog                                # all five
//	go run ./cmd/reindex-catalog --index=catalog_labels         # one
//	go run ./cmd/reindex-catalog --batch=5000
//
// It is also the ONLY carrier of an index SETTINGS change: EnsureIndexes runs
// unconditionally below, before any lane, so re-running this command is what
// brings a deployed Meilisearch to the declared terminal state — filterable /
// sortable / pagination settings included. There is deliberately no manual
// "PATCH these settings" step for an operator to forget, and running it twice
// changes nothing the second time.
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
	indexFlag := flag.String("index", "catalog_credit_names,catalog_characters,catalog_labels,catalog_works,catalog_tags", "comma-separated index uids")
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
			err = reindexEntity(ctx, db.DB(), idx, *batch, catalogSearch.IndexCharacters, "catalog_character", "c",
				model.EntityTypeCharacter, "character_id", "character", "catalog_character_alias", "character_id")
		case catalogSearch.IndexLabels:
			err = reindexLabels(ctx, db.DB(), idx, *batch)
		case catalogSearch.IndexWorks:
			err = reindexWorks(ctx, db.DB(), idx, *batch)
		case catalogSearch.IndexTags:
			err = reindexTags(ctx, db.DB(), idx, *batch)
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
	aliases, err := loadAliasTable(db, "catalog_label_alias", "label_id")
	if err != nil {
		return err
	}
	if err := purgeSoftDeleted(ctx, db, idx, catalogSearch.IndexLabels, "catalog_label", "b"); err != nil {
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
		if err := db.Raw(`SELECT id, display_name, lang, coalesce(latin,'') AS latin, kind FROM catalog_label
			WHERE id > ? AND deleted_at IS NULL ORDER BY id LIMIT ?`, lastID, batch).Scan(&rows).Error; err != nil {
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
			for _, a := range aliases[r.ID] {
				d.AddAlias(a.lang, a.name)
			}
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
//
// aliasTable/aliasCol name the entity's alias table; the soft-delete purge and
// the `deleted_at IS NULL` gate below assume the table carries deleted_at,
// which every caller's does (tags, which do not, have their own reindexer).
func reindexEntity(ctx context.Context, db *gorm.DB, idx *catalogSearch.Indexer, batch int, uid, table, prefix string, entityType int16, popCol, etype, aliasTable, aliasCol string) error {
	pop, err := loadPopularity(db, popCol)
	if err != nil {
		return err
	}
	srcs, keys, err := loadSources(db, entityType)
	if err != nil {
		return err
	}
	aliases, err := loadAliasTable(db, aliasTable, aliasCol)
	if err != nil {
		return err
	}
	if err := purgeSoftDeleted(ctx, db, idx, uid, table, prefix); err != nil {
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
		if err := db.Raw(fmt.Sprintf(`SELECT id, display_name, lang, coalesce(latin,'') AS latin FROM %s
			WHERE id > ? AND deleted_at IS NULL ORDER BY id LIMIT ?`, table), lastID, batch).Scan(&rows).Error; err != nil {
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
			for _, a := range aliases[r.ID] {
				d.AddAlias(a.lang, a.name)
			}
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
	return loadAliasTable(db, "catalog_name_alias", "credit_name_id")
}

// loadAliasTable reads one entity's alias table into owner id → aliases.
//
// Labels and characters have carried these tables all along — 14,845 aliases on
// 10,240 labels, 168,655 on 137,569 characters — and neither index read them,
// so an entity was findable only under its display_name. That is what made
// `Yuzusoft` return nothing while the label plainly lists it as an alias: the
// document simply had no such field. credit_names had the wiring from day one;
// this generalises it rather than inventing anything.
func loadAliasTable(db *gorm.DB, table, ownerCol string) (map[int64][]alias, error) {
	var rows []struct {
		OwnerID int64  `gorm:"column:owner_id"`
		Name    string `gorm:"column:name"`
		Lang    string `gorm:"column:lang"`
	}
	q := fmt.Sprintf(`SELECT %s AS owner_id, name, lang FROM %s`, ownerCol, table)
	if err := db.Raw(q).Scan(&rows).Error; err != nil {
		return nil, err
	}
	m := map[int64][]alias{}
	for _, r := range rows {
		m[r.OwnerID] = append(m[r.OwnerID], alias{lang: r.Lang, name: r.Name})
	}
	return m, nil
}

// purgeSoftDeleted removes the documents of rows that have been soft-deleted
// since they were last indexed.
//
// Skipping them in the build loop is NOT enough, and this is the whole reason
// the merged 「ゆずソフト」 labels stayed searchable: the reindexer UPSERTS, it
// never clears the index first, so a document written before its row was
// deleted survives every subsequent run. Merging does not touch Meilisearch
// either — the merge writes the redirect in Postgres and stops — so nothing in
// the system ever removed these. Detail pages 301 correctly; search and every
// picker built on it kept offering the dead id.
func purgeSoftDeleted(ctx context.Context, db *gorm.DB, idx *catalogSearch.Indexer, uid, table, prefix string) error {
	var ids []int64
	if err := db.Raw(fmt.Sprintf(
		`SELECT id FROM %s WHERE deleted_at IS NOT NULL ORDER BY id`, table)).Scan(&ids).Error; err != nil {
		return err
	}
	if len(ids) == 0 {
		return nil
	}
	docIDs := make([]string, len(ids))
	for i, id := range ids {
		docIDs[i] = prefix + fmt.Sprint(id)
	}
	if err := idx.DeleteBatch(ctx, uid, docIDs); err != nil {
		return err
	}
	slog.Info("purged soft-deleted documents", "index", uid, "docs", len(docIDs))
	return nil
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
// abbreviation / search-hint — kind ASC), scoped to the index population. It
// reads catalog_work_title for every live galgame work, independent of claim
// ownership. The native table already contains every official, alias,
// abbreviation, and search-hint row; latin is preserved on the same row.
// One query serves the whole population, never per work.
func loadWorkTitles(db *gorm.DB) (map[int64][]workTitle, error) {
	const population = `w.deleted_at IS NULL AND w.status = 0
		AND w.medium_id = (SELECT id FROM catalog_medium WHERE key = 'galgame')`

	m := map[int64][]workTitle{}
	var native []struct {
		WorkID int64  `gorm:"column:work_id"`
		Lang   string `gorm:"column:lang"`
		Title  string `gorm:"column:title"`
		Latin  string `gorm:"column:latin"`
		Kind   int16  `gorm:"column:kind"`
	}
	if err := db.Raw(`SELECT t.work_id, t.lang, t.title, coalesce(t.latin,'') AS latin, t.kind
		FROM catalog_work_title t
		JOIN catalog_work w ON w.id = t.work_id
		WHERE ` + population + `
		ORDER BY t.work_id, t.kind, t.lang, t.id`).Scan(&native).Error; err != nil {
		return nil, err
	}
	for _, r := range native {
		m[r.WorkID] = append(m[r.WorkID], workTitle{lang: r.Lang, title: r.Title, latin: r.Latin, kind: r.Kind})
	}
	return m, nil
}

type workIntro struct{ lang, text string }

// loadWorkIntros returns work id → merged synopsis rows for the index
// population. Claimed and bodyless works both read catalog_work_intro, merged
// to one row per language by (provenance, source_id): provenance ASC keeps a
// machine translation from beating a source row, then source_id supplies the
// stable source priority. One query serves the whole population.
func loadWorkIntros(db *gorm.DB) (map[int64][]workIntro, error) {
	const population = `w.deleted_at IS NULL AND w.status = 0
		AND w.medium_id = (SELECT id FROM catalog_medium WHERE key = 'galgame')`

	out := map[int64][]workIntro{}
	var native []struct {
		WorkID int64  `gorm:"column:work_id"`
		Lang   string `gorm:"column:lang"`
		Intro  string `gorm:"column:intro"`
	}
	if err := db.Raw(`SELECT i.work_id, i.lang, i.intro
		FROM catalog_work_intro i JOIN catalog_work w ON w.id = i.work_id
		WHERE ` + population + `
		ORDER BY i.work_id, i.lang, i.provenance, i.source_id, i.id`).Scan(&native).Error; err != nil {
		return nil, err
	}
	seen := map[int64]map[string]bool{}
	for _, r := range native {
		langs := seen[r.WorkID]
		if langs == nil {
			langs = map[string]bool{}
			seen[r.WorkID] = langs
		}
		if langs[r.Lang] {
			continue // a higher-priority row already claimed this language
		}
		langs[r.Lang] = true
		out[r.WorkID] = append(out[r.WorkID], workIntro{lang: r.Lang, text: r.Intro})
	}
	return out, nil
}

// reindexWorks builds the catalog_works index (wave 105): every LIVE galgame
// registry work — claimed AND bodyless — searchable by display_name + all
// title rows (search hints included: findability-only, never displayed), with
// content_rating filterable (the public nsfw gate) and a cross-population
// popularity tiebreaker.
//
// A2-1d adds the product-search axes: the tag / label / engine / series id
// arrays, the earliest-release ordinal, olang, claimed and updated_ts. They
// exist so GET /v1/catalog/works/search can push its WHOLE filter set into
// Meilisearch — that is what makes its total, its facets and its items share
// one gate instead of the deprecated face's unfiltered-total trap.
//
// A2-R5 adds content_limit, the EDITORIAL DISPLAY axis: a claimed work's value
// comes from the editor's declaration (catalog_work.display_nsfw — a wiki-body
// LEFT JOIN until the W1-pre wave nativized it, refs/proj/140 §5b), a bodyless
// work's from its rating. It is a second axis beside content_rating, never a
// re-encoding of it.
//
// A2-1f adds the synopsis text, language-bucketed and rune-capped. It only
// ever MATCHES when a caller passes search_intro=1: the face pins
// attributesToSearchOn to the title family otherwise, so growing the index does
// not widen anybody's existing query.
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
	facets, err := loadWorksFacets(db)
	if err != nil {
		return err
	}
	intros, err := loadWorkIntros(db)
	if err != nil {
		return err
	}
	processed, lastID := 0, int64(0)
	for {
		var rows []struct {
			ID          int64  `gorm:"column:id"`
			DisplayName string `gorm:"column:display_name"`
			// Explicit column tag: GORM snake-cases OLang to o_lang, which
			// matches no result column and would scan as "" — the trap that left
			// the works list's olang empty from W1 until A2-1a.
			OLang         string `gorm:"column:olang"`
			ContentRating int16  `gorm:"column:content_rating"`
			Site          string `gorm:"column:site"`
			// The two other claim columns (A2-R1 区 C): the claim_state field is
			// projected from all three through model.ClaimStateKey, which is the
			// read face's own projection — so `claim_state=live` selects exactly
			// the rows whose records render claimed_by.state=live.
			ProductWorkID *int64    `gorm:"column:product_work_id"`
			ClaimState    *int16    `gorm:"column:claim_state"`
			UpdatedAt     time.Time `gorm:"column:updated_at"`
			// DisplayNSFW is the CLAIMED work's editorial display flag — the
			// authority for the content_limit field, which model.DisplayLimitKey
			// projects alongside the three claim columns (A2-R5). A column on the
			// row since the W1-pre wave nativized it off the wiki body
			// (refs/proj/140 §5b); it was a LEFT JOIN into galgame before.
			DisplayNSFW bool `gorm:"column:display_nsfw"`
		}
		if err := db.Raw(`SELECT w.id, w.display_name, w.olang, w.content_rating, coalesce(w.site,'') AS site,
				w.product_work_id, w.claim_state, w.updated_at, w.display_nsfw
			FROM catalog_work w
			WHERE w.id > ? AND w.deleted_at IS NULL AND w.status = 0
				AND w.medium_id = (SELECT id FROM catalog_medium WHERE key = 'galgame')
			ORDER BY w.id LIMIT ?`, lastID, batch).Scan(&rows).Error; err != nil {
			return err
		}
		if len(rows) == 0 {
			break
		}
		docs := make([]catalogSearch.EntityDoc, len(rows))
		for i, r := range rows {
			in := catalogSearch.WorkDocInput{
				ID: r.ID, DisplayName: r.DisplayName, OLang: r.OLang,
				ContentRating: r.ContentRating,
				// claimed == "a product site owns this row", i.e. the works
				// list's `w.site <> ''` (NULL and '' are both bodyless).
				Claimed:      r.Site != "",
				ClaimState:   model.ClaimStateKey(&r.Site, r.ProductWorkID, r.ClaimState),
				ContentLimit: model.DisplayLimitKey(&r.Site, r.ProductWorkID, r.DisplayNSFW, r.ContentRating),
				ReleasedOrd:  facets.releasedOrd[r.ID], UpdatedTS: r.UpdatedAt.Unix(),
				Popularity: pop[r.ID], Sources: srcs[r.ID], SourceKeys: keys[r.ID],
				TagIDs: facets.tagIDs[r.ID], LabelIDs: facets.labelIDs[r.ID],
				EngineIDs: facets.engineIDs[r.ID], SeriesIDs: facets.seriesIDs[r.ID],
			}
			for _, t := range titles[r.ID] {
				in.Titles = append(in.Titles, catalogSearch.WorkDocTitle{Lang: t.lang, Title: t.title, Latin: t.latin})
			}
			for _, iv := range intros[r.ID] {
				in.Intros = append(in.Intros, catalogSearch.WorkDocIntro{Lang: iv.lang, Text: iv.text})
			}
			docs[i] = catalogSearch.BuildWorkDoc(in)
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
