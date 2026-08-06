package entityintromt

import (
	"context"
	"strings"

	"gorm.io/gorm"
)

// SourceLang is the language a candidate is translated FROM.
type SourceLang string

const (
	SourceJa SourceLang = "ja"
	SourceEn SourceLang = "en"
)

// candidate is one entity eligible for →zh-Hans MT.
//
//   - SrcLang / SourceID / Text: the CHOSEN source row — ja preferred (lowest
//     source_id), en fallback (see the package doc). The machine row is
//     attributed to that source_id and its src_hash is sha256(Text).
//   - MZhID / MZhSrcHash: the entity's EXISTING machine zh-Hans row, if any.
//     Present → idempotence / re-translate decision; absent → a fresh insert.
type candidate struct {
	EntityID   int64   `gorm:"column:entity_id"`
	SrcLang    string  `gorm:"column:src_lang"`
	SourceID   int16   `gorm:"column:source_id"`
	Text       string  `gorm:"column:src_text"`
	MZhID      *int64  `gorm:"column:mzh_id"`
	MZhSrcHash *string `gorm:"column:mzh_src_hash"`
	// Gloss is the entity's term list, loaded in bulk after the candidate query
	// (glossary.go) — never scanned from it.
	Gloss Glossary `gorm:"-"`
}

// loadCandidates resolves one lane's candidate set:
//
//	live entities (deleted_at IS NULL)
//	  → the chosen source row: DISTINCT ON entity, ja rows before en rows
//	    ((lang <> 'ja') ASC), then lowest source_id — the same row precedence
//	    the read face surfaces
//	  → has NO zh-Hans/zh-Hant row with provenance=0 (fill-missing)
//	  LEFT JOIN its existing machine zh-Hans row (provenance=1), if any
//
// Ordered by entity id ASC — a total, deterministic order, so Limit/Offset
// windows form stable, resumable batches (the character lane runs nightly in
// slices). The table/column names are interpolated from the package-internal
// lane table, never from input.
func loadCandidates(ctx context.Context, db *gorm.DB, lane laneDef, limit, offset int) ([]candidate, error) {
	t, id := lane.introTable, lane.idCol
	q := `
		WITH src AS (
			SELECT DISTINCT ON (` + id + `) ` + id + ` AS entity_id, lang, source_id, intro
			FROM ` + t + `
			WHERE lang IN ('ja','en') AND provenance = 0
			ORDER BY ` + id + `, (lang <> 'ja'), source_id
		),
		has_zh_source AS (
			SELECT DISTINCT ` + id + ` AS entity_id FROM ` + t + `
			WHERE lang IN ('zh-Hans','zh-Hant') AND provenance = 0
		),
		mzh AS (
			SELECT DISTINCT ON (` + id + `) ` + id + ` AS entity_id, id AS mzh_id, src_hash AS mzh_src_hash
			FROM ` + t + ` WHERE lang = 'zh-Hans' AND provenance = 1
			ORDER BY ` + id + `, source_id
		)
		SELECT src.entity_id, src.lang AS src_lang, src.source_id, src.intro AS src_text,
			mzh.mzh_id, mzh.mzh_src_hash
		FROM src
		JOIN ` + lane.entityTable + ` e ON e.id = src.entity_id AND e.deleted_at IS NULL
		LEFT JOIN has_zh_source hz ON hz.entity_id = src.entity_id
		LEFT JOIN mzh ON mzh.entity_id = src.entity_id
		WHERE hz.entity_id IS NULL
		ORDER BY src.entity_id ASC`
	args := []any{}
	if limit > 0 {
		q += ` LIMIT ?`
		args = append(args, limit)
	}
	if offset > 0 {
		q += ` OFFSET ?`
		args = append(args, offset)
	}
	var out []candidate
	if err := db.WithContext(ctx).Raw(q, args...).Scan(&out).Error; err != nil {
		return nil, err
	}
	// Whitespace-only source text would hash and "translate" to garbage; drop
	// it here so dry and apply agree (source rows are NOT NULL but nothing
	// upstream pins them non-blank).
	kept := out[:0]
	for _, c := range out {
		if strings.TrimSpace(c.Text) != "" {
			kept = append(kept, c)
		}
	}
	return kept, nil
}
