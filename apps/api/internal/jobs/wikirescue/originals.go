package wikirescue

import (
	"context"
	"fmt"
	"strings"

	"api/internal/platform/catalog/model"
	"api/internal/platform/catalog/repository"
	"api/internal/platform/catalog/service"

	"gorm.io/gorm"
)

// siteGalgameWiki is the tenant key a wiki-claimed work carries in
// catalog_work.site — NOT the medium key "galgame". Getting it wrong fails
// silently: every lookup would miss and every work would be re-minted.
const siteGalgameWiki = "galgame_wiki"

// mediumGalgame is the catalog_medium id for galgame (seed-pinned).
const mediumGalgame int16 = 1

// wikiOriginal is one user-original galgame — an entry with no VNDB id, so
// nothing upstream can ever recreate it.
type wikiOriginal struct {
	ID            int64
	NameEnUS      string
	NameJaJP      string
	NameZhCN      string
	NameZhTW      string
	CatalogWorkID *int64
}

// titleSpec is one catalog_work_title row to write for an original.
type titleSpec struct {
	lang  string
	title string
}

// langOfWikiName maps the wiki's four fixed name columns onto BCP-47 tags.
// zh-Hant and en are new values for this column (it has only ever held ja /
// zh-Hans / zh / ''), but they are the correct BCP-47 spellings and the column
// is documented as BCP-47 — a narrower choice would lose the distinction
// between the two Chinese scripts the wiki deliberately keeps apart.
func (o wikiOriginal) titles() []titleSpec {
	out := make([]titleSpec, 0, 4)
	add := func(lang, name string) {
		if strings.TrimSpace(name) != "" {
			out = append(out, titleSpec{lang: lang, title: name})
		}
	}
	add("ja", o.NameJaJP)
	add("zh-Hans", o.NameZhCN)
	add("zh-Hant", o.NameZhTW)
	add("en", o.NameEnUS)
	return out
}

// displayNameOf mirrors catalogsync.displayName: Japanese first, then the other
// locales, so a work is never nameless.
func (o wikiOriginal) displayNameOf() string {
	for _, n := range []string{o.NameJaJP, o.NameZhCN, o.NameEnUS, o.NameZhTW} {
		if strings.TrimSpace(n) != "" {
			return n
		}
	}
	return ""
}

// parkedOriginal records a user-original entry this step could not project.
type parkedOriginal struct {
	GalgameID int64  `json:"galgame_id"`
	NameJaJP  string `json:"name_ja_jp"`
	NameZhCN  string `json:"name_zh_cn"`
	NameZhTW  string `json:"name_zh_tw"`
	NameEnUS  string `json:"name_en_us"`
	Reason    string `json:"reason"`
}

