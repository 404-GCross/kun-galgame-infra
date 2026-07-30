package olangfix

import (
	"context"
	"fmt"

	"api/internal/platform/catalog/model"

	"gorm.io/gorm"
)

// siteGalgameWiki is the claim site of the galgame wiki — lane W's population.
const siteGalgameWiki = "galgame_wiki"

// registry holds the ids this backfill needs, resolved BY KEY (never hardcoded)
// so a rehearsal copy with different auto-increment seeds still works — the
// worktags / dlsitegenres discipline.
type registry struct {
	galgameMedium int16
	vndbSource    int16
}

func resolveRegistry(ctx context.Context, db *gorm.DB) (registry, error) {
	var r registry
	if err := db.WithContext(ctx).Raw(`SELECT id FROM catalog_medium WHERE key = 'galgame'`).Scan(&r.galgameMedium).Error; err != nil {
		return r, fmt.Errorf("resolve galgame medium: %w", err)
	}
	if err := db.WithContext(ctx).Raw(`SELECT id FROM catalog_source WHERE key = 'vndb'`).Scan(&r.vndbSource).Error; err != nil {
		return r, fmt.Errorf("resolve vndb source: %w", err)
	}
	if r.galgameMedium == 0 || r.vndbSource == 0 {
		return r, fmt.Errorf("registry not seeded (galgame medium=%d, vndb source=%d)",
			r.galgameMedium, r.vndbSource)
	}
	return r, nil
}

// candidate is one work whose olang this run may rewrite, plus the raw signal
// its lane decides from. Exactly one of the VN* / Wiki* groups is populated.
type candidate struct {
	WorkID   int64
	Lane     string
	OldOLang string

	VNID    string // lane V: the anchored VNDB id ("v19658")
	VNOLang string // lane V: src_vndb.vn.olang, "" when the row is missing or blank
	VNFound bool   // lane V: the mirror actually has that VN

	WikiLang  string // lane W: raw galgame.original_language
	WikiFound bool   // lane W: the claimed galgame row still exists
}

// source renders what decided this candidate, for samples.
func (c candidate) source() string {
	if c.Lane == laneVNDB {
		return c.VNID
	}
	return c.WikiLang
}

// loadCandidates builds the combined candidate list: lane V first (the VNDB
// authority), then lane W (the wiki remainder). Both are ordered by work id, so
// the combined list is stable across runs and Limit/Offset windows it
// reproducibly.
func loadCandidates(ctx context.Context, db *gorm.DB, reg registry, st *Stats) ([]candidate, error) {
	v, err := loadVNDBCandidates(ctx, db, reg, st)
	if err != nil {
		return nil, fmt.Errorf("load vndb-anchored candidates: %w", err)
	}
	w, err := loadWikiCandidates(ctx, db, reg)
	if err != nil {
		return nil, fmt.Errorf("load wiki-claimed candidates: %w", err)
	}
	st.VNCandidates = len(v)
	st.WikiCandidates = len(w)
	return append(v, w...), nil
}

// loadVNDBCandidates resolves every non-soft-deleted galgame-medium work
// carrying an EXACT VNDB WORK anchor, left-joined to the src_vndb mirror.
//
// The anchor's external_id and src_vndb.vn.id are the same 'v19658' text, so the
// join is direct. One work is expected to carry exactly one such anchor (a
// second one would be an identity collision, not a data shape); rows are ordered
// by (work id, external_id) and folded in Go so the LOWEST id wins and the
// duplicates are COUNTED rather than silently dropped by a DISTINCT ON.
func loadVNDBCandidates(ctx context.Context, db *gorm.DB, reg registry, st *Stats) ([]candidate, error) {
	var rows []struct {
		WorkID   int64  `gorm:"column:work_id"`
		OldOLang string `gorm:"column:old_olang"`
		VNID     string `gorm:"column:vn_id"`
		VNOLang  string `gorm:"column:vn_olang"`
		VNFound  bool   `gorm:"column:vn_found"`
	}
	if err := db.WithContext(ctx).Raw(`
		SELECT w.id AS work_id, w.olang AS old_olang, r.external_id AS vn_id,
		       coalesce(v.olang, '') AS vn_olang, (v.id IS NOT NULL) AS vn_found
		FROM catalog_work w
		JOIN catalog_external_ref r ON r.entity_type = ? AND r.entity_id = w.id
			AND r.source_id = ? AND r.link_kind = ?
		LEFT JOIN src_vndb.vn v ON v.id = r.external_id
		WHERE w.medium_id = ? AND w.deleted_at IS NULL
		ORDER BY w.id, r.external_id`,
		model.EntityTypeWork, reg.vndbSource, model.LinkKindExact, reg.galgameMedium).
		Scan(&rows).Error; err != nil {
		return nil, err
	}

	out := make([]candidate, 0, len(rows))
	var last int64
	for _, r := range rows {
		if len(out) > 0 && r.WorkID == last {
			st.VNMultiAnchor++ // the ORDER BY already kept the lowest id first
			continue
		}
		last = r.WorkID
		out = append(out, candidate{
			WorkID: r.WorkID, Lane: laneVNDB, OldOLang: r.OldOLang,
			VNID: r.VNID, VNOLang: r.VNOLang, VNFound: r.VNFound,
		})
	}
	return out, nil
}

