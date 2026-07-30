// Package catalogsync reconciles the galgame wiki (the product) against the
// catalog registry (the platform): it registers every wiki galgame as a
// claimed catalog_work with correctly GRADED external anchors, and proposes
// two families of probable cross-references (EG rosetta, strict title match).
//
// Dependency direction: this is a PRODUCT-domain package importing the
// PLATFORM-domain catalog (galgame → catalog is allowed; the reverse is not).
// Its drivers are cmd/reconcile-galgame-works (any phase, on demand) and the
// nightly reconcile-galgame-claims job (the claim phase only, via
// internal/jobs/galgameclaim). No request path ever reaches it.
//
// The R8 three-layer write policy (doc 17 §3) lands here on real data for the
// first time:
//   - exact — first-party curated anchors only (wiki vndb_id, and audit-ok
//     bids that resolve to a Bangumi game subject). Written by ClaimWork.
//   - probable — community rosetta (EG's vndb column) and rule-suggested
//     title matches. Written by InsertRefIfAbsent; the confirm bucket is
//     where a human promotes them to exact.
//
// Everything is idempotent: a second full pass writes nothing.
package catalogsync

import (
	"context"
	"fmt"

	"api/internal/platform/catalog/repository"
	"api/internal/platform/catalog/service"

	"gorm.io/gorm"
)

// Phase selects which reconciliation stages run. They are ordered: eg and
// bangumi attach refs to the works that claim creates, so a full run does
// claim first.
const (
	PhaseClaim   = "claim"
	PhaseEG      = "eg"
	PhaseBangumi = "bangumi"
	PhaseAll     = "all"
)

// The catalog source ids (catalog_source seed) and the galgame medium/site.
const (
	sourceVNDB    int16 = 2
	sourceBangumi int16 = 3
	sourceEG      int16 = 5
	// mediumGalgame is the catalog_medium key id — the WORK MEDIUM, a different
	// key space from the site/tenant key below.
	mediumGalgame int16 = 1
	// siteGalgame is the ECOSYSTEM TENANT KEY written to catalog_work.site. The
	// wiki's tenant identity across the ecosystem is "galgame_wiki" — the same
	// key image_site_usage rows and the wiki's oauth client carry — NOT the
	// medium key "galgame" (mediumGalgame). letmoe/wiki consume catalog BY this
	// tenant key; getting the tenant context wrong fails silently (doc 17 §0;
	// cf. the refping site-scope GC near-miss that nearly GC'd ~66k images).
	siteGalgame     = "galgame_wiki"
	bangumiGameType = 4 // Bangumi subject.type for games.

	// matched_by rule tags — every graded assertion stays traceable/revocable.
	ruleWikiVNDB  = "rule:wiki-vndb-id"
	ruleWikiBID   = "rule:wiki-bid-typed"
	ruleEGRosetta = "rule:eg-vndb-rosetta"
	ruleTitleYear = "rule:title-year-strict"
)

// Options configures a reconciliation run.
type Options struct {
	Phase  string // one of Phase*; "" is treated as PhaseAll
	DryRun bool
	Limit  int // cap the wiki galgames processed (0 = all); debugging aid
}

// ClaimStats reports the work-registration phase.
type ClaimStats struct {
	Processed      int
	ClaimedNew     int
	AlreadyClaimed int
	VNDBExact      int
	BIDExact       int
	BIDSkippedBad  int
	// SkippedRejected counts anchors dropped because their (work, source,
	// external_id) is recorded negative knowledge. Counted in apply only:
	// dry-run does not resolve work ids for the claim phase.
	SkippedRejected int
	// WorkIDBackfilled reports how many wiki galgame.catalog_work_id cells this
	// run actually wrote (NULL→id or changed id). Covered is how many claimed
	// works had an id available to write back (the coverage denominator). In
	// dry-run WorkIDBackfilled is the count that WOULD change (no write).
	WorkIDBackfilled int
	WorkIDCovered    int
	// ClaimStateWritten counts the catalog_work.claim_state cells this run
	// changed (R7). Dry-run reports what WOULD change. A converged re-run
	// reports 0 — the projection is a pure function of the wiki status, so
	// re-running it is the backfill (`--phase claim --apply` over the whole
	// population is how the existing rows get their first value).
	ClaimStateWritten int
}