// stepOriginals guarantees the 279 user-original entries survive the table
// drop (charter "默认结构件"). These are the only wiki works with NO upstream:
// everything else is VNDB-derived and regenerable.
//
// Scope is vndb_id='' AND status=0 (published). The status 1/4 rows are counted
// and reported but deliberately not projected — they are drafts/deleted.
//
// Claimed wiki works normally carry no catalog_work_title at all (the
// bridge-not-copy design reads titles live from galgame.name_*). That bridge
// dies with the table, so this step MATERIALIZES the titles.
func (r *Runner) stepOriginals(ctx context.Context) (Stats, error) {
	st := Stats{Step: "h"}

	var offScope int
	if err := r.galgame.WithContext(ctx).Raw(
		`SELECT count(*) FROM galgame WHERE vndb_id = '' AND status <> 0`).Scan(&offScope).Error; err != nil {
		return st, fmt.Errorf("count out-of-scope originals: %w", err)
	}

	var originals []wikiOriginal
	if err := r.galgame.WithContext(ctx).Raw(
		`SELECT id, coalesce(name_en_us,'') AS name_en_us, coalesce(name_ja_jp,'') AS name_ja_jp,
		        coalesce(name_zh_cn,'') AS name_zh_cn, coalesce(name_zh_tw,'') AS name_zh_tw,
		        catalog_work_id
		 FROM galgame WHERE vndb_id = '' AND status = 0 ORDER BY id`).Scan(&originals).Error; err != nil {
		return st, fmt.Errorf("read user originals: %w", err)
	}
	st.Source = len(originals)

	// ── phase 1: resolve an anchor for every original ───────────────────────
	titleIndex, err := loadTitleIndex(ctx, r.catalog)
	if err != nil {
		return st, err
	}
	anchored := make(map[int64]int64, len(originals))
	var toMatch, toMint []wikiOriginal
	for _, o := range originals {
		if o.CatalogWorkID != nil && *o.CatalogWorkID != 0 {
			anchored[o.ID] = *o.CatalogWorkID
			continue
		}
		hit, ok := uniqueTitleHit(titleIndex, o)
		if ok {
			anchored[o.ID] = hit
			toMatch = append(toMatch, o)
			continue
		}
		toMint = append(toMint, o)
	}
	st.Anchored = len(anchored) - len(toMatch)
	st.Linked = len(toMatch)
	st.Planned = len(toMint)

	if !r.opts.Apply {
		st.Note = fmt.Sprintf(
			"out_of_scope_status_1_4=%d pre_anchored=%d title_matched=%d to_mint=%d",
			offScope, st.Anchored, len(toMatch), len(toMint))
		return st, nil
	}

	// Write back the anchors recovered by exact title match.
	if len(toMatch) > 0 {
		pairs := make(map[int64]int64, len(toMatch))
		for _, o := range toMatch {
			pairs[o.ID] = anchored[o.ID]
		}
		if err := r.writeBackAnchors(ctx, pairs); err != nil {
			return st, err
		}
	}

	// ── phase 2: mint the works that have no counterpart at all ─────────────
	works := service.NewWorkService(r.catalog, service.NewResolveService(repository.NewRedirectRepository(r.catalog)))
	parked := make([]parkedOriginal, 0)
	for _, o := range toMint {
		name := o.displayNameOf()
		if name == "" {
			parked = append(parked, parkedOriginal{
				GalgameID: o.ID, Reason: "entry has no name in any of the four languages",
			})
			continue
		}
		// ClaimWork, never a raw insert: it is idempotent, cannot mint a second
		// identity for an already-claimed product work, and writes the created
		// revision. No anchors — a user original has no external id by definition.
		workID, created, err := works.ClaimWork(ctx, service.ClaimWorkParams{
			MediumID:      mediumGalgame,
			Site:          siteGalgameWiki,
			ProductWorkID: o.ID,
			DisplayName:   name,
			OLang:         "ja",
			// all_ages is never inferred (doc 17 §6) — the same call the
			// reconcile lane makes for every other wiki work.
			ContentRating: model.ContentRatingAllAges,
		})
		if err != nil {
			return st, fmt.Errorf("claim work for galgame %d: %w", o.ID, err)
		}
		if created {
			st.Created++
		}
		anchored[o.ID] = workID
		if err := r.writeBackAnchors(ctx, map[int64]int64{o.ID: workID}); err != nil {
			return st, err
		}
		st.Linked++
	}
	st.Parked = len(parked)
	if err := r.park("h-originals", parked); err != nil {
		return st, err
	}

	// ── phase 3: materialize titles for every anchored original ─────────────
	rows := make([][]any, 0, len(originals)*3)
	for _, o := range originals {
		workID, ok := anchored[o.ID]
		if !ok {
			continue
		}
		specs := o.titles()
		for i, s := range specs {
			// The title in the work's original language is the OFFICIAL one
			// (OLang decides which row is primary — the VNDB model); the rest are
			// aliases. With no ja name the first available takes the official slot
			// so the work always has one.
			kind := model.WorkTitleKindAlias
			if s.lang == "ja" || (i == 0 && specs[0].lang != "ja") {
				kind = model.WorkTitleKindOfficial
			}
			rows = append(rows, []any{workID, s.lang, s.title, kind})
		}
	}
	// catalog_work_title carries no timestamp columns (title rows are facts
	// about the work, versioned through catalog_revision).
	landed, err := insertReturning(r.catalog.WithContext(ctx), "catalog_work_title",
		[]string{"work_id", "lang", "title", "kind"}, "work_id", rows)
	if err != nil {
		return st, err
	}
	st.Written = len(landed)
	if len(landed) > 0 {
		if err := repository.TouchWorks(ctx, r.catalog, landed); err != nil {
			return st, fmt.Errorf("touch works: %w", err)
		}
		st.Touched = distinctCount(landed)
	}
	st.Note = fmt.Sprintf("out_of_scope_status_1_4=%d pre_anchored=%d title_matched=%d minted=%d titles_planned=%d",
		offScope, st.Anchored, len(toMatch), st.Created, len(rows))
	return st, nil
}

