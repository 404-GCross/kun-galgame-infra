package main

import (
	"fmt"
	"io"
	"strings"

	"api/internal/platform/catalog/model"

	"gorm.io/gorm"
)

// Leg A — ingest source-side aliases of ALREADY-IMPORTED entities as
// kind=search_hint rows (search-only, never displayed → the classification of
// variant vs pen-name is deliberately dodged). Idempotent on the alias table's
// (owner, name, lang) unique key. Scope (doc-enumerated): bangumi persons →
// credit names, bangumi persons(type=3) → labels, bangumi characters, and EG
// credit names' name-parenthetical aliases.

type hintStats struct {
	BangumiNames  int
	BangumiLabels int
	BangumiChars  int
	EGNames       int
	SkippedSame   int // alias folds to the main name — no information
	SkippedRole   int // pure role annotation (声優/歌手/…)
	Already       int // already present (idempotent re-run)
}

// aliasHint is one search-hint row to write.
type aliasHint struct {
	owner int64
	name  string // stored NFKC form, whitespace preserved
}

// bgmRow is one (owner, main-name, alias) triple pulled from a bangumi infobox.
type bgmRow struct {
	Owner    int64  `gorm:"column:owner"`
	MainNFKC string `gorm:"column:main"`
	AliasNF  string `gorm:"column:alias"`
}

func runHints(db *gorm.DB, w io.Writer, apply bool) (hintStats, error) {
	var st hintStats

	// The bangumi buckets: same 别名-Items shape, different catalog entity.
	buckets := []struct {
		label    string
		srcTable string // src_bangumi.<srcTable>
		entTable string // catalog entity table (for the main-name join)
		entType  int16
		rule     string
		aliasTbl string
		ownerCol string
		counter  *int
	}{
		{"bangumi credit_names", "person", "catalog_credit_name", model.EntityTypeCreditName, "rule:bangumi-person-import", "catalog_name_alias", "credit_name_id", &st.BangumiNames},
		{"bangumi labels", "person", "catalog_label", model.EntityTypeLabel, "rule:bangumi-person-import", "catalog_label_alias", "label_id", &st.BangumiLabels},
		{"bangumi characters", "character", "catalog_character", model.EntityTypeCharacter, "rule:bangumi-character-import", "catalog_character_alias", "character_id", &st.BangumiChars},
	}
	for _, b := range buckets {
		nameCol := "display_name"
		if b.entType == model.EntityTypeCreditName {
			nameCol = "name"
		}
		var rows []bgmRow
		q := fmt.Sprintf(`
			SELECT r.entity_id AS owner,
			       normalize(e.%s, NFKC) AS main,
			       normalize(it->>'Value', NFKC) AS alias
			FROM catalog_external_ref r
			JOIN src_bangumi.%s s ON s.id = r.external_id::bigint
			JOIN %s e ON e.id = r.entity_id
			CROSS JOIN LATERAL jsonb_array_elements(s.infobox_parsed->'Fields') f
			CROSS JOIN LATERAL jsonb_array_elements(f->'Items') it
			WHERE r.entity_type = ? AND r.source_id = ? AND r.matched_by = ?
			  AND s.parse_error = '' AND jsonb_typeof(s.infobox_parsed->'Fields') = 'array'
			  AND (f->>'Key') = '别名' AND jsonb_typeof(f->'Items') = 'array'
			  AND coalesce(trim(it->>'Value'), '') <> ''`, nameCol, b.srcTable, b.entTable)
		if err := db.Raw(q, b.entType, sourceBangumi, b.rule).Scan(&rows).Error; err != nil {
			return st, fmt.Errorf("%s scan: %w", b.label, err)
		}
		hints := make([]aliasHint, 0, len(rows))
		for _, r := range rows {
			if h, ok := hintOf(r.Owner, r.AliasNF, r.MainNFKC, &st); ok {
				hints = append(hints, h)
			}
		}
		written, already, err := upsertHints(db, b.aliasTbl, b.ownerCol, hints, apply)
		if err != nil {
			return st, fmt.Errorf("%s upsert: %w", b.label, err)
		}
		*b.counter = written
		st.Already += already
		fmt.Fprintf(w, "leg A · %-20s candidates=%d written=%d already=%d\n", b.label, len(hints), written, already)
	}

	// EG credit names: aliases live in the name's own parentheses.
	var egRows []struct {
		Owner int64  `gorm:"column:owner"`
		Name  string `gorm:"column:name"`
	}
	if err := db.Raw(`SELECT cn.id AS owner, normalize(cn.name, NFKC) AS name
		FROM catalog_credit_name cn
		JOIN catalog_external_ref r ON r.entity_type = ? AND r.entity_id = cn.id
		     AND r.source_id = ? AND r.matched_by = 'rule:eg-creater-import'
		WHERE cn.name ~ '[（(]'`, model.EntityTypeCreditName, sourceEG).Scan(&egRows).Error; err != nil {
		return st, fmt.Errorf("eg names scan: %w", err)
	}
	var egHints []aliasHint
	for _, r := range egRows {
		for _, raw := range parenItemsRaw(r.Name) {
			// r.Name is the full "base(alias)" form; hintOf drops any paren item
			// that folds back to the base.
			if h, ok := hintOf(r.Owner, raw, r.Name, &st); ok {
				egHints = append(egHints, h)
			}
		}
	}
	written, already, err := upsertHints(db, "catalog_name_alias", "credit_name_id", egHints, apply)
	if err != nil {
		return st, fmt.Errorf("eg names upsert: %w", err)
	}
	st.EGNames = written
	st.Already += already
	fmt.Fprintf(w, "leg A · %-20s candidates=%d written=%d already=%d\n", "eg credit_names", len(egHints), written, already)

	mode := "DRY-RUN"
	if apply {
		mode = "APPLIED"
	}
	fmt.Fprintf(w, "leg A %s — hints written: bgm_names=%d bgm_labels=%d bgm_chars=%d eg_names=%d | skipped_same=%d skipped_role=%d already=%d\n",
		mode, st.BangumiNames, st.BangumiLabels, st.BangumiChars, st.EGNames, st.SkippedSame, st.SkippedRole, st.Already)
	return st, nil
}

