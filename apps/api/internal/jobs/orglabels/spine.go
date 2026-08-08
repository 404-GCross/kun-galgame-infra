package orglabels

// The corporate-graph SPINE.
//
// Every other lane in this package derives a label's EXISTENCE from work
// attribution: a source org earns a label because some work needed attributing
// to it. That rule has a blind spot the size of the industry's actual corporate
// structure — a holding company, a parent publisher, a predecessor social name
// publishes nothing under its own name, so it can never come into being here,
// and VNDB's producers_relations graph is mostly ABOUT those entities. Measured
// before this pass existed: 4,432 of 7,318 upstream edges (60.6%) had no second
// endpoint to point at, and the biggest missing nodes were VISUAL ARTS,
// WillPlus, HOBIBOX and NEXTON — every one of them a parent whose works are all
// attributed to its brands.
//
// So this pass grants existence on a different warrant: PARTICIPATION IN THE
// GRAPH. A VNDB producer that upstream relates to another producer is an entity
// the catalog can say something about, whether or not it ever shipped a game
// under that name.
//
// Three rules make that safe:
//
//   - A spine label is minted with NO work_label edges. The whole point is to
//     decouple existence from attribution: a parent must not absorb its
//     children's games, which are already correctly attributed to the brands.
//     (This is the one place the package mints work-free, and it is why it does
//     not reuse mintLabel.)
//   - It runs AFTER the co-work pass in every fixpoint iteration, over what is
//     still unanchored. The co-work pass keeps its mint-with-edges behaviour for
//     the orgs it can grade; the spine only picks up what that pass dropped
//     (conflict / ungradeable / ambiguous), so it is strictly additive.
//   - It NEVER guesses an identity. One same-named label → anchor it; none →
//     mint; several → the pair goes to catalog_match_candidate for the human
//     dedup worklist, because "these two same-named labels are one brand" is a
//     curation judgement this package is not entitled to make (see the classLabel
//     banner in catalog-dedup-batch: same-name labels are routinely NOT the same
//     brand). What counts as "same-named" is itself graded — see planSpine.
//
// VNDB type=in producers are excluded — a person is not a label, and minting one
// would smuggle the frozen person-identity resolution into the label space
// (the same rule the co-work pass enforces via canCreate).

