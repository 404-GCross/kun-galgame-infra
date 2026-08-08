package orglabels

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"sort"

	"api/internal/platform/catalog/model"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// orgRec is one source organization, as loaded by a per-source adapter. works
// is W(O) (distinct catalog work ids); nameNorms are the NFKC folds used for
// name equality. displayName/latin/lang/newKind describe the label to mint if
// the org has no matching label; canCreate is false for VNDB persons (type=in),
// which may anchor an existing label but never mint one.
type orgRec struct {
	extID       string
	works       []int64
	nameNorms   []string
	displayName string
	latin       *string
	lang        string
	newKind     int16
	canCreate   bool
}

// resKind is the outcome of grading one org.
type resKind int

const (
	resAnchorExisting  resKind = iota // anchor to an existing label (tier decides exact/probable)
	resNewLabel                       // mint a new label + edges
	resSkipNoMatch                    // no gradeable label, no name match, cannot mint
	resSkipAmbiguous                  // name matches several distinct labels (name-only)
	resSkipUngradeable                // shares exactly one work with label(s) but no name — not gradeable
)

// gradeResult is the per-org decision, before the one-anchor-per-label pass.
type gradeResult struct {
	kind    resKind
	labelID int64  // target for resAnchorExisting
	tier    int16  // LinkKindExact(0) / LinkKindProbable(1) for resAnchorExisting
	rule    string // matched_by
	share   int    // |W(O)∩W(target)| — sort key for the greedy claim
}

// grader carries the source-agnostic indices + this source's rule strings.
type grader struct {
	workLabels map[int64][]int64
	labelNorms map[string][]int64
	rules      ruleSet
}

// grade applies the refs/proj/83 裁定4/5 rules to one org. It is pure (no
// writes, no claim state); the one-anchor-per-label enforcement is a later
// greedy pass over the results.
func (g *grader) grade(o *orgRec) gradeResult {
	// Co-occurrence tally: label → shared work count.
	share := make(map[int64]int)
	for _, w := range o.works {
		for _, l := range g.workLabels[w] {
			share[l]++
		}
	}
	// Name-matched labels (dedup).
	nameHit := make(map[int64]bool)
	for _, n := range o.nameNorms {
		if n == "" {
			continue
		}
		for _, l := range g.labelNorms[n] {
			nameHit[l] = true
		}
	}

	// Exact-eligible: s>=2, or s==1 with a name match. Pick the best by
	// (share desc, name-match, id asc) so the strongest signal wins.
	bestLabel := int64(0)
	bestShare := 0
	bestName := false
	for l, s := range share {
		eligible := s >= 2 || (s == 1 && nameHit[l])
		if !eligible {
			continue
		}
		if better(s, nameHit[l], l, bestShare, bestName, bestLabel) {
			bestLabel, bestShare, bestName = l, s, nameHit[l]
		}
	}
	if bestLabel != 0 {
		rule := g.rules.coworks
		if bestShare == 1 {
			rule = g.rules.coworkName
		}
		return gradeResult{kind: resAnchorExisting, labelID: bestLabel, tier: 0, rule: rule, share: bestShare}
	}

	// Name-only probable: a name match with NO shared work (s==0). Require a
	// single distinct label — several equal-named labels are ambiguous.
	var nameOnly []int64
	for l := range nameHit {
		if share[l] == 0 {
			nameOnly = append(nameOnly, l)
		}
	}
	if len(nameOnly) == 1 {
		return gradeResult{kind: resAnchorExisting, labelID: nameOnly[0], tier: 1, rule: g.rules.nameOnly, share: 0}
	}
	if len(nameOnly) > 1 {
		return gradeResult{kind: resSkipAmbiguous}
	}

	// No gradeable label and no name match. If the org shares works with some
	// label (all s==1, no name), it is ungradeable — do NOT mint (the works are
	// already attributed). Otherwise mint a fresh label when allowed + non-empty.
	if len(share) > 0 {
		return gradeResult{kind: resSkipUngradeable}
	}
	if o.canCreate && len(o.works) > 0 {
		return gradeResult{kind: resNewLabel}
	}
	return gradeResult{kind: resSkipNoMatch}
}

// better reports whether candidate (s,name,id) beats the current best.
func better(s int, name bool, id int64, bestS int, bestName bool, bestID int64) bool {
	if s != bestS {
		return s > bestS
	}
	if name != bestName {
		return name // name match wins ties
	}
	return bestID == 0 || id < bestID
}