// EGStats reports the erogamespace rosetta phase.
type EGStats struct {
	Matched          int
	AmbiguousSkipped int
	RefsWritten      int
	Already          int
	Unclaimed        int // game not yet claimed a work (skipped)
	SkippedRejected  int // pairing recorded as negative knowledge
}

// BangumiStats reports the strict title-match phase.
type BangumiStats struct {
	CandidatesLHS     int
	MatchedUnique     int
	RejectedAmbiguous int
	RefsWritten       int
	Already           int
	SkippedRejected   int // pairing recorded as negative knowledge
}

// Stats aggregates every phase that ran.
type Stats struct {
	Claim   ClaimStats
	EG      EGStats
	Bangumi BangumiStats
}

// Reconciler holds the three database handles and the catalog work service.
// wiki and catalog may be the SAME *gorm.DB (the integration test co-locates
// both schemas in one database, as CI does); eg is nil unless the EG phase
// runs.
type Reconciler struct {
	wiki    *gorm.DB
	catalog *gorm.DB
	eg      *gorm.DB
	works   *service.WorkService
	dryRun  bool
	limit   int

	games []wikiGame // preloaded once, reused across phases
	// type4 subject ids (bid audit-ok gate) + the RHS rows for title match.
	type4 []subjectRow
	// rejected is the negative-knowledge index, preloaded once and consulted
	// by every ref-writing phase (doc 17 R7).
	rejected rejectionSet
}

// wikiGame is one preloaded galgame row with the two fields normalized by the
// EXACT Silver expression (lower(normalize(col, NFKC))) so title comparison is
// byte-isomorphic with src_bangumi.*_norm — see bangumi.go.
type wikiGame struct {
	ID     int64  `gorm:"column:id"`
	VNDBID string `gorm:"column:vndb_id"`
	BID    *int64 `gorm:"column:bid"`
	// Status is the wiki's own publication state machine (model.GalgameStatus*).
	// It NEVER crosses into the catalog contract as a value — it is projected
	// here into the catalog-owned claim_state vocabulary (R7) and nothing else.
	Status   int16  `gorm:"column:status"`
	NameJaJP string `gorm:"column:name_ja_jp"`
	NameZhCN string `gorm:"column:name_zh_cn"`
	NameEnUS string `gorm:"column:name_en_us"`
	NameZhTW string `gorm:"column:name_zh_tw"`
	JaNorm   string `gorm:"column:ja_norm"`
	ZhNorm   string `gorm:"column:zh_norm"`
	Year     *int   `gorm:"column:year"`
	// OriginalLanguage is the wiki's product-locale spelling (ja-jp / en-us /
	// zh-cn / …). It becomes the claimed work's catalog olang via
	// model.MapWikiOLang — see runClaim.
	OriginalLanguage string `gorm:"column:original_language"`
}

// subjectRow is one type=4 Bangumi subject: its normalized names and release
// year, the RHS of the strict title match.
type subjectRow struct {
	ID         int64  `gorm:"column:id"`
	NameNorm   string `gorm:"column:name_norm"`
	NameCNNorm string `gorm:"column:name_cn_norm"`
	Year       int    // parsed from date (0 = no usable year)
}

// New wires a reconciler. eg may be nil when the EG phase will not run.
func New(wiki, catalog, eg *gorm.DB, opts Options) *Reconciler {
	// WorkService needs a ResolveService, but ClaimWork resolves through
	// package-level tx helpers; a resolve service backed by the same db keeps
	// the constructor honest without adding a second code path.
	return &Reconciler{
		wiki:    wiki,
		catalog: catalog,
		eg:      eg,
		works:   service.NewWorkService(catalog, service.NewResolveService(repository.NewRedirectRepository(catalog))),
		dryRun:  opts.DryRun,
		limit:   opts.Limit,
	}
}

