package model

import "time"

// CatalogTagWorkCount is the precomputed work_count behind every canonical-tag
// chip: one row per tag, holding the three tallies the read face can be asked
// for. It exists because the tag edge — alone among the taxonomy edges — cannot
// answer the question cheaply.
//
// A label or an engine reaches works through a direct foreign key, so counting
// them is an index range per key. A canonical tag reaches works through
// catalog_tag_source_map ⋈ catalog_work_tag, which is ~1.2M rows wide and has
// no ordering that serves both directions: postgres' best plan walks the whole
// gated work population and expands each work's ~39 tag rows, producing ~430k
// rows to keep ~69k. Measured at 200-400ms in production, once per
// GET /works/{id} — 90% of the catalog service's slow-query log. It is not a
// plan problem: forcing every alternative join order, adding a covering index
// and denormalizing the mapping onto catalog_work_tag were each tried and each
// came out slower or barely better, because the work is genuine.
//
// The three columns are the read face's whole parameter space. A chip's count
// varies only with the caller's nsfw setting, and the display tally never
// varies at all, so the live aggregate's FILTER clauses become three columns:
//
//	NAll  — the nsfw caller's count (no content_rating gate)
//	NSFW_ — the sfw caller's count (content_rating <> r18)
//	NNSFW — how many of the tag's works are editorially nsfw (the display axis)
//
// Every other gate (deleted_at, status, medium, claim_state=live) is fixed by
// the contract, which is what makes a rollup possible here at all.
//
// The tradeoff this table makes, stated plainly: the number beside a tag chip
// stops being exact-by-construction and becomes exact-as-of ComputedAt. That is
// a real weakening of the A2-R1 invariant and it is deliberate — the counts only
// move when a batch import runs, and a browsing aid that is minutes stale is
// worth more than one that costs a third of a second on every work page. It is
// weakened for TAGS ONLY: the label, engine and series edges are counted live,
// because they are fast and there is nothing to buy by making them stale.
//
// The refresh lives in the service package, not in the job that schedules it,
// so that it composes the population gates from the very same helpers the read
// path does. A rollup computed by a second implementation of the predicate
// would not be stale — it would be wrong, which is worse.
type CatalogTagWorkCount struct {
	// TagID is catalog_tag.id, and the whole key: one row per canonical tag.
	TagID int64 `gorm:"primaryKey;autoIncrement:false" json:"tag_id"`
	// The three tallies carry no DEFAULT: zero is a meaningful count (a tag
	// whose works are all deleted really does reach none), and a default would
	// let the database quietly substitute it for a value we meant to write.
	NAll  int `gorm:"not null" json:"n_all"`
	NSfw  int `gorm:"not null" json:"n_sfw"`
	NNsfw int `gorm:"not null" json:"n_nsfw"`
	// ComputedAt is what makes the staleness observable rather than a rumour:
	// an operator can see how old the numbers are without reading the crontab.
	ComputedAt time.Time `gorm:"not null" json:"computed_at"`
}

func (CatalogTagWorkCount) TableName() string { return "catalog_tag_work_count" }