// AnchorStats is the per-run tally (dry and apply produce the same plan; the
// *_written fields count actual inserts, apply only).
type AnchorStats struct {
	Orgs            int
	Already         int
	AnchorsExact    int
	AnchorsProbable int
	NewLabels       int
	NewEdges        int
	Conflict        int
	SkipNoMatch     int
	SkipAmbiguous   int
	SkipUngradeable int
	VNDBInAnchored  int // VNDB type=in orgs that anchored an existing label
	Errors          int
	// Spine is the corporate-graph pass — the producers this grader cannot
	// reach because they publish nothing under their own name. See spine.go.
	Spine SpineStats
}

func (s *AnchorStats) add(o AnchorStats) {
	s.Orgs += o.Orgs
	s.Already += o.Already
	s.AnchorsExact += o.AnchorsExact
	s.AnchorsProbable += o.AnchorsProbable
	s.NewLabels += o.NewLabels
	s.NewEdges += o.NewEdges
	s.Conflict += o.Conflict
	s.SkipNoMatch += o.SkipNoMatch
	s.SkipAmbiguous += o.SkipAmbiguous
	s.SkipUngradeable += o.SkipUngradeable
	s.VNDBInAnchored += o.VNDBInAnchored
	s.Errors += o.Errors
	s.Spine.add(o.Spine)
}

// RunAnchor opens the pools and executes the E2a anchoring wave for the
// requested source(s).
func RunAnchor(ctx context.Context, opts Opts) (AnchorStats, error) {
	needEG := opts.Source == "eg" || opts.Source == "all"
	catalog, eg, err := openPools(opts, needEG)
	if err != nil {
		return AnchorStats{}, err
	}
	return anchorAll(ctx, catalog, eg, opts.Source, opts.Limit, opts.Apply)
}

// maxAnchorPasses caps the fixpoint loop (safety; convergence is normally 2).
const maxAnchorPasses = 6

// anchorAll is the pool-agnostic core (tests inject the catalog handle as eg
// too, since the EG stand-in tables live in the same test database).
//
// It runs the per-source pass to a FIXPOINT: a label minted by one source
// becomes a name-match target for a later source, so a single pass leaves a
// cross-source tail. Reloading the label indices and repeating until a pass
// writes nothing makes ONE --apply converge, so a second invocation is a true
// no-op (the 二遍零写 guarantee). The reported write counters are the converged
// totals; the skip/already/conflict breakdown is the first pass (the plan that
// matches the dry run). A dry run makes no writes, so it never loops.
func anchorAll(ctx context.Context, catalog, eg *gorm.DB, source string, limit int, apply bool) (AnchorStats, error) {
	var total AnchorStats
	for pass := 1; pass <= maxAnchorPasses; pass++ {
		workLabels, err := loadLabelWorks(catalog)
		if err != nil {
			return total, fmt.Errorf("load label works: %w", err)
		}
		labelNorms, err := loadLabelNorms(catalog)
		if err != nil {
			return total, fmt.Errorf("load label norms: %w", err)
		}

		var pt AnchorStats
		for _, src := range wantedSources(source) {
			orgs, rules, srcID, err := loadSource(catalog, eg, src, limit)
			if err != nil {
				return total, fmt.Errorf("load %s orgs: %w", src, err)
			}
			g := &grader{workLabels: workLabels, labelNorms: labelNorms, rules: rules}
			st, err := anchorSource(ctx, catalog, g, orgs, srcID, apply)
			if err != nil {
				return total, fmt.Errorf("anchor %s: %w", src, err)
			}
			slog.Info("org-label anchor source done", "source", src, "pass", pass, "apply", apply,
				"orgs", st.Orgs, "already", st.Already, "exact", st.AnchorsExact,
				"probable", st.AnchorsProbable, "new_labels", st.NewLabels, "new_edges", st.NewEdges,
				"conflict", st.Conflict, "skip_no_match", st.SkipNoMatch,
				"skip_ambiguous", st.SkipAmbiguous, "skip_ungradeable", st.SkipUngradeable,
				"vndb_in_anchored", st.VNDBInAnchored)
			pt.add(st)
		}

		// The spine runs LAST in the iteration, over what the co-work grader
		// could not reach — see spine.go. It is a VNDB-only lane (it is the only
		// upstream that publishes company structure at all), so a run scoped to
		// another source skips it.
		if slices.Contains(wantedSources(source), "vndb") {
			sp, err := runSpine(ctx, catalog, labelNorms, limit, apply)
			if err != nil {
				return total, fmt.Errorf("spine pass: %w", err)
			}
			slog.Info("org-label spine pass done", "pass", pass, "apply", apply,
				"considered", sp.Considered, "minted", sp.Minted, "anchored", sp.Anchored,
				"candidates", sp.Candidates, "candidate_rows", sp.CandidateRows,
				"skip_claimed", sp.SkipClaimed, "errors", sp.Errors)
			pt.Spine = sp
		}

		if pass == 1 {
			total = pt // the first pass is the plan (matches the dry run)
		} else {
			// Later passes contribute only their (small) additional writes.
			total.AnchorsExact += pt.AnchorsExact
			total.AnchorsProbable += pt.AnchorsProbable
			total.NewLabels += pt.NewLabels
			total.NewEdges += pt.NewEdges
			total.VNDBInAnchored += pt.VNDBInAnchored
			total.Errors += pt.Errors
			total.Spine.add(pt.Spine)
		}
		// Dry runs never write, so the fixpoint is a single pass by construction.
		// The spine's writes count towards convergence too: a label it mints is a
		// name-match target for the next iteration's co-work pass.
		if !apply || pt.AnchorsExact+pt.AnchorsProbable+pt.NewLabels+pt.Spine.writes() == 0 {
			break
		}
	}
	return total, nil
}

