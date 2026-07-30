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
			err = reindexEntity(ctx, db.DB(), idx, *batch, catalogSearch.IndexCharacters, "catalog_character", "c", model.EntityTypeCharacter, "character_id", "character")
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
// abbreviation / search-hint — kind ASC), scoped to the index population. It
// mirrors the READ face's own title merge (ReadService.loadWorkTitles, A2-R1),
// because a work must not be findable by a name the read face would never show
// — and, far more importantly here, must BE findable by the names it does show:
//
//   - CLAIMED works bridge the wiki body — galgame's four fixed name columns
//     pivot to BCP-47 official rows and galgame_alias rows become alias rows
//     with no language, with the read face's own intra-bridge dedup (an alias
//     whose string is already an official name is one name, indexed once);
//   - BODYLESS works read catalog_work_title verbatim, every kind.
//
// Strict XOR on the DISPLAY lane, like the read face: a claimed work's native
// display rows are not read. The SEARCH-HINT lane is deliberately NOT part of
// that XOR — hints are catalog-native findability rows with no wiki counterpart
// (the DLsite importer attaches a store product name to a work it did not
// create, and thousands of those sit on claimed works), they are never
// displayed anywhere, and dropping them would make a findability FIX remove
// findability. So a claimed work keeps its hints alongside the bridge — the
// screenshot facet's (facet, source) XOR refinement, applied to titles: two
// lanes carrying different rows, not two copies of one row.
//
// Three queries for the whole population, never per work.
func loadWorkTitles(db *gorm.DB) (map[int64][]workTitle, error) {
	const population = `w.deleted_at IS NULL AND w.status = 0
		AND w.medium_id = (SELECT id FROM catalog_medium WHERE key = 'galgame')`

	m := map[int64][]workTitle{}

	// ── claimed: the wiki bridge, official names first ──
	var names []struct {
		WorkID   int64  `gorm:"column:work_id"`
		NameJaJP string `gorm:"column:name_ja_jp"`
		NameEnUS string `gorm:"column:name_en_us"`
		NameZhCN string `gorm:"column:name_zh_cn"`
		NameZhTW string `gorm:"column:name_zh_tw"`
	}
	if err := db.Raw(`SELECT w.id AS work_id, g.name_ja_jp, g.name_en_us, g.name_zh_cn, g.name_zh_tw
		FROM catalog_work w JOIN galgame g ON g.id = w.product_work_id
		WHERE w.site = 'galgame_wiki' AND ` + population).Scan(&names).Error; err != nil {
		return nil, err
	}
	// The pivot is the read face's, verbatim — same columns, same BCP-47 tags.
	official := map[int64]map[string]bool{}
	for _, r := range names {
		seen := map[string]bool{}
		for _, p := range []struct{ lang, text string }{
			{"ja", r.NameJaJP}, {"en", r.NameEnUS},
			{"zh-Hans", r.NameZhCN}, {"zh-Hant", r.NameZhTW},
		} {
			title := strings.TrimSpace(p.text)
			if title == "" {
				continue
			}
			seen[title] = true
			m[r.WorkID] = append(m[r.WorkID], workTitle{
				lang: p.lang, title: title, kind: model.WorkTitleKindOfficial,
			})
		}
		official[r.WorkID] = seen
	}

	// ── claimed: the wiki aliases, no language (the wiki records none) ──
	var aliases []struct {
		WorkID int64  `gorm:"column:work_id"`
		Name   string `gorm:"column:name"`
	}
	if err := db.Raw(`SELECT w.id AS work_id, a.name
		FROM catalog_work w JOIN galgame_alias a ON a.galgame_id = w.product_work_id
		WHERE w.site = 'galgame_wiki' AND ` + population + `
		ORDER BY w.id, a.id`).Scan(&aliases).Error; err != nil {
		return nil, err
	}
	for _, a := range aliases {
		name := strings.TrimSpace(a.Name)
		if name == "" || official[a.WorkID][name] {
			continue
		}
		m[a.WorkID] = append(m[a.WorkID], workTitle{title: name, kind: model.WorkTitleKindAlias})
	}

	// ── native: every kind for a BODYLESS work, search hints only for a claimed
	// one (see the doc comment — the hint lane has no wiki counterpart).
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
		WHERE `+population+` AND (coalesce(w.site, '') = '' OR t.kind = ?)
		ORDER BY t.work_id, t.kind, t.lang`, model.WorkTitleKindSearchHint).Scan(&native).Error; err != nil {
		return nil, err
	}
	for _, r := range native {
		m[r.WorkID] = append(m[r.WorkID], workTitle{lang: r.Lang, title: r.Title, latin: r.Latin, kind: r.Kind})
	}
	return m, nil
}

type workIntro struct{ lang, text string }

// loadWorkIntros returns work id → merged synopsis rows for the index
// population (A2-1f). It mirrors the READ face's own merge exactly
// (ReadService.loadWorkIntros), because a work must not be findable by text
// the read face would never show:
//
//   - CLAIMED works bridge the wiki body's four fixed language columns
//     (galgame.intro_*), pivoted to BCP-47 — bridge-not-copy, same as the read
//     face;
//   - BODYLESS works read catalog_work_intro, merged to ONE row per language
//     by (provenance, source_id): provenance ASC keeps a machine translation
//     from beating a source row (step 75), then the usual source priority.
//
// Strict XOR, like the read face: a claimed work never falls back to native
// rows. Two queries for the whole population, never per work.
func loadWorkIntros(db *gorm.DB) (map[int64][]workIntro, error) {
	const population = `w.deleted_at IS NULL AND w.status = 0
		AND w.medium_id = (SELECT id FROM catalog_medium WHERE key = 'galgame')`

	out := map[int64][]workIntro{}

	// ── claimed: the wiki bridge ──
	var bridged []struct {
		WorkID    int64  `gorm:"column:work_id"`
		IntroJaJP string `gorm:"column:intro_ja_jp"`
		IntroEnUS string `gorm:"column:intro_en_us"`
		IntroZhCN string `gorm:"column:intro_zh_cn"`
		IntroZhTW string `gorm:"column:intro_zh_tw"`
	}
	if err := db.Raw(`SELECT w.id AS work_id, g.intro_ja_jp, g.intro_en_us, g.intro_zh_cn, g.intro_zh_tw
		FROM catalog_work w JOIN galgame g ON g.id = w.product_work_id
		WHERE w.site = 'galgame_wiki' AND ` + population).Scan(&bridged).Error; err != nil {
		return nil, err
	}
	// The pivot is the read face's, verbatim — same columns, same BCP-47 tags.
	for _, r := range bridged {
		for _, p := range []struct{ lang, text string }{
			{"ja", r.IntroJaJP}, {"en", r.IntroEnUS},
			{"zh-Hans", r.IntroZhCN}, {"zh-Hant", r.IntroZhTW},
		} {
			if strings.TrimSpace(p.text) != "" {
				out[r.WorkID] = append(out[r.WorkID], workIntro{lang: p.lang, text: p.text})
			}
		}
	}

	// ── bodyless: the native rows, first-per-language wins ──
	var native []struct {
		WorkID int64  `gorm:"column:work_id"`
		Lang   string `gorm:"column:lang"`
		Intro  string `gorm:"column:intro"`
	}
	if err := db.Raw(`SELECT i.work_id, i.lang, i.intro
		FROM catalog_work_intro i JOIN catalog_work w ON w.id = i.work_id
		WHERE coalesce(w.site, '') = '' AND ` + population + `
		ORDER BY i.work_id, i.lang, i.provenance, i.source_id`).Scan(&native).Error; err != nil {
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
		}
		if err := db.Raw(`SELECT id, display_name, olang, content_rating, coalesce(site,'') AS site,
				product_work_id, claim_state, updated_at
			FROM catalog_work
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
			in := catalogSearch.WorkDocInput{
				ID: r.ID, DisplayName: r.DisplayName, OLang: r.OLang,
				ContentRating: r.ContentRating,
				// claimed == "a product site owns this row", i.e. the works
				// list's `w.site <> ''` (NULL and '' are both bodyless).
				Claimed:     r.Site != "",
				ClaimState:  model.ClaimStateKey(&r.Site, r.ProductWorkID, r.ClaimState),
				ReleasedOrd: facets.releasedOrd[r.ID], UpdatedTS: r.UpdatedAt.Unix(),
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
