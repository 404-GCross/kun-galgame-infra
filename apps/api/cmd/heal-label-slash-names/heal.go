package main

import (
	"fmt"
	"io"
	"strings"

	"api/internal/platform/catalog/model"

	"gorm.io/gorm"
)

// healCase is one adjudicated label: the id, the display_name the human
// actually looked at (the drift guard's pin), and the canonical name to keep.
//
// Canonical is NOT required to be one of the slash segments. Label 11646 is the
// standing counter-example: the row reads 「モニスタラッシュ / a Matures」 and the
// adjudicated name is 「ア・マチュアズ」, the katakana rendering of the brand — a
// name the row never carried. Both segments therefore become aliases.
type healCase struct {
	LabelID int64
	// Expect is the FULL current display_name, matched exactly. Nothing weaker
	// is safe: "starts with the canonical" would happily heal a row that had
	// since gained or lost a brand, i.e. a row nobody adjudicated.
	Expect    string
	Canonical string
}

// healCases is the adjudicated table, verbatim from the refs/proj/175 human
// review of the 127 slash-named labels. Eight entries, no rule, no wildcard:
// the other 119 slash names were reviewed and deliberately left alone.
var healCases = []healCase{
	{4086, "POISON / POISON MOTION / POISON EXTASY", "POISON"},
	{5960, "インターハート / Candy Soft / ぐみそふと / はちみつそふと / REAL / DarknessPot / 娘。 / しばそふと / DESSERT Soft / カカオ / ういろうそふと / ましゅまろそふと", "インターハート"},
	{8791, "CHAOS-R / CHAOS-L / CHAOS-R EXTREME /CHAOS-R Re:Master / CHAOS-R feat. Freak Strike / FREAK STRIKE / CHAOS-R LIS-UP / MAYHEM", "CHAOS-R"},
	{9133, "ココロリウム / ア・ラ・フィリア", "ココロリウム"},
	{9522, "Omega Program / 正経同人", "Omega Program"},
	{10145, "ぱちぱちそふと / ぱちぱちそふと黒", "ぱちぱちそふと"},
	{11646, "モニスタラッシュ / a Matures", "ア・マチュアズ"},
	{11786, "ピンポイント / キングピン / ピンポイントクイック", "ピンポイント"},
}

// aliasRow is one planned catalog_label_alias insert.
type aliasRow struct {
	Name string
	Kind int16
}

// caseResult reports what applyCase did with one case.
type caseResult struct {
	skipped bool
	reason  string
	aliases []aliasRow
}

// planAliases derives the alias rows that preserve everything the monster
// string used to make findable.
//
// The full original goes in as a SEARCH HINT (kind=2, never displayed): it is
// not a name of anything, only the string someone may have bookmarked or
// pasted. Each '/'-separated segment goes in as a SPELLING VARIANT (kind=1) —
// those ARE names, of the sibling brands the maker page fronts — except the
// canonical itself, which is the display_name and must not be its own alias.
//
// Segments are whitespace-trimmed (label 8791 carries a "EXTREME /CHAOS-R"
// spelling with no space after the slash) and de-duplicated in encounter order,
// so the plan is stable and the DB-side ON CONFLICT is a backstop rather than
// the deduplication.
func planAliases(original, canonical string) []aliasRow {
	rows := []aliasRow{{Name: original, Kind: model.AliasKindSearchHint}}
	seen := map[string]bool{original: true, canonical: true}
	for _, seg := range strings.Split(original, "/") {
		seg = strings.TrimSpace(seg)
		if seg == "" || seen[seg] {
			continue
		}
		seen[seg] = true
		rows = append(rows, aliasRow{Name: seg, Kind: model.AliasKindSpellingVariant})
	}
	return rows
}

