package orglabels

import (
	"testing"

	"api/internal/platform/catalog/model"
)

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
	if got := spineKind(plans[0].org, g); got != model.LabelKindPublisher {
		t.Errorf("kind = %d, want publisher (%d)", got, model.LabelKindPublisher)
	}
}

func TestSpineRefusesToGuessBetweenSameNamedLabels(t *testing.T) {
	norms := map[string][]int64{"nexton": {13231, 41}}
	g, ea := spineFixture(norms, nil)

	plans, st := planSpine([]orgRec{org("p75", "NEXTON", "nexton")}, g, ea, norms, norms)

	if st.Minted != 0 || st.Anchored != 0 {
		t.Fatalf("stats = %+v, want nothing written for an ambiguous name", st)
	}
	if st.Candidates != 1 || st.CandidateRows != 1 {
		t.Fatalf("stats = %+v, want one producer raising one label pair", st)
	}
	if got := plans[0].labels; len(got) != 2 || got[0] != 41 || got[1] != 13231 {
		t.Errorf("labels = %v, want [41 13231] ascending", got)
	}
}

func TestSpineAnchorsALoneNameButNeverASharedOne(t *testing.T) {
	norms := map[string][]int64{"moon": {900}}
	g, ea := spineFixture(norms, nil)

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

	g2, ea2 := spineFixture(norms, map[int64]bool{900: true})
	_, st2 := planSpine([]orgRec{org("p430", "Moon", "moon")}, g2, ea2, norms, norms)
	if st2.Anchored != 0 || st2.SkipClaimed != 1 {
		t.Errorf("stats = %+v, want the pre-claimed label left alone", st2)
	}
}

func TestSpineExcludesPersonsNonMembersAndAnchored(t *testing.T) {
	norms := map[string][]int64{}
	g, ea := spineFixture(norms, nil)
	ea.byExtID["p430"] = model.LinkKindExact

	person := org("p75", "ある人")
	person.canCreate = false

	_, st := planSpine([]orgRec{
		person,
		org("p430", "Liquid", "liquid"),
		org("p999", "Offgraph", "off"),
	}, g, ea, norms, norms)

	if st.Considered != 0 {
		t.Fatalf("stats = %+v, want every row excluded before grading", st)
	}
}

func TestSpineWillNotMintANodeWhoseOnlyNeighboursArePeople(t *testing.T) {
	norms := map[string][]int64{}
	g, ea := spineFixture(norms, nil)
	delete(g.linkable, "p9")

	_, st := planSpine([]orgRec{org("p9", "Founder's Company", "founder")}, g, ea, norms, norms)

	if st.Minted != 0 || st.Considered != 0 {
		t.Fatalf("stats = %+v, want no mint for an unrenderable node", st)
	}
	if st.SkipEdgeless != 1 {
		t.Errorf("SkipEdgeless = %d, want the exclusion counted", st.SkipEdgeless)
	}
}

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