import (
	"context"
	"slices"

	"api/internal/platform/catalog/model"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Spine matched_by rule ids, distinct from the co-work lane's so the provenance
// says which warrant granted the anchor.
const (
	ruleSpineNew  = "rule:vndb-org-graph-new"
	ruleSpineName = "rule:vndb-org-graph-name"
)

// graphFacts is what producers_relations says about a producer, independent of
// any work.
//
// linkable, not mere membership, is the existence test. A producer whose only
// upstream relations point at type=in individuals has nothing this catalog can
// render: persons are not labels, so those edges can never land, and minting it
// would produce a page with no works AND no family — the empty entity the whole
// wave exists to avoid creating. Measured: 39 of 1,100 graph members are in
// that position.
type graphFacts struct {
	member   map[string]bool // appears in producers_relations at all
	linkable map[string]bool // …and at least one neighbour is a co/ng producer
	parent   map[string]bool // …and something hangs UNDER it (publisher, not brand)
}

// spineAct is the planner's verdict for one producer.
type spineAct int

const (
	spineMint          spineAct = iota // no same-named label exists — bring the node into being
	spineAnchor                        // exactly one free same-named label — it IS this producer
	spineCandidate                     // several same-named labels — hand the pair to the dedup queue
	spineSkipClaimed                   // the one same-named label is already another producer's identity
	spineSkipAliasOnly                 // several labels matched, all of them only by alias
)

// matchLabels returns the labels any of these name folds resolves to, ascending
// (catalog_match_candidate has an a_id < b_id check constraint, so the order is
// load-bearing, not cosmetic).
func matchLabels(norms []string, index map[string][]int64) []int64 {
	hit := make(map[int64]bool)
	for _, n := range norms {
		if n == "" {
			continue
		}
		for _, l := range index[n] {
			hit[l] = true
		}
	}
	out := make([]int64, 0, len(hit))
	for l := range hit {
		out = append(out, l)
	}
	slices.Sort(out)
	return out
}

// spinePlan is one decided producer. labelID is set for spineAnchor; labels
// carries the ambiguous set for spineCandidate.
type spinePlan struct {
	act     spineAct
	org     *orgRec
	labelID int64
	labels  []int64
}

// SpineStats is the pass's receipt. Every producer the pass looked at lands in
// exactly one counter — a bucket that silently vanished would read as coverage.
type SpineStats struct {
	Considered    int // graph members of a mintable type that are not yet anchored
	Minted        int
	Anchored      int
	Candidates    int // producers routed to the dedup queue
	CandidateRows int // DISTINCT label pairs written (two producers can raise one pair)
	SkipClaimed   int // same-named label already claimed by another producer
	SkipEdgeless  int // in the graph, but every neighbour is an individual — see graphFacts
	SkipAliasOnly int // several labels matched, none of them by its own display name
	Errors        int
}

// addWrites accumulates only what a pass DID. The counters below it describe
// what is still unresolved after a pass, which is state, not events.
func (s *SpineStats) addWrites(o SpineStats) {
	s.Minted += o.Minted
	s.Anchored += o.Anchored
	s.Errors += o.Errors
}

// setState overwrites the "what is left" counters with a pass's view. The
// fixpoint's LAST pass is the settled answer, so it replaces earlier ones —
// adding them would report the same pending candidate once per iteration.
func (s *SpineStats) setState(o SpineStats) {
	s.Considered = o.Considered
	s.Candidates = o.Candidates
	s.CandidateRows = o.CandidateRows
	s.SkipClaimed = o.SkipClaimed
	s.SkipEdgeless = o.SkipEdgeless
	s.SkipAliasOnly = o.SkipAliasOnly
}

// writes reports whether the pass changed anything — the fixpoint loop's
// termination signal.
func (s SpineStats) writes() int { return s.Minted + s.Anchored }

// planSpine decides every producer without touching the database, so the whole
// policy is unit-testable and the dry run and the apply run share one code path.
//
// claimed starts from the labels this source already holds an identity anchor on
// and GROWS as the plan is built: two producers that name-match the same lone
// label cannot both be it, and the first by external id wins so a re-plan is
// stable.
//
// It takes TWO name indices, because the two things a name match can do here
// need different strengths of evidence.
//
// labelNorms folds display names AND label aliases; displayNorms folds display
// names only. A label alias is NOT reliably an "is also called" — catalog_label
// carries aliases like ネクストン on the labels PSYCHO and RaSeN, where the
// upstream meaning is "PUBLISHED BY NEXTON". So:
//
//   - alias evidence may BLOCK a mint (creating a twin of a label that already
//     answers to this name is the one irreversible mistake here), and may anchor
//     when it points at exactly ONE label with no competition — that is how the
//     legal names land (株式会社ビジュアルアーツ → VISUAL ARTS, 株式会社ウィルプラス
//     → ウィルプラス);
//   - but once SEVERAL labels match, the alias is exactly what created the
//     ambiguity, so the proposal is narrowed to labels matched by their own
//     display name. NEXTON matches four labels and only two of them are named
//     NEXTON; nominating PSYCHO for a merge into NEXTON would be a wrong answer
//     handed to a human as a suggestion.
func planSpine(orgs []orgRec, g graphFacts, ea *existingAnchors, labelNorms, displayNorms map[string][]int64) ([]spinePlan, SpineStats) {
	var st SpineStats
	claimed := make(map[int64]bool, len(ea.claimedByLabel))
	for l := range ea.claimedByLabel {
		claimed[l] = true
	}

	out := make([]spinePlan, 0, 64)
	for i := range orgs {
		o := &orgs[i]
		// canCreate is false for VNDB persons — never a label, see the banner.
		if !o.canCreate || !g.member[o.extID] || o.displayName == "" {
			continue
		}
		if _, done := ea.byExtID[o.extID]; done {
			continue
		}
		// Counted, not silently dropped: a bucket that vanishes reads as coverage.
		if !g.linkable[o.extID] {
			st.SkipEdgeless++
			continue
		}
		st.Considered++

		any := matchLabels(o.nameNorms, labelNorms)
		// Only one label answers to this name at all: no ambiguity for the alias
		// to have manufactured, so alias evidence is good enough to anchor.
		target := any
		if len(any) > 1 {
			target = matchLabels(o.nameNorms, displayNorms)
		}

		switch {
		case len(any) == 0:
			out = append(out, spinePlan{act: spineMint, org: o})
			st.Minted++
		case len(target) == 0:
			// Several labels matched, none by its own display name — the match is
			// alias noise. Neither mint (a twin would be worse) nor assert.
			out = append(out, spinePlan{act: spineSkipAliasOnly, org: o, labels: any})
			st.SkipAliasOnly++
		case len(target) == 1 && !claimed[target[0]]:
			claimed[target[0]] = true
			out = append(out, spinePlan{act: spineAnchor, org: o, labelID: target[0]})
			st.Anchored++
		case len(target) == 1:
			out = append(out, spinePlan{act: spineSkipClaimed, org: o, labelID: target[0]})
			st.SkipClaimed++
		default:
			out = append(out, spinePlan{act: spineCandidate, org: o, labels: target})
			st.Candidates++
		}
	}
	st.CandidateRows = len(candidatePairs(out))
	return out, st
}

// candidatePairs is the deduped set of label pairs a plan raises, in plan order.
// Two producers can name-match the same labels and so nominate the same pair;
// one shared derivation keeps the dry run's count and the apply run's inserts
// from disagreeing about what "candidate rows" means.
func candidatePairs(plans []spinePlan) [][2]int64 {
	seen := make(map[[2]int64]bool)
	out := make([][2]int64, 0, len(plans))
	for _, p := range plans {
		if p.act != spineCandidate {
			continue
		}
		for i := 0; i < len(p.labels); i++ {
			for j := i + 1; j < len(p.labels); j++ {
				pair := [2]int64{p.labels[i], p.labels[j]} // labels are sorted: a < b
				if seen[pair] {
					continue
				}
				seen[pair] = true
				out = append(out, pair)
			}
		}
	}
	return out
}

// spineKind is the label kind a minted spine node gets. A producer with
// something hanging under it is a PUBLISHER, not a game brand — that is the
// whole reason it has no works of its own, and calling it a brand would put a
// holding company in the same bucket as the studios it owns.
func spineKind(o *orgRec, g graphFacts) int16 {
	if g.parent[o.extID] && o.newKind == model.LabelKindGameBrand {
		return model.LabelKindPublisher
	}
	return o.newKind
}

// runSpine plans and (when applying) writes one spine pass.
func runSpine(ctx context.Context, db *gorm.DB, labelNorms map[string][]int64, limit int, apply bool) (SpineStats, error) {
	orgs, _, _, err := loadSource(db, nil, "vndb", limit)
	if err != nil {
		return SpineStats{}, err
	}
	g, err := loadGraphFacts(db)
	if err != nil {
		return SpineStats{}, err
	}
	ea, err := loadExistingAnchors(db, sourceVNDB)
	if err != nil {
		return SpineStats{}, err
	}
	displayNorms, err := loadLabelDisplayNorms(db)
	if err != nil {
		return SpineStats{}, err
	}

	plans, st := planSpine(orgs, g, ea, labelNorms, displayNorms)
	if !apply {
		return st, nil
	}

	refs := make([]model.CatalogExternalRef, 0, st.Anchored)
	for _, p := range plans {
		if p.act == spineAnchor {
			refs = append(refs, model.CatalogExternalRef{
				EntityType: model.EntityTypeLabel, EntityID: p.labelID, SourceID: sourceVNDB,
				ExternalID: p.org.extID, LinkKind: model.LinkKindExact, MatchedBy: ruleSpineName,
			})
		}
	}
	pairs := candidatePairs(plans)
	cands := make([]model.CatalogMatchCandidate, 0, len(pairs))
	for _, pair := range pairs {
		cands = append(cands, model.CatalogMatchCandidate{
			EntityType: model.EntityTypeLabel,
			AID:        pair[0], BID: pair[1],
			Reason: model.CandidateReasonNameNormEqual,
			Status: model.CandidateStatusPending,
		})
	}
	if len(refs) > 0 {
		if err := db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).
			CreateInBatches(refs, 1000).Error; err != nil {
			return st, err
		}
	}
	if len(cands) > 0 {
		// DoNothing, not upsert: a pair a human already REJECTED must stay
		// rejected — resurrecting it on every run is exactly the churn the
		// candidate table's "rejected rows are kept forever" rule prevents.
		if err := db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).
			CreateInBatches(cands, 1000).Error; err != nil {
			return st, err
		}
	}
	for _, p := range plans {
		if p.act != spineMint {
			continue
		}
		if err := mintSpineLabel(ctx, db, p.org, spineKind(p.org, g)); err != nil {
			st.Errors++
			st.Minted--
		}
	}
	return st, nil
}

