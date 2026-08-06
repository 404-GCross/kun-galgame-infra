package entityintromt

import (
	"context"
	"strconv"
	"strings"

	"gorm.io/gorm"
)

// An entity intro is even more proper-noun dense than a work blurb: a character
// profile is mostly her own name plus the titles she appears in, a voice-actor
// blurb is a name plus a filmography, a brand blurb is a brand name plus its
// catalogue. Left alone the model INVENTS kanji for kana names that already
// have an authoritative Chinese rendering in this database
// (catalog_character_alias / catalog_name_alias / catalog_label_alias /
// catalog_work_title, all lang zh-*).
//
// So every candidate carries its own small glossary, the prompt is told to use
// those renderings verbatim and to leave everything else in the original script
// (translate.go), and the glossary is FOLDED INTO src_hash (write.go) so a
// changed glossary re-translates exactly the affected rows and nothing else.
//
// The value type, the cap, the priority rule and the canonical serialization
// are deliberately DUPLICATED from intromt rather than shared: the two jobs are
// independent binaries with independent schedules, and a shared package would
// couple their release cadence for ~80 lines. The serialization format is the
// one thing that must stay byte-identical — see Canonical.

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
	// maxGlossaryEntries caps one candidate's glossary; past that the prompt
	// budget is better spent on the source text itself.
	maxGlossaryEntries = 20
	// maxWorksPerEntity bounds how many of an entity's works contribute a title
	// pair. Unbounded it is a planner problem (a prolific brand or a veteran
	// voice actress has thousands of edges), and past the cap the entries would
	// be discarded anyway.
	maxWorksPerEntity = 12
	// glossaryChunk bounds one IN list — glossaries are loaded in BULK for the
	// whole candidate set, never per row.
	glossaryChunk = 1000
)

// Canonical renders the glossary as the hash input: one "src\tzh" line per
// entry, newline-joined, in priority order. Byte-identical to
// intromt.Glossary.Canonical by contract.
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
// highest-priority pair wins and later ones are dropped. Pairs that would teach
// the model nothing (empty side, or a rendering identical to the source) never
// enter — which is also why an entity's several zh aliases collapse to the best
// one: they all share the same source term.
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

// Own-name queries. Aliases are ordered primary-for-locale first, then zh-Hans
// (the target language) before the other Chinese tags, then lowest id — a
// total, deterministic order. Kind 2 (search hint) is excluded everywhere: it
// is a findability string, never a displayed name.
const (
	characterOwnQuery = `
		SELECT a.character_id AS owner_id, c.display_name AS src, a.name AS zh
		FROM catalog_character_alias a
		JOIN catalog_character c ON c.id = a.character_id
		WHERE a.character_id IN (?)
		  AND a.lang IN ('zh-Hans','zh','zh-Hant') AND a.kind IN (0,1)
		ORDER BY a.character_id, (NOT a.is_primary_for_locale), (a.lang <> 'zh-Hans'), a.id`

	// A person's name of record is its PRIMARY credit name (invariant 1: the
	// entity layer never hangs identity off catalog_person itself), so the
	// source term comes from catalog_credit_name and the rendering from that
	// name's zh aliases.
	personOwnQuery = `
		SELECT p.id AS owner_id, cn.name AS src, a.name AS zh
		FROM catalog_person p
		JOIN catalog_credit_name cn ON cn.id = p.primary_credit_name_id
		JOIN catalog_name_alias a ON a.credit_name_id = cn.id
		  AND a.lang IN ('zh-Hans','zh','zh-Hant') AND a.kind IN (0,1)
		WHERE p.id IN (?)
		ORDER BY p.id, (NOT a.is_primary_for_locale), (a.lang <> 'zh-Hans'), a.id`

	labelOwnQuery = `
		SELECT a.label_id AS owner_id, l.display_name AS src, a.name AS zh
		FROM catalog_label_alias a
		JOIN catalog_label l ON l.id = a.label_id
		WHERE a.label_id IN (?)
		  AND a.lang IN ('zh-Hans','zh','zh-Hant') AND a.kind IN (0,1)
		ORDER BY a.label_id, (NOT a.is_primary_for_locale), (a.lang <> 'zh-Hans'), a.id`
)