// Run executes the requested phase(s) and returns the aggregated stats.
func (r *Reconciler) Run(ctx context.Context, phase string) (Stats, error) {
	if phase == "" {
		phase = PhaseAll
	}
	var stats Stats
	if err := r.loadGames(); err != nil {
		return stats, fmt.Errorf("load wiki galgames: %w", err)
	}
	if err := r.loadType4Subjects(); err != nil {
		return stats, fmt.Errorf("load type=4 subjects: %w", err)
	}
	rejected, err := loadRejections(r.catalog)
	if err != nil {
		return stats, fmt.Errorf("load match rejections: %w", err)
	}
	r.rejected = rejected

	if phase == PhaseClaim || phase == PhaseAll {
		cs, err := r.runClaim(ctx)
		if err != nil {
			return stats, fmt.Errorf("claim phase: %w", err)
		}
		stats.Claim = cs
	}
	if phase == PhaseEG || phase == PhaseAll {
		if r.eg == nil {
			return stats, fmt.Errorf("eg phase requested but no erogamespace connection provided")
		}
		es, err := r.runEG()
		if err != nil {
			return stats, fmt.Errorf("eg phase: %w", err)
		}
		stats.EG = es
	}
	if phase == PhaseBangumi || phase == PhaseAll {
		bs, err := r.runBangumi()
		if err != nil {
			return stats, fmt.Errorf("bangumi phase: %w", err)
		}
		stats.Bangumi = bs
	}
	return stats, nil
}

// loadGames preloads every galgame (or the first --limit by id), normalizing
// the two title fields with the SAME Postgres expression as the Silver
// generated columns and extracting the release year. Loaded once, reused by
// all phases.
func (r *Reconciler) loadGames() error {
	q := `
		SELECT id, vndb_id, bid, status, name_ja_jp, name_zh_cn, name_en_us, name_zh_tw,
		       original_language,
		       lower(normalize(name_ja_jp, NFKC)) AS ja_norm,
		       lower(normalize(name_zh_cn, NFKC)) AS zh_norm,
		       CASE WHEN release_date IS NOT NULL
		            THEN EXTRACT(YEAR FROM release_date)::int END AS year
		FROM galgame ORDER BY id`
	if r.limit > 0 {
		q += fmt.Sprintf(" LIMIT %d", r.limit)
	}
	return r.wiki.Raw(q).Scan(&r.games).Error
}

// loadType4Subjects preloads the game-subject rows: their ids gate the bid
// anchor (audit-ok = exists AND type=4), and the normalized names + year are
// the RHS of the title match.
func (r *Reconciler) loadType4Subjects() error {
	var rows []struct {
		ID         int64  `gorm:"column:id"`
		NameNorm   string `gorm:"column:name_norm"`
		NameCNNorm string `gorm:"column:name_cn_norm"`
		Date       string `gorm:"column:date"`
	}
	if err := r.catalog.Raw(
		`SELECT id, name_norm, name_cn_norm, date FROM src_bangumi.subject WHERE type = ?`,
		bangumiGameType,
	).Scan(&rows).Error; err != nil {
		return err
	}
	r.type4 = make([]subjectRow, 0, len(rows))
	for _, s := range rows {
		r.type4 = append(r.type4, subjectRow{
			ID: s.ID, NameNorm: s.NameNorm, NameCNNorm: s.NameCNNorm,
			Year: yearOf(s.Date),
		})
	}
	return nil
}

// workIDMap returns galgame id → catalog_work id for the eg/bangumi phases.
// In apply mode it reads the works the claim phase already created. In dry-run
// no works exist yet, so it treats every processed game as claim-eligible
// (work id 0 is a placeholder — dry-run skips the insert): this makes a dry
// run report the SAME plan a fresh apply would execute, instead of counting
// everything as unclaimed.
func (r *Reconciler) workIDMap() (map[int64]int64, error) {
	if r.dryRun {
		m := make(map[int64]int64, len(r.games))
		for i := range r.games {
			m[r.games[i].ID] = 0
		}
		return m, nil
	}
	return repository.LoadClaimedWorkIDs(r.catalog, siteGalgame)
}

// type4IDs is the set of game-subject ids — a bid is audit-ok iff present.
func (r *Reconciler) type4IDs() map[int64]struct{} {
	set := make(map[int64]struct{}, len(r.type4))
	for _, s := range r.type4 {
		set[s.ID] = struct{}{}
	}
	return set
}

// yearOf parses the leading 4 digits of a Bangumi date string ("2004-04-28",
// "2008", "") into a plausible year, or 0 when there is none.
func yearOf(date string) int {
	if len(date) < 4 {
		return 0
	}
	y := 0
	for i := 0; i < 4; i++ {
		c := date[i]
		if c < '0' || c > '9' {
			return 0
		}
		y = y*10 + int(c-'0')
	}
	if y < 1900 || y > 2100 {
		return 0
	}
	return y
}