// mintSpineLabel creates the label row, its self anchor and its revision — and
// NO work_label edges. See the banner: a graph node exists on its own warrant,
// and its children's games stay attributed to its children.
func mintSpineLabel(ctx context.Context, db *gorm.DB, o *orgRec, kind int16) error {
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		label := model.CatalogLabel{
			DisplayName:     o.displayName,
			Latin:           o.latin,
			Lang:            o.lang,
			Kind:            kind,
			FieldProvenance: mintProvenance(sourceVNDB, o),
		}
		if err := tx.Create(&label).Error; err != nil {
			return err
		}
		if err := tx.Create(&model.CatalogExternalRef{
			EntityType: model.EntityTypeLabel, EntityID: label.ID, SourceID: sourceVNDB,
			ExternalID: o.extID, LinkKind: model.LinkKindExact, MatchedBy: ruleSpineNew,
		}).Error; err != nil {
			return err
		}
		return tx.Create(&model.CatalogRevision{
			EntityType: model.EntityTypeLabel, EntityID: label.ID, Revision: 1,
			Action: model.RevisionActionImported, Snapshot: labelSnapshot(label), IsMinor: false,
		}).Error
	})
}

// loadGraphFacts reads producers_relations once. `parent` follows the direction
// reading pinned in labelrelations/relationmap.go — a row (id, pid, rel) says
// "the producer at pid is <rel> of the producer at id", so a row with rel in
// (sub, imp) means something hangs UNDER the producer at id.
func loadGraphFacts(db *gorm.DB) (graphFacts, error) {
	g := graphFacts{member: map[string]bool{}, linkable: map[string]bool{}, parent: map[string]bool{}}
	var rows []struct {
		ID       string `gorm:"column:id"`
		PID      string `gorm:"column:pid"`
		Relation string `gorm:"column:relation"`
		AType    string `gorm:"column:a_type"`
		BType    string `gorm:"column:b_type"`
	}
	// The join drops edges whose endpoint is missing from the producers table —
	// an edge to an entity the dump does not describe is not a fact about
	// anything, and it must not make its other end look connected.
	if err := db.Raw(`
		SELECT r.id, r.pid, r.relation, a.type AS a_type, b.type AS b_type
		FROM src_vndb.producers_relations r
		JOIN src_vndb.producers a ON a.id = r.id
		JOIN src_vndb.producers b ON b.id = r.pid`).Scan(&rows).Error; err != nil {
		return g, err
	}
	corporate := func(t string) bool { return t == "co" || t == "ng" }
	for _, r := range rows {
		g.member[r.ID], g.member[r.PID] = true, true
		if corporate(r.BType) {
			g.linkable[r.ID] = true
		}
		if corporate(r.AType) {
			g.linkable[r.PID] = true
		}
		if r.Relation == "sub" || r.Relation == "imp" {
			g.parent[r.ID] = true
		}
	}
	return g, nil
}
