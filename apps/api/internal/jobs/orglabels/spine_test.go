package orglabels

import (
	"testing"

	"api/internal/platform/catalog/model"
)

// spineFixture is the shape every case below varies one axis of: a graph with
// one parent (p75) and one brand under it, plus the label index.
func spineFixture(labelNorms map[string][]int64, claimed map[int64]bool) (graphFacts, *existingAnchors) {
	g := graphFacts{
		member:   map[string]bool{"p75": true, "p430": true, "p9": true},
		linkable: map[string]bool{"p75": true, "p430": true, "p9": true},
		parent:   map[string]bool{"p75": true},
	}
	ea := &existingAnchors{byExtID: map[string]int16{}, claimedByLabel: map[int64]bool{}}
	for l := range claimed {
		ea.claimedByLabel[l] = true
	}
	_ = labelNorms
	return g, ea
}

func org(extID, name string, norms ...string) orgRec {
	return orgRec{extID: extID, displayName: name, nameNorms: norms, newKind: model.LabelKindGameBrand, canCreate: true}
}

// A parent company publishes nothing under its own name, so no label bears its
// name and none ever will by the co-work route. The spine is the only thing
// that can bring it into being — and it must, or the 58 relation edges hanging
// off it have no second endpoint.
func TestSpineMintsAProducerNoLabelIsNamedAfter(t *testing.T) {
	norms := map[string][]int64{"liquid": {11784}}
	g, ea := spineFixture(norms, nil)

	plans, st := planSpine([]orgRec{org("p75", "ネクストン", "ネクストン", "nexton")}, g, ea, norms, norms)

	if st.Considered != 1 || st.Minted != 1 {
		t.Fatalf("stats = %+v, want one considered and one minted", st)
	}
	if len(plans) != 1 || plans[0].act != spineMint {
		t.Fatalf("plans = %+v, want a single mint", plans)
	}
	// Something hangs under it, so it is a publisher — not another game brand
	// sitting beside the studios it owns.
	if got := spineKind(plans[0].org, g); got != model.LabelKindPublisher {
		t.Errorf("kind = %d, want publisher (%d)", got, model.LabelKindPublisher)
	}
}

// The NEXTON case that opened this wave: two labels already carry the name, one
// minted by the Bangumi import and one by the DLsite import. Which of them (if
// either) IS this producer is a curation judgement, so the pass must refuse to
// pick and hand the pair to the human dedup queue instead.
func TestSpineRefusesToGuessBetweenSameNamedLabels(t *testing.T) {
	norms := map[string][]int64{"nexton": {13231, 41}} // deliberately unsorted
	g, ea := spineFixture(norms, nil)

	plans, st := planSpine([]orgRec{org("p75", "NEXTON", "nexton")}, g, ea, norms, norms)

	if st.Minted != 0 || st.Anchored != 0 {
		t.Fatalf("stats = %+v, want nothing written for an ambiguous name", st)
	}
	if st.Candidates != 1 || st.CandidateRows != 1 {
		t.Fatalf("stats = %+v, want one producer raising one label pair", st)
	}
	// The pair must be ordered — catalog_match_candidate has a a_id < b_id check
	// constraint, so an unsorted set would fail the insert.
	if got := plans[0].labels; len(got) != 2 || got[0] != 41 || got[1] != 13231 {
		t.Errorf("labels = %v, want [41 13231] ascending", got)
	}
}

// A lone same-named label is the producer — unless another producer already
// claimed it. Two companies really do share a name, and letting the second one
// anchor the same label would assert they are one company.
func TestSpineAnchorsALoneNameButNeverASharedOne(t *testing.T) {
	norms := map[string][]int64{"moon": {900}}
	g, ea := spineFixture(norms, nil)

	// p430 sorts before p9 by external id, so it takes the label and p9 does not.
	plans, st := planSpine([]orgRec{org("p430", "Moon", "moon"), org("p9", "Moon", "moon")}, g, ea, norms, norms)

	if st.Anchored != 1 || st.SkipClaimed != 1 || st.Minted != 0 {
		t.Fatalf("stats = %+v, want one anchor and one refused, no mint", st)
	}
	if plans[0].act != spineAnchor || plans[0].labelID != 900 {
		t.Errorf("first plan = %+v, want p430 anchoring label 900", plans[0])
	}
	if plans[1].act != spineSkipClaimed {
		t.Errorf("second plan = %+v, want the shared name refused", plans[1])
	}

	// A label the source already holds an identity anchor on is claimed before
	// the pass starts, so neither producer takes it.
	g2, ea2 := spineFixture(norms, map[int64]bool{900: true})
	_, st2 := planSpine([]orgRec{org("p430", "Moon", "moon")}, g2, ea2, norms, norms)
	if st2.Anchored != 0 || st2.SkipClaimed != 1 {
		t.Errorf("stats = %+v, want the pre-claimed label left alone", st2)
	}
}

