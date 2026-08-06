package main

import (
	"context"
	"sort"

	"gorm.io/gorm"
)

// Provenance values written to catalog_character_trait.name_zh_provenance.
const (
	provenanceCurated = int16(0) // community dictionary / human
	provenanceMachine = int16(1) // LLM
)

// traitRow is the vocabulary row the planner reasons about.
type traitRow struct {
	ID         int64  `gorm:"column:id"`
	VndbTID    string `gorm:"column:vndb_tid"`
	Name       string `gorm:"column:name"`
	NameZh     string `gorm:"column:name_zh"`
	Provenance int16  `gorm:"column:name_zh_provenance"`
	GroupTID   string `gorm:"column:group_tid"`
}

// decision is what the planner concluded for one (trait, proposed zh) match.
type decision int

const (
	decWrite    decision = iota // name_zh is '' or machine-made — fill/upgrade it
	decSame                     // already exactly this value — nothing to do
	decConflict                 // a CURATED value is already there and differs
)

// plannedWrite is one trait the run would touch.
type plannedWrite struct {
	Trait    traitRow
	Zh       string
	Decision decision
}

// plan matches proposed renderings against the vocabulary table and decides,
// per trait, whether the write is allowed.
//
// The guard is the whole point of the tool: CURATED BEATS MACHINE, and curated
// never silently loses to anything. A row whose name_zh is empty, or whose
// name_zh was produced by the machine lane, may be written; a row that already
// carries a human/dictionary rendering is only ever confirmed (identical) or
// reported as a conflict for a human to settle. Nothing here can turn a curated
// name into a different one.
//
// Matching is by trait NAME, so one proposal can legitimately land on several
// rows: catalog_character_trait holds 3,327 rows under 3,094 distinct names
// (VNDB reuses a name across groups, e.g. the same colour under Hair and Eyes).
// Every row sharing the name gets the same rendering — the group they hang
// under is what disambiguates them on the read face.
func plan(proposals []pair, rows []traitRow, prov int16) []plannedWrite {
	byName := map[string]string{}
	for _, p := range proposals {
		if _, ok := byName[p.En]; !ok {
			byName[p.En] = p.Zh
		}
	}
	var out []plannedWrite
	for _, r := range rows {
		zh, ok := byName[r.Name]
		if !ok {
			continue
		}
		out = append(out, plannedWrite{Trait: r, Zh: zh, Decision: decide(r, zh, prov)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Trait.ID < out[j].Trait.ID })
	return out
}

func decide(r traitRow, zh string, prov int16) decision {
	switch {
	case r.NameZh == zh && r.Provenance == prov:
		return decSame
	case r.NameZh == "":
		return decWrite
	case r.Provenance == provenanceMachine:
		// A machine guess yields to curated input, and to a re-run of the
		// machine lane itself (the CSV is the reviewed artefact either way).
		return decWrite
	case r.NameZh == zh:
		// Same text, curated already — a machine re-proposal must not downgrade
		// the provenance, so leave it alone.
		return decSame
	default:
		return decConflict
	}
}

// loadTraits reads the vocabulary rows the run needs (all of them — 3.3k rows
// is one small query, and the match set is only known after the join).
func loadTraits(ctx context.Context, db *gorm.DB) ([]traitRow, error) {
	var rows []traitRow
	err := db.WithContext(ctx).Raw(
		`SELECT id, vndb_tid, name, name_zh, name_zh_provenance, group_tid
		   FROM catalog_character_trait ORDER BY id`).Scan(&rows).Error
	return rows, err
}

// applyWrites persists the decWrite rows, one UPDATE per trait inside a single
// transaction. The WHERE clause repeats the guard in SQL so a concurrent writer
// that curated a row between the plan and the write cannot be overwritten
// either — the planner's decision is an optimistic read, this is the fence.
func applyWrites(ctx context.Context, db *gorm.DB, writes []plannedWrite, prov int16) (int, error) {
	written := 0
	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for _, w := range writes {
			if w.Decision != decWrite {
				continue
			}
			res := tx.Exec(`UPDATE catalog_character_trait
				   SET name_zh = ?, name_zh_provenance = ?, updated_at = now()
				 WHERE id = ? AND (name_zh = '' OR name_zh_provenance = ?)`,
				w.Zh, prov, w.Trait.ID, provenanceMachine)
			if res.Error != nil {
				return res.Error
			}
			written += int(res.RowsAffected)
		}
		return nil
	})
	return written, err
}

// counts summarises a plan for the report.
type counts struct {
	Proposals int // distinct en keys parsed out of the source
	Matched   int // vocabulary rows a proposal landed on
	Write     int
	Same      int
	Conflict  int
}

func summarise(proposals []pair, writes []plannedWrite) counts {
	c := counts{Proposals: len(proposals), Matched: len(writes)}
	for _, w := range writes {
		switch w.Decision {
		case decWrite:
			c.Write++
		case decSame:
			c.Same++
		case decConflict:
			c.Conflict++
		}
	}
	return c
}
