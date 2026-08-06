package intromt

import (
	"context"
	"strings"

	"gorm.io/gorm"
)

// A machine translation of a galgame blurb is mostly a proper-noun problem:
// character names, work titles and brand names are what the model gets wrong,
// and it gets them wrong in the most expensive way — it INVENTS kanji for kana
// names that already have an authoritative Chinese rendering in this very
// database (catalog_work_title zh-*, catalog_character_alias zh-*,
// catalog_label_alias zh-*, catalog_name_alias zh-*).
//
// So every candidate carries its own small glossary of the terms it is likely
// to contain, the prompt is told to use those renderings verbatim and to leave
// everything else in the original script (translate.go), and the glossary is
// FOLDED INTO src_hash (write.go) so a changed glossary re-translates exactly
// the affected rows and nothing else.

// GlossaryEntry is one authoritative rendering: the term as it appears in the
// source text (Src) and the Chinese form the catalog already holds (Zh).
type GlossaryEntry struct {
	Src string
	Zh  string
}

// Glossary is a candidate's term list in PRIORITY ORDER — the order is part of
// the contract, because it decides both what survives the cap and what the
// canonical serialization (and therefore the hash) looks like.
type Glossary []GlossaryEntry

const (
	// maxGlossaryEntries caps one candidate's glossary. A blurb is a few
	// hundred characters; twenty terms already covers the work itself, its
	// roster and its brand, and a longer list costs prompt budget that the
	// source text needs more.
	maxGlossaryEntries = 20
	// maxWorksPerEntity bounds how many of an entity's works contribute a title
	// pair. Unbounded it is a planner problem (a prolific brand has thousands),
	// and past the cap the entries would be discarded anyway.
	maxWorksPerEntity = 12
	// glossaryChunk bounds one IN list — glossaries are loaded in BULK for the
	// whole candidate set, never per row.
	glossaryChunk = 1000
)

// Canonical renders the glossary as the hash input: one "src\tzh" line per
// entry, newline-joined, in priority order. Identical in entityintromt — the
// two jobs must produce the same bytes for the same term list, so a term list
// that moves between them (a work title is a work title) hashes the same.
func (g Glossary) Canonical() string {
	if len(g) == 0 {
		return ""
	}
	lines := make([]string, 0, len(g))
	for _, e := range g {
		lines = append(lines, e.Src+"\t"+e.Zh)
	}
	return strings.Join(lines, "\n")
}

// glossaryBuilder accumulates entries in priority order, deduplicating BY
// SOURCE TERM: a term has exactly one authoritative rendering, so the
// highest-priority pair wins and later ones are dropped. Pairs that would
// teach the model nothing (empty side, or a rendering identical to the source)
// never enter.
type glossaryBuilder struct {
	seen map[string]struct{}
	out  Glossary
}

func (b *glossaryBuilder) add(src, zh string) {
	if len(b.out) >= maxGlossaryEntries {
		return
	}
	src, zh = strings.TrimSpace(src), strings.TrimSpace(zh)
	if src == "" || zh == "" || src == zh {
		return
	}
	if b.seen == nil {
		b.seen = make(map[string]struct{}, maxGlossaryEntries)
	}
	if _, dup := b.seen[src]; dup {
		return
	}
	b.seen[src] = struct{}{}
	b.out = append(b.out, GlossaryEntry{Src: src, Zh: zh})
}

// glossRow is the shared shape every glossary query returns: (owner, source
// term, Chinese rendering), already ordered by the query.
type glossRow struct {
	OwnerID int64  `gorm:"column:owner_id"`
	Src     string `gorm:"column:src"`
	Zh      string `gorm:"column:zh"`
}