// updateSQL renames the label and stamps the rename's provenance.
//
// field_provenance follows the R8 array convention every catalog writer uses
// ({"<field>":[{"source","at"}, ...]}, latest writer first) — the same shape the
// vndb importer already writes under "display_name" on 56 labels and the logo
// lane writes under "logo_hash" on 4,835. The source key is "curated"
// (catalog_source id 12), which is what a human adjudication is; the entry is
// PREPENDED so the importer's original claim survives underneath it as history.
//
// The WHERE re-asserts the guarded display_name, so a concurrent writer between
// the guard SELECT and this UPDATE loses the race instead of being overwritten.
const updateSQL = `
UPDATE catalog_label
   SET display_name = ?,
       field_provenance = jsonb_set(field_provenance, '{display_name}',
           jsonb_build_array(jsonb_build_object(
               'source', 'curated',
               'at', to_char(now() AT TIME ZONE 'utc', 'YYYY-MM-DD"T"HH24:MI:SS"Z"')))
           || COALESCE(field_provenance->'display_name', '[]'::jsonb)),
       updated_at = now()
 WHERE id = ? AND display_name = ? AND deleted_at IS NULL`

// insertAliasSQL adds one alias, idempotently. lang is '' throughout: the
// segments are brand names in whatever script the maker page used, and this
// tool has no basis to assert a locale for them — a wrong lang would be a
// worse claim than none. is_primary_for_locale is never set.
//
// source_id is left NULL for exactly the same reason: the segment is carved out
// of a display_name this tool did not import, so which upstream supplied it is
// not knowable here. provenance is stated explicitly because wave 195 made the
// column NOT NULL with no default — a heal is a re-filing of a name a source
// wrote, never a machine translation of one.
const insertAliasSQL = `
INSERT INTO catalog_label_alias (label_id, name, lang, kind, is_primary_for_locale, provenance)
VALUES (?, ?, '', ?, false, 0)
ON CONFLICT (label_id, name, lang) DO NOTHING`

// applyCase runs the drift guard and, unless this is a dry run, the writes for
// one case in a single transaction. A skip is never an error: the whole point
// is that the tool keeps going and reports which rows moved out from under the
// adjudication.
func applyCase(db *gorm.DB, c healCase, apply bool, w io.Writer) (caseResult, error) {
	var current []string
	if err := db.Raw(`SELECT display_name FROM catalog_label WHERE id = ? AND deleted_at IS NULL`,
		c.LabelID).Scan(&current).Error; err != nil {
		return caseResult{}, err
	}
	if len(current) == 0 {
		return skip(w, c, "label not found or soft-deleted"), nil
	}
	got := current[0]
	if !strings.Contains(got, "/") {
		return skip(w, c, fmt.Sprintf("display_name carries no slash any more (%q) — already healed, or healed by someone else", got)), nil
	}
	if got != c.Expect {
		return skip(w, c, fmt.Sprintf("display_name drifted since adjudication\n      adjudicated: %q\n      live:        %q", c.Expect, got)), nil
	}

	aliases := planAliases(got, c.Canonical)
	fmt.Fprintf(w, "[heal] label %d\n  %q\n  -> %q\n", c.LabelID, got, c.Canonical)
	for _, a := range aliases {
		fmt.Fprintf(w, "  + alias kind=%d %q\n", a.Kind, a.Name)
	}
	if !apply {
		return caseResult{aliases: aliases}, nil
	}

	err := db.Transaction(func(tx *gorm.DB) error {
		res := tx.Exec(updateSQL, c.Canonical, c.LabelID, c.Expect)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected != 1 {
			return fmt.Errorf("rename matched %d rows, want 1 (the row changed under us)", res.RowsAffected)
		}
		for _, a := range aliases {
			if err := tx.Exec(insertAliasSQL, c.LabelID, a.Name, a.Kind).Error; err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return caseResult{}, err
	}
	return caseResult{aliases: aliases}, nil
}

// skip reports a guard refusal loudly — it is the outcome an operator most
// needs to see, so it never hides among the healed rows.
func skip(w io.Writer, c healCase, reason string) caseResult {
	fmt.Fprintf(w, "[SKIP] label %d: %s\n", c.LabelID, reason)
	return caseResult{skipped: true, reason: reason}
}