// wantedSources expands the --source selector into the ordered source list.
func wantedSources(sel string) []string {
	if sel == "all" {
		return []string{"vndb", "bangumi", "eg"}
	}
	return []string{sel}
}

// anchorSource grades every org, resolves the one-anchor-per-label claim
// greedily (strongest share first), then writes.
func anchorSource(ctx context.Context, db *gorm.DB, g *grader, orgs []orgRec, source int16, apply bool) (AnchorStats, error) {
	ea, err := loadExistingAnchors(db, source)
	if err != nil {
		return AnchorStats{}, fmt.Errorf("load existing anchors: %w", err)
	}
	st := AnchorStats{Orgs: len(orgs)}

	type planItem struct {
		org *orgRec
		res gradeResult
	}
	var anchors, newLabels []planItem
	for i := range orgs {
		o := &orgs[i]
		if _, done := ea.byExtID[o.extID]; done {
			st.Already++
			continue
		}
		res := g.grade(o)
		switch res.kind {
		case resAnchorExisting:
			anchors = append(anchors, planItem{o, res})
		case resNewLabel:
			newLabels = append(newLabels, planItem{o, res})
		case resSkipAmbiguous:
			st.SkipAmbiguous++
		case resSkipUngradeable:
			st.SkipUngradeable++
		default:
			st.SkipNoMatch++
		}
	}

	// Greedy one-anchor-per-label: strongest share first, then exact before
	// probable, then external_id for stability. claimedLabels is seeded with the
	// labels this source already exact-anchored so re-runs never double-claim.
	sort.Slice(anchors, func(i, j int) bool {
		a, b := anchors[i].res, anchors[j].res
		if a.share != b.share {
			return a.share > b.share
		}
		if a.tier != b.tier {
			return a.tier < b.tier // exact(0) before probable(1)
		}
		return anchors[i].org.extID < anchors[j].org.extID
	})
	claimed := make(map[int64]bool, len(ea.claimedByLabel))
	for l := range ea.claimedByLabel {
		claimed[l] = true
	}

	refs := make([]model.CatalogExternalRef, 0, len(anchors))
	for _, it := range anchors {
		if claimed[it.res.labelID] {
			st.Conflict++
			slog.Debug("org-label anchor conflict — label already claimed by this source",
				"source", source, "ext_id", it.org.extID, "label_id", it.res.labelID)
			continue
		}
		claimed[it.res.labelID] = true
		refs = append(refs, model.CatalogExternalRef{
			EntityType: model.EntityTypeLabel, EntityID: it.res.labelID, SourceID: source,
			ExternalID: it.org.extID, LinkKind: it.res.tier, MatchedBy: it.res.rule,
		})
		if it.res.tier == 0 {
			st.AnchorsExact++
		} else {
			st.AnchorsProbable++
		}
		if !it.org.canCreate {
			st.VNDBInAnchored++
		}
	}
	if apply && len(refs) > 0 {
		// Batch insert; ON CONFLICT DO NOTHING is the idempotency backstop (the
		// exact-tier anti-squat index + the composite PK).
		res := db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).CreateInBatches(refs, 1000)
		if res.Error != nil {
			return st, fmt.Errorf("batch insert anchors: %w", res.Error)
		}
	}

	for _, it := range newLabels {
		edges := len(it.org.works)
		if apply {
			n, err := mintLabel(ctx, db, source, it.org)
			if err != nil {
				st.Errors++
				slog.Warn("org-label mint failed", "source", source, "ext_id", it.org.extID, "error", err)
				continue
			}
			edges = n
		}
		st.NewLabels++
		st.NewEdges += edges
	}
	return st, nil
}