// The three term groups of the WORK lane, in priority order. Each takes the
// work-id list EXACTLY ONCE so the chunked loader can bind a single argument.
//
// Only kinds official(0) and alias(1) are read from catalog_work_title:
// kind 3 is a SEARCH HINT (157k rows in prod), a findability string that is
// not a title anyone would recognise in prose. The same rule applies to the
// alias tables, whose kind 2 is the search hint.
//
// The Chinese side prefers zh-Hans (the target language), then the official
// kind, then the lowest id — a total, deterministic order. The Japanese side
// is the surfaced title: official kind first, then lowest id. A work with no
// ja title contributes nothing: its zh title is almost always ALSO its
// display_name (2,393 of 2,455 such works in prod), so pairing it with the
// display name would produce a no-op "中文 → 中文" entry.
const (
	// 1. The work's own title.
	workOwnTitleQuery = `
		WITH t AS (
			SELECT work_id, lang, title, kind, id FROM catalog_work_title
			WHERE work_id IN (?) AND kind IN (0,1)
			  AND (lang = 'ja' OR lang IN ('zh-Hans','zh','zh-Hant'))
		),
		jat AS (
			SELECT DISTINCT ON (work_id) work_id, title FROM t
			WHERE lang = 'ja' ORDER BY work_id, kind, id
		),
		zht AS (
			SELECT DISTINCT ON (work_id) work_id, title FROM t
			WHERE lang <> 'ja' ORDER BY work_id, (lang <> 'zh-Hans'), kind, id
		)
		SELECT jat.work_id AS owner_id, jat.title AS src, zht.title AS zh
		FROM jat JOIN zht ON zht.work_id = jat.work_id
		ORDER BY jat.work_id`

	// 2. The roster characters that already have a Chinese alias, in
	// character-id order (a roster has no notion of a "primary" member).
	workRosterQuery = `
		WITH ros AS (
			SELECT wc.work_id, wc.character_id, c.display_name
			FROM catalog_work_character wc
			JOIN catalog_character c ON c.id = wc.character_id AND c.deleted_at IS NULL
			WHERE wc.work_id IN (?)
		),
		al AS (
			SELECT DISTINCT ON (a.character_id) a.character_id, a.name
			FROM catalog_character_alias a
			WHERE a.character_id IN (SELECT character_id FROM ros)
			  AND a.lang IN ('zh-Hans','zh','zh-Hant') AND a.kind IN (0,1)
			ORDER BY a.character_id, (NOT a.is_primary_for_locale), (a.lang <> 'zh-Hans'), a.id
		)
		SELECT ros.work_id AS owner_id, ros.display_name AS src, al.name AS zh
		FROM ros JOIN al ON al.character_id = ros.character_id
		ORDER BY ros.work_id, ros.character_id`

	// 3. The brands/circles that signed it.
	workLabelQuery = `
		WITH lab AS (
			SELECT DISTINCT wl.work_id, wl.label_id, l.display_name
			FROM catalog_work_label wl
			JOIN catalog_label l ON l.id = wl.label_id AND l.deleted_at IS NULL
			WHERE wl.work_id IN (?)
		),
		al AS (
			SELECT DISTINCT ON (a.label_id) a.label_id, a.name
			FROM catalog_label_alias a
			WHERE a.label_id IN (SELECT label_id FROM lab)
			  AND a.lang IN ('zh-Hans','zh','zh-Hant') AND a.kind IN (0,1)
			ORDER BY a.label_id, (NOT a.is_primary_for_locale), (a.lang <> 'zh-Hans'), a.id
		)
		SELECT lab.work_id AS owner_id, lab.display_name AS src, al.name AS zh
		FROM lab JOIN al ON al.label_id = lab.label_id
		ORDER BY lab.work_id, lab.label_id`
)

// attachGlossaries loads every candidate's glossary in BULK and hangs it on the
// candidate. A failure here aborts the run rather than degrading to
// glossary-less translation: a row written without the glossary would carry the
// plain hash and be re-translated by the very next run, spending the budget
// twice for a worse result.
func attachGlossaries(ctx context.Context, db *gorm.DB, cands []candidate) error {
	ids := make([]int64, 0, len(cands))
	for _, c := range cands {
		ids = append(ids, c.WorkID)
	}
	gs, err := loadGlossaries(ctx, db, ids)
	if err != nil {
		return err
	}
	for i := range cands {
		cands[i].Gloss = gs[cands[i].WorkID]
	}
	return nil
}

// loadGlossaries resolves the glossary of every listed work. The query ORDER is
// the priority order: own title, then roster characters, then labels.
func loadGlossaries(ctx context.Context, db *gorm.DB, workIDs []int64) (map[int64]Glossary, error) {
	builders := make(map[int64]*glossaryBuilder, len(workIDs))
	for _, id := range workIDs {
		builders[id] = &glossaryBuilder{}
	}
	for _, q := range []string{workOwnTitleQuery, workRosterQuery, workLabelQuery} {
		if err := collectGlossary(ctx, db, q, workIDs, builders); err != nil {
			return nil, err
		}
	}
	out := make(map[int64]Glossary, len(builders))
	for id, b := range builders {
		if len(b.out) > 0 {
			out[id] = b.out
		}
	}
	return out, nil
}

// collectGlossary runs one glossary query over the owner ids in chunks and
// feeds the rows to their owners' builders, preserving the query's order.
func collectGlossary(ctx context.Context, db *gorm.DB, query string, ids []int64, builders map[int64]*glossaryBuilder) error {
	for start := 0; start < len(ids); start += glossaryChunk {
		end := min(start+glossaryChunk, len(ids))
		var rows []glossRow
		if err := db.WithContext(ctx).Raw(query, ids[start:end]).Scan(&rows).Error; err != nil {
			return err
		}
		for _, row := range rows {
			if b := builders[row.OwnerID]; b != nil {
				b.add(row.Src, row.Zh)
			}
		}
	}
	return nil
}
