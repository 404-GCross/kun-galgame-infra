package importer

// Loaders for the Bangumi type-4 gated expansion (refs/proj/78): the pool query
// with the grounded meta_tags gate predicates, the existing-work-title collision
// index, and the unified cross-source (eg / dlsite-game / VNDB-ja) title corpus.

import (
	"fmt"

	"api/internal/platform/catalog/model"

	"gorm.io/gorm"
)

// buildBgmGatedPoolQuery builds the pool query with the grounded meta_tags gate
// predicates inlined as jsonb_exists / jsonb_exists_any (operator-free — GORM
// treats the jsonb `?` operator as a bind placeholder). Returns every unanchored
// type-4 subject with its SQL-decided sig_p / sig_t / console-mobile-excluded.
func buildBgmGatedPoolQuery() string {
	metaArr := `jsonb_typeof(s.meta_tags)='array'`
	exAny := func(arr string) string { return `jsonb_exists_any(s.meta_tags, ` + arr + `)` }
	ex := func(k string) string { return `jsonb_exists(s.meta_tags, '` + k + `')` }
	sigP := metaArr + ` AND ` + exAny(sqlPCPlatforms) + ` AND (` +
		exAny(sqlGenreStrict) + ` OR (` + exAny(sqlGenreAdv) + ` AND ` + exAny(sqlAgeRated) + `))`
	sigT := `(` + metaArr + ` AND (` + ex(`Galgame`) + ` OR ` + ex(`galgame`) + `))` +
		` OR EXISTS(SELECT 1 FROM jsonb_array_elements(s.tags) tg WHERE jsonb_typeof(s.tags)='array'` +
		` AND lower(tg->>'name') = ANY(` + sqlGalgameFolk + `) AND coalesce((tg->>'count')::int,0) >= 3)`
	excluded := metaArr + ` AND ` + exAny(sqlConsoleMobile) + ` AND NOT ` + exAny(sqlPCFamily)
	return `SELECT s.id AS id, coalesce(s.name,'') AS name, coalesce(s.name_cn,'') AS name_cn,
			coalesce(s.name_norm,'') AS name_norm, coalesce(s.name_cn_norm,'') AS name_cn_norm, s.nsfw AS nsfw,
			(` + sigP + `) AS sig_p, (` + sigT + `) AS sig_t, (` + excluded + `) AS excluded
		FROM src_bangumi.subject s
		WHERE s.type = 4
			AND NOT EXISTS (SELECT 1 FROM catalog_external_ref e
				WHERE e.source_id = ? AND e.entity_type = ? AND e.external_id = s.id::text)
		ORDER BY s.id`
}

// loadGatedPool returns every unanchored type-4 subject with its SQL flags.
func (im *Importer) loadGatedPool() ([]poolRow, error) {
	var rows []poolRow
	err := im.catalog.Raw(buildBgmGatedPoolQuery(), bangumiSource, model.EntityTypeWork).Scan(&rows).Error
	return rows, err
}

// loadExistingWorkTitleNorms indexes EVERY existing work title (norm ≥4) →
// (work id, title) for the collision safety rope.
func (im *Importer) loadExistingWorkTitleNorms() (map[string]wtNorm, error) {
	var rows []struct {
		Norm   string `gorm:"column:title_norm"`
		WorkID int64  `gorm:"column:work_id"`
		Title  string `gorm:"column:title"`
	}
	if err := im.catalog.Raw(
		`SELECT title_norm, work_id, title FROM catalog_work_title WHERE length(title_norm) >= ?`,
		bgmGatedMinLen,
	).Scan(&rows).Error; err != nil {
		return nil, err
	}
	out := make(map[string]wtNorm, len(rows))
	for _, r := range rows {
		if _, ok := out[r.Norm]; !ok { // first work wins — the sample only needs one target
			out[r.Norm] = wtNorm{workID: r.WorkID, title: r.Title}
		}
	}
	return out, nil
}

// loadCrossSourceNorms builds the unified NFKC-lower cross-source title set from
// the three Japanese galgame/VN corpora: erogamespace game names, DLsite GAME
// titles, and VNDB Japanese release titles (+ their romaji). All norms are
// computed IN-SQL with the identical lower(normalize(col,NFKC)) fold, so equality
// with the Bangumi name_norm generated column is byte-isomorphic.
func (im *Importer) loadCrossSourceNorms(dlsiteDB *gorm.DB) (map[string]bool, error) {
	set := make(map[string]bool, 300000)
	collect := func(db *gorm.DB, query string, args ...any) error {
		var norms []string
		if err := db.Raw(query, args...).Scan(&norms).Error; err != nil {
			return err
		}
		for _, n := range norms {
			if runeLen(n) >= bgmGatedMinLen {
				set[n] = true
			}
		}
		return nil
	}
	// erogamespace: pure Japanese PC eroge corpus.
	if err := collect(im.eg,
		`SELECT DISTINCT lower(normalize(gamename, NFKC)) FROM games WHERE gamename IS NOT NULL AND gamename <> ''`,
	); err != nil {
		return nil, fmt.Errorf("eg corpus: %w", err)
	}
	// DLsite: GAME work types only (excludes manga/CG/voice/etc).
	if err := collect(dlsiteDB,
		`SELECT DISTINCT lower(normalize(work_name, NFKC)) FROM works
			WHERE status = 'fetched' AND work_name IS NOT NULL AND work_type_string = ANY(`+sqlDLsiteGameTypes()+`)`,
	); err != nil {
		return nil, fmt.Errorf("dlsite corpus: %w", err)
	}
	// VNDB: Japanese-original release titles + their romaji (drops en/ru/etc,
	// whose generic titles collide with non-galgame subjects).
	if err := collect(im.catalog,
		`SELECT DISTINCT lower(normalize(title, NFKC)) FROM src_vndb.releases_titles WHERE lang = 'ja' AND title IS NOT NULL
			UNION SELECT DISTINCT lower(normalize(latin, NFKC)) FROM src_vndb.releases_titles WHERE lang = 'ja' AND latin IS NOT NULL AND latin <> ''`,
	); err != nil {
		return nil, fmt.Errorf("vndb corpus: %w", err)
	}
	return set, nil
}

// sqlDLsiteGameTypes renders the DLsite game-type allowlist as a SQL text[]
// literal (a fixed, injection-free vocabulary).
func sqlDLsiteGameTypes() string {
	out := "array["
	for i, t := range dlsiteGameTypes {
		if i > 0 {
			out += ","
		}
		out += "'" + t + "'"
	}
	return out + "]"
}