// workTitlePairSQL is the second half of every "related works" query: from a
// `scope` CTE of (owner_id, work_id) it emits the ja→zh title pairs of the
// first maxWorksPerEntity works, in work-id order.
//
// Only kinds official(0) and alias(1) are read: kind 3 is a SEARCH HINT (157k
// rows in prod), a findability string nobody would recognise in prose. A work
// with no ja title contributes nothing — its zh title is almost always ALSO its
// display_name, so there is no Japanese side to map FROM.
var workTitlePairSQL = `,
		t AS (
			SELECT work_id, lang, title, kind, id FROM catalog_work_title
			WHERE work_id IN (SELECT work_id FROM scope) AND kind IN (0,1)
			  AND (lang = 'ja' OR lang IN ('zh-Hans','zh','zh-Hant'))
		),
		jat AS (
			SELECT DISTINCT ON (work_id) work_id, title FROM t
			WHERE lang = 'ja' ORDER BY work_id, kind, id
		),
		zht AS (
			SELECT DISTINCT ON (work_id) work_id, title FROM t
			WHERE lang <> 'ja' ORDER BY work_id, (lang <> 'zh-Hans'), kind, id
		),
		pairs AS (
			SELECT s.owner_id, jat.title AS src, zht.title AS zh,
				row_number() OVER (PARTITION BY s.owner_id ORDER BY s.work_id) AS rn
			FROM scope s
			JOIN jat ON jat.work_id = s.work_id
			JOIN zht ON zht.work_id = s.work_id
		)
		SELECT owner_id, src, zh FROM pairs WHERE rn <= ` + strconv.Itoa(maxWorksPerEntity) + `
		ORDER BY owner_id, rn`

// Related-work queries: the works whose titles a given entity's intro is likely
// to name. Each takes the owner-id list EXACTLY ONCE so the chunked loader can
// bind a single argument.
var (
	// The works the character appears in (the roster edge, not a voice credit).
	characterWorksQuery = `
		WITH scope AS (
			SELECT DISTINCT wc.character_id AS owner_id, wc.work_id
			FROM catalog_work_character wc
			JOIN catalog_work w ON w.id = wc.work_id AND w.deleted_at IS NULL
			WHERE wc.character_id IN (?)
		)` + workTitlePairSQL

	// The works the person is credited on. Discovery runs over ALL of the
	// person's credit names including link_visibility=hidden ones: the glossary
	// only supplies Chinese renderings of WORK TITLES, so a hidden name can
	// widen the term list without any name linkage reaching the output.
	personWorksQuery = `
		WITH scope AS (
			SELECT DISTINCT cn.person_id AS owner_id, cr.work_id
			FROM catalog_credit_name cn
			JOIN catalog_credit cr ON cr.credit_name_id = cn.id
			JOIN catalog_work w ON w.id = cr.work_id AND w.deleted_at IS NULL
			WHERE cn.person_id IN (?)
		)` + workTitlePairSQL

	// The works the label signed.
	labelWorksQuery = `
		WITH scope AS (
			SELECT DISTINCT wl.label_id AS owner_id, wl.work_id
			FROM catalog_work_label wl
			JOIN catalog_work w ON w.id = wl.work_id AND w.deleted_at IS NULL
			WHERE wl.label_id IN (?)
		)` + workTitlePairSQL
)

// laneGlossaryQueries lists each lane's term groups IN PRIORITY ORDER: the
// entity's own name first, then its related works' titles.
var laneGlossaryQueries = map[string][]string{
	LaneCharacter: {characterOwnQuery, characterWorksQuery},
	LanePerson:    {personOwnQuery, personWorksQuery},
	LaneLabel:     {labelOwnQuery, labelWorksQuery},
}

// attachGlossaries loads every candidate's glossary in BULK and hangs it on the
// candidate. A failure here aborts the run rather than degrading to
// glossary-less translation: a row written without the glossary would carry the
// plain hash and be re-translated by the very next run, spending the budget
// twice for a worse result.
func attachGlossaries(ctx context.Context, db *gorm.DB, lane laneDef, cands []candidate) error {
	ids := make([]int64, 0, len(cands))
	for _, c := range cands {
		ids = append(ids, c.EntityID)
	}
	gs, err := loadGlossaries(ctx, db, lane, ids)
	if err != nil {
		return err
	}
	for i := range cands {
		cands[i].Gloss = gs[cands[i].EntityID]
	}
	return nil
}

// loadGlossaries resolves the glossary of every listed entity of one lane.
func loadGlossaries(ctx context.Context, db *gorm.DB, lane laneDef, ids []int64) (map[int64]Glossary, error) {
	builders := make(map[int64]*glossaryBuilder, len(ids))
	for _, id := range ids {
		builders[id] = &glossaryBuilder{}
	}
	for _, q := range laneGlossaryQueries[lane.key] {
		if err := collectGlossary(ctx, db, q, ids, builders); err != nil {
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
