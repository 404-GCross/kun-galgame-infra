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
//     brand).
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
// any work: whether it appears in the graph at all, and whether anything hangs
// UNDER it (which is what makes it a publisher rather than a brand).
type graphFacts struct {
	member map[string]bool
	parent map[string]bool
}

// spineAct is the planner's verdict for one producer.
type spineAct int

const (
	spineMint        spineAct = iota // no same-named label exists — bring the node into being
	spineAnchor                      // exactly one free same-named label — it IS this producer
	spineCandidate                   // several same-named labels — hand the pair to the dedup queue
	spineSkipClaimed                 // the one same-named label is already another producer's identity
)

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
	CandidateRows int // label pairs actually written (a producer can raise several)
	SkipClaimed   int // same-named label already claimed by another producer
	Errors        int
}

func (s *SpineStats) add(o SpineStats) {
	s.Considered += o.Considered
	s.Minted += o.Minted
	s.Anchored += o.Anchored
	s.Candidates += o.Candidates
	s.CandidateRows += o.CandidateRows
	s.SkipClaimed += o.SkipClaimed
	s.Errors += o.Errors
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
func planSpine(orgs []orgRec, g graphFacts, ea *existingAnchors, labelNorms map[string][]int64) ([]spinePlan, SpineStats) {
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
		st.Considered++

		hit := make(map[int64]bool)
		for _, n := range o.nameNorms {
			if n == "" {
				continue
			}
			for _, l := range labelNorms[n] {
				hit[l] = true
			}
		}
		labels := make([]int64, 0, len(hit))
		for l := range hit {
			labels = append(labels, l)
		}
		slices.Sort(labels)

		switch {
		case len(labels) == 0:
			out = append(out, spinePlan{act: spineMint, org: o})
			st.Minted++
		case len(labels) == 1 && !claimed[labels[0]]:
			claimed[labels[0]] = true
			out = append(out, spinePlan{act: spineAnchor, org: o, labelID: labels[0]})
			st.Anchored++
		case len(labels) == 1:
			out = append(out, spinePlan{act: spineSkipClaimed, org: o, labelID: labels[0]})
			st.SkipClaimed++
		default:
			out = append(out, spinePlan{act: spineCandidate, org: o, labels: labels})
			st.Candidates++
			st.CandidateRows += len(labels) * (len(labels) - 1) / 2
		}
	}
	return out, st
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

	plans, st := planSpine(orgs, g, ea, labelNorms)
	if !apply {
		return st, nil
	}

	refs := make([]model.CatalogExternalRef, 0, st.Anchored)
	cands := make([]model.CatalogMatchCandidate, 0, st.CandidateRows)
	for _, p := range plans {
		switch p.act {
		case spineAnchor:
			refs = append(refs, model.CatalogExternalRef{
				EntityType: model.EntityTypeLabel, EntityID: p.labelID, SourceID: sourceVNDB,
				ExternalID: p.org.extID, LinkKind: model.LinkKindExact, MatchedBy: ruleSpineName,
			})
		case spineCandidate:
			for i := 0; i < len(p.labels); i++ {
				for j := i + 1; j < len(p.labels); j++ {
					cands = append(cands, model.CatalogMatchCandidate{
						EntityType: model.EntityTypeLabel,
						AID:        p.labels[i], BID: p.labels[j],
						Reason: model.CandidateReasonNameNormEqual,
						Status: model.CandidateStatusPending,
					})
				}
			}
		}
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
	g := graphFacts{member: map[string]bool{}, parent: map[string]bool{}}
	var rows []struct {
		ID       string `gorm:"column:id"`
		PID      string `gorm:"column:pid"`
		Relation string `gorm:"column:relation"`
	}
	if err := db.Raw(`SELECT id, pid, relation FROM src_vndb.producers_relations`).Scan(&rows).Error; err != nil {
		return g, err
	}
	for _, r := range rows {
		g.member[r.ID] = true
		g.member[r.PID] = true
		if r.Relation == "sub" || r.Relation == "imp" {
			g.parent[r.ID] = true
		}
	}
	return g, nil
}