// hintOf validates one alias against the main name and the role filter,
// returning the storable hint (NFKC form, trimmed). mainNFKC "" skips the
// same-fold check (the EG path applies it itself, keeping its own tally).
func hintOf(owner int64, aliasNFKC, mainNFKC string, st *hintStats) (aliasHint, bool) {
	name := strings.TrimSpace(aliasNFKC)
	af := foldName(aliasNFKC)
	if af == "" {
		return aliasHint{}, false
	}
	if isRoleTag(aliasNFKC) {
		st.SkippedRole++
		return aliasHint{}, false
	}
	if mainNFKC != "" && af == foldName(mainNFKC) {
		st.SkippedSame++
		return aliasHint{}, false
	}
	return aliasHint{owner: owner, name: name}, true
}

// upsertHints inserts search-hint rows idempotently on (owner, name, lang=”).
// Dry-run counts the not-yet-present rows without writing.
func upsertHints(db *gorm.DB, table, ownerCol string, hints []aliasHint, apply bool) (written, already int, err error) {
	// De-dup within this batch first (an entity can carry the same alias twice).
	seen := map[string]struct{}{}
	uniq := hints[:0]
	for _, h := range hints {
		k := fmt.Sprintf("%d\x00%s", h.owner, h.name)
		if _, ok := seen[k]; ok {
			continue
		}
		seen[k] = struct{}{}
		uniq = append(uniq, h)
	}

	for _, h := range uniq {
		if !apply {
			var exists bool
			if err = db.Raw(fmt.Sprintf(
				`SELECT EXISTS(SELECT 1 FROM %s WHERE %s = ? AND name = ? AND lang = '')`, table, ownerCol),
				h.owner, h.name).Scan(&exists).Error; err != nil {
				return
			}
			if exists {
				already++
			} else {
				written++
			}
			continue
		}
		res := db.Exec(fmt.Sprintf(
			`INSERT INTO %s (%s, name, lang, kind, is_primary_for_locale)
			 VALUES (?, ?, '', ?, false) ON CONFLICT (%s, name, lang) DO NOTHING`,
			table, ownerCol, ownerCol), h.owner, h.name, model.AliasKindSearchHint)
		if res.Error != nil {
			err = res.Error
			return
		}
		if res.RowsAffected > 0 {
			written++
		} else {
			already++
		}
	}
	return
}
