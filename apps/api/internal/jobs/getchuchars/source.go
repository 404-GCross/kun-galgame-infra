// Package getchuchars projects the Getchu crawler's character rosters onto
// catalog characters: the profile prose, and the 身長/スリーサイズ/血液型/誕生日
// attributes (refs/proj/167 §9).
//
// WHY THIS FACET. 12,225 characters on published works carry no intro at all,
// and no upstream the catalog already ingests can close that at scale —
// erogamescape has no character prose to give beyond what wave 120 took, and
// DMM has no character entity whatsoever. Getchu carries a structured roster
// for ~84% of the target pages.
//
// MATCHING. Getchu character rows carry no id we could anchor on, so a match is
// made WITHIN one work's roster, which is what makes it safe: the candidate set
// is a handful of characters that already belong to the same work, not the
// 227k-row character table. Three levels, measured against 24,067 crawled
// characters:
//
//	display name          17,299   (72% of anchored)
//	+ alias                  +50
//	+ Getchu's furigana   +1,012   → 18,361 (76%)
//
// The furigana level is worth its code; the alias level is nearly worthless and
// is kept only because it costs one more OR in the same query. Of the rest,
// only 94 fail because the work has no roster at all — the loss is name FORM,
// not missing data.
//
// AMBIGUITY IS A SKIP, NEVER A GUESS. A Getchu name matching two roster
// characters, or two Getchu rows matching one character, drops both: within a
// single work those are the cases where a wrong link is most plausible (twins,
// a family sharing a surname, an alias shared with a relative).
package getchuchars

import (
	"context"
	"fmt"

	"gorm.io/gorm"
)

// Edition is one Getchu roster row that resolved to a catalog character. A
// product's 限定版 / 通常版 / DL版 each publish the same roster, so one
// character routinely has several.
type Edition struct {
	GetchuID string
	Ordinal  int
}

// Candidate is one matched (Getchu character → catalog character) pair.
type Candidate struct {
	CharacterID int64  `gorm:"column:character_id"`
	WorkID      int64  `gorm:"column:work_id"`
	GetchuID    string `gorm:"column:getchu_id"`
	Ordinal     int    `gorm:"column:ordinal"`
	Name        string `gorm:"column:name"`
	Profile     string `gorm:"column:profile"`
	Attrs       []byte `gorm:"column:attrs"`
	MatchedBy   string `gorm:"column:matched_by"`
	// Editions is every roster row that resolved to this character, richest
	// first — GetchuID/Ordinal above are Editions[0].
	//
	// Prose lanes only ever want the richest one. A lane fetching a PER-ROW
	// asset must not: the page pairs an image with the row it sits beside, so
	// the image lives on a specific edition, and the richest edition is not
	// necessarily the one that has it. Picking "the edition that actually
	// carries the thing" belongs in Go, exactly as getchuintros learned for
	// `story` — a SQL DISTINCT ON would silently choose an empty row and report
	// the character as having nothing.
	Editions []Edition
}

// loadRoster reads the catalog side: every character on a work that carries a
// getchu release anchor, with all the name forms a match may key on.
//
// The anchor is the wave-167 exact ref (source=getchu, entity=release), so this
// job inherits that wave's trust chain and needs no matching at the WORK level
// at all.
const rosterSQL = `
SELECT DISTINCT g.external_id AS getchu_id, rel.work_id, wc.character_id,
       lower(normalize(regexp_replace(ch.display_name, '[[:space:]　]', '', 'g'), NFKC)) AS key_name,
       lower(normalize(regexp_replace(coalesce(al.name,''), '[[:space:]　]', '', 'g'), NFKC)) AS key_alias
FROM catalog_external_ref g
JOIN catalog_release rel ON rel.id = g.entity_id AND rel.deleted_at IS NULL
JOIN catalog_work w ON w.id = rel.work_id AND w.deleted_at IS NULL
JOIN catalog_work_character wc ON wc.work_id = rel.work_id
JOIN catalog_character ch ON ch.id = wc.character_id AND ch.deleted_at IS NULL
LEFT JOIN catalog_character_alias al ON al.character_id = ch.id
WHERE g.entity_type = ? AND g.source_id = ? AND g.link_kind = ?`

// rosterRow is one (work, character, name-form) tuple.
type rosterRow struct {
	GetchuID    string `gorm:"column:getchu_id"`
	WorkID      int64  `gorm:"column:work_id"`
	CharacterID int64  `gorm:"column:character_id"`
	KeyName     string `gorm:"column:key_name"`
	KeyAlias    string `gorm:"column:key_alias"`
}

// getchuChar is one crawled roster entry, read from the staging database.
type getchuChar struct {
	GetchuID string `gorm:"column:getchu_id"`
	Ordinal  int    `gorm:"column:ordinal"`
	Name     string `gorm:"column:name"`
	Reading  string `gorm:"column:reading"`
	CV       string `gorm:"column:cv"`
	Profile  string `gorm:"column:profile"`
	Attrs    []byte `gorm:"column:attrs"`
}

func loadGetchuChars(ctx context.Context, gdb *gorm.DB) ([]getchuChar, error) {
	var out []getchuChar
	err := gdb.WithContext(ctx).Raw(`
		SELECT getchu_id, ordinal, name, coalesce(reading,'') AS reading,
		       coalesce(cv,'') AS cv, coalesce(profile,'') AS profile, attrs
		FROM item_characters
		WHERE btrim(name) <> ''
		ORDER BY getchu_id, ordinal`).Scan(&out).Error
	if err != nil {
		return nil, fmt.Errorf("read staging item_characters: %w", err)
	}
	return out, nil
}

func loadRoster(ctx context.Context, db *gorm.DB, getchuSource int16) ([]rosterRow, error) {
	var out []rosterRow
	err := db.WithContext(ctx).Raw(rosterSQL, entityTypeRelease, getchuSource, linkKindExact).Scan(&out).Error
	if err != nil {
		return nil, fmt.Errorf("load roster: %w", err)
	}
	return out, nil
}