// writeBackAnchors stamps galgame.catalog_work_id, only where it is NULL or
// differs, mirroring catalogsync.writeBackWorkIDs (the column's owning lane).
func (r *Runner) writeBackAnchors(ctx context.Context, pairs map[int64]int64) error {
	if len(pairs) == 0 {
		return nil
	}
	var sb strings.Builder
	sb.WriteString("UPDATE galgame AS g SET catalog_work_id = v.wid FROM (VALUES ")
	args := make([]any, 0, len(pairs)*2)
	i := 0
	for gid, wid := range pairs {
		if i > 0 {
			sb.WriteString(",")
		}
		if i == 0 {
			sb.WriteString("(?::bigint,?::bigint)")
		} else {
			sb.WriteString("(?,?)")
		}
		args = append(args, gid, wid)
		i++
	}
	sb.WriteString(") AS v(gid, wid) WHERE g.id = v.gid AND (g.catalog_work_id IS NULL OR g.catalog_work_id <> v.wid)")
	if err := r.galgame.WithContext(ctx).Exec(sb.String(), args...).Error; err != nil {
		return fmt.Errorf("write back catalog_work_id: %w", err)
	}
	return nil
}

// loadTitleIndex maps an exact title string to the works carrying it. Titles
// shared by several works are useless as an anchor, so they are kept with all
// their work ids and rejected at lookup time.
func loadTitleIndex(ctx context.Context, db *gorm.DB) (map[string][]int64, error) {
	type row struct {
		WorkID int64
		Title  string
	}
	var rows []row
	if err := db.WithContext(ctx).Raw(
		`SELECT work_id, title FROM catalog_work_title WHERE title <> ''`).Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("load catalog_work_title: %w", err)
	}
	idx := make(map[string][]int64, len(rows))
	for _, x := range rows {
		idx[x.Title] = appendUniqueID(idx[x.Title], x.WorkID)
	}
	return idx, nil
}

// uniqueTitleHit resolves an original by exact title equality across its four
// names. It only accepts a hit when EVERY matching name points at the same
// single work — an ambiguous match is no match.
func uniqueTitleHit(idx map[string][]int64, o wikiOriginal) (int64, bool) {
	var found int64
	for _, n := range []string{o.NameJaJP, o.NameZhCN, o.NameZhTW, o.NameEnUS} {
		if strings.TrimSpace(n) == "" {
			continue
		}
		ids := idx[n]
		if len(ids) == 0 {
			continue
		}
		if len(ids) > 1 {
			return 0, false
		}
		if found != 0 && found != ids[0] {
			return 0, false
		}
		found = ids[0]
	}
	return found, found != 0
}

func appendUniqueID(ids []int64, id int64) []int64 {
	for _, x := range ids {
		if x == id {
			return ids
		}
	}
	return append(ids, id)
}