// loadWikiCandidates resolves the wiki-claimed galgame works lane V did not
// cover — the "claimed but no exact VNDB anchor" remainder — left-joined to the
// wiki galgame table (same database) through catalog_work.product_work_id.
//
// The LEFT JOIN is deliberate: a claim whose galgame row has since vanished must
// surface as a counted WikiRowMissing, not disappear from the population.
func loadWikiCandidates(ctx context.Context, db *gorm.DB, reg registry) ([]candidate, error) {
	var rows []struct {
		WorkID    int64  `gorm:"column:work_id"`
		OldOLang  string `gorm:"column:old_olang"`
		WikiLang  string `gorm:"column:wiki_lang"`
		WikiFound bool   `gorm:"column:wiki_found"`
	}
	if err := db.WithContext(ctx).Raw(`
		SELECT w.id AS work_id, w.olang AS old_olang,
		       coalesce(g.original_language, '') AS wiki_lang, (g.id IS NOT NULL) AS wiki_found
		FROM catalog_work w
		LEFT JOIN galgame g ON g.id = w.product_work_id
		WHERE w.site = ? AND w.medium_id = ? AND w.deleted_at IS NULL
		  AND NOT EXISTS (
			SELECT 1 FROM catalog_external_ref r
			WHERE r.entity_type = ? AND r.entity_id = w.id
			  AND r.source_id = ? AND r.link_kind = ?)
		ORDER BY w.id`,
		siteGalgameWiki, reg.galgameMedium,
		model.EntityTypeWork, reg.vndbSource, model.LinkKindExact).
		Scan(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]candidate, 0, len(rows))
	for _, r := range rows {
		out = append(out, candidate{
			WorkID: r.WorkID, Lane: laneWiki, OldOLang: r.OldOLang,
			WikiLang: r.WikiLang, WikiFound: r.WikiFound,
		})
	}
	return out, nil
}

// loadOLangVocabulary reads the distinct olang values the VNDB mirror actually
// publishes. It is used ONLY to warn about pass-through wiki values nobody
// upstream uses (a likely typo like the live 'ck'); olang is an open vocabulary,
// so an unknown value never blocks a write.
func loadOLangVocabulary(ctx context.Context, db *gorm.DB) (map[string]bool, error) {
	var vals []string
	if err := db.WithContext(ctx).
		Raw(`SELECT DISTINCT olang FROM src_vndb.vn WHERE olang <> ''`).
		Scan(&vals).Error; err != nil {
		return nil, err
	}
	out := make(map[string]bool, len(vals)+1)
	for _, v := range vals {
		out[v] = true
	}
	// The deliberate fallback is always legitimate, even against an empty mirror.
	out[model.OLangDefault] = true
	return out, nil
}

// window applies the offset/limit chunking to the candidate list (slicing keeps
// it obviously correct — the dlsitegenres discipline).
func window[T any](in []T, limit, offset int) []T {
	if offset > 0 {
		if offset >= len(in) {
			return nil
		}
		in = in[offset:]
	}
	if limit > 0 && limit < len(in) {
		in = in[:limit]
	}
	return in
}