// The three exclusions, each for its own reason: a person is not a label; a
// producer outside the graph has no warrant for existence-without-works; an
// already-anchored producer is done.
func TestSpineExcludesPersonsNonMembersAndAnchored(t *testing.T) {
	norms := map[string][]int64{}
	g, ea := spineFixture(norms, nil)
	ea.byExtID["p430"] = model.LinkKindExact

	person := org("p75", "ある人")
	person.canCreate = false // VNDB type=in

	_, st := planSpine([]orgRec{
		person,
		org("p430", "Liquid", "liquid"), // already anchored
		org("p999", "Offgraph", "off"),  // not in producers_relations
	}, g, ea, norms, norms)

	if st.Considered != 0 {
		t.Fatalf("stats = %+v, want every row excluded before grading", st)
	}
}

// A producer whose every upstream relation points at an individual has nothing
// renderable: persons never enter the label graph, so those edges can never
// land, and minting it would create the empty page this wave exists to avoid.
// It is counted, not silently dropped.
func TestSpineWillNotMintANodeWhoseOnlyNeighboursArePeople(t *testing.T) {
	norms := map[string][]int64{}
	g, ea := spineFixture(norms, nil)
	delete(g.linkable, "p9") // in the graph, but only ever related to a person

	_, st := planSpine([]orgRec{org("p9", "Founder's Company", "founder")}, g, ea, norms, norms)

	if st.Minted != 0 || st.Considered != 0 {
		t.Fatalf("stats = %+v, want no mint for an unrenderable node", st)
	}
	if st.SkipEdgeless != 1 {
		t.Errorf("SkipEdgeless = %d, want the exclusion counted", st.SkipEdgeless)
	}
}

// Two producers can name-match the same pair of labels. The queue must receive
// that pair ONCE — and the dry run must report the same number the apply run
// inserts, or the receipt lies about the size of the human worklist.
func TestSpineRaisesEachLabelPairOnce(t *testing.T) {
	norms := map[string][]int64{"nexton": {41, 13231}, "ネクストン": {41, 13231}}
	g, ea := spineFixture(norms, nil)

	plans, st := planSpine([]orgRec{
		org("p430", "NEXTON", "nexton"),
		org("p9", "ネクストン", "ネクストン"),
	}, g, ea, norms, norms)

	if st.Candidates != 2 {
		t.Fatalf("Candidates = %d, want both producers routed", st.Candidates)
	}
	if st.CandidateRows != 1 {
		t.Errorf("CandidateRows = %d, want the shared pair counted once", st.CandidateRows)
	}
	if got := candidatePairs(plans); len(got) != 1 || got[0] != [2]int64{41, 13231} {
		t.Errorf("pairs = %v, want a single ascending pair", got)
	}
}

// The production NEXTON shape, which is why the two indices exist. Four labels
// answer to the name: 41 and 13231 are actually CALLED NEXTON, while PSYCHO
// (12784) and RaSeN (12857) merely carry it as an alias — DLsite's way of saying
// "published by NEXTON". A merge proposal asserts identity, so only the two that
// bear the name may be nominated; suggesting PSYCHO be merged into NEXTON is a
// wrong answer handed to a human as a recommendation.
func TestSpineNominatesOnlyLabelsThatBearTheNameThemselves(t *testing.T) {
	all := map[string][]int64{"ネクストン": {41, 12784, 12857, 13231}, "nexton": {41, 13231}}
	display := map[string][]int64{"ネクストン": {41, 13231}, "nexton": {41, 13231}}
	g, ea := spineFixture(all, nil)

	plans, st := planSpine([]orgRec{org("p75", "ネクストン", "ネクストン")}, g, ea, all, display)

	if st.Candidates != 1 || st.CandidateRows != 1 {
		t.Fatalf("stats = %+v, want exactly one pair nominated", st)
	}
	if got := plans[0].labels; len(got) != 2 || got[0] != 41 || got[1] != 13231 {
		t.Errorf("nominated %v, want only the labels named NEXTON", got)
	}
}

// …but when EVERY match is an alias, there is nothing to nominate and nothing
// safe to assert. Minting anyway would add yet another label answering to a name
// that several already answer to — the exact duplication this wave exists to
// stop making worse.
func TestSpineNeitherMintsNorAssertsOnPureAliasNoise(t *testing.T) {
	all := map[string][]int64{"ネクストン": {12784, 12857}}
	display := map[string][]int64{}
	g, ea := spineFixture(all, nil)

	_, st := planSpine([]orgRec{org("p75", "ネクストン", "ネクストン")}, g, ea, all, display)

	if st.Minted != 0 || st.Anchored != 0 || st.Candidates != 0 {
		t.Fatalf("stats = %+v, want no write of any kind", st)
	}
	if st.SkipAliasOnly != 1 {
		t.Errorf("SkipAliasOnly = %d, want the refusal counted", st.SkipAliasOnly)
	}
}
