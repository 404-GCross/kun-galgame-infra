// public_release_labels_test.go — wave 200's read half: the company chips that
// hang off a RELEASE rather than a work.
//
// The fixture is あまいろショコラータ's shape, the case the wave was raised for:
// one work, three editions, and companies that are facts about ONE edition each.
// If these ever collapse back onto the work, the assertions below stop
// distinguishing the ports from the original — which is exactly the failure the
// storage grain was changed to end.
package service

import (
	"context"
	"encoding/json"
	"testing"

	"api/internal/platform/catalog/model"
)

// addReleaseLabel attaches an existing label to a release under one capacity.
func addReleaseLabel(t *testing.T, releaseID, labelID int64, kind int16) {
	t.Helper()
	if err := testDB.Create(&model.CatalogReleaseLabel{
		ReleaseID: releaseID, LabelID: labelID, Kind: kind,
	}).Error; err != nil {
		t.Fatalf("create release label edge: %v", err)
	}
}

// createLabel makes a bare label with no work-level edge — release attribution
// must not depend on the company also being attributed to the work.
func createLabel(t *testing.T, displayName string, kind int16) int64 {
	t.Helper()
	l := &model.CatalogLabel{DisplayName: displayName, Kind: kind}
	if err := testDB.Create(l).Error; err != nil {
		t.Fatalf("create label %s: %v", displayName, err)
	}
	return l.ID
}

func TestReleaseLabelsAreScopedToTheirEdition(t *testing.T) {
	cleanTables(t)
	ctx := context.Background()
	svc := newPublicSvcCDN()

	w := createWorkX(t, galgameMediumID, model.ContentRatingAllAges, model.WorkStatusLive, "あまいろショコラータ")
	original := createRelease(t, w.ID, 2019, 4, 26)
	port := createRelease(t, w.ID, 2021, 3, 25)
	undated := createRelease(t, w.ID, 2020, 1, 1) // attribution-free, see below

	cabbage := createLabel(t, "きゃべつそふと", model.LabelKindGameBrand)
	hunex := createLabel(t, "HuneX", model.LabelKindGameBrand)

	// The original: one company in two capacities — the collapse case.
	addReleaseLabel(t, original.ID, cabbage, model.WorkLabelKindDeveloper)
	addReleaseLabel(t, original.ID, cabbage, model.WorkLabelKindPublisher)
	// The port: developed by the same studio, published by somebody else.
	addReleaseLabel(t, port.ID, cabbage, model.WorkLabelKindDeveloper)
	addReleaseLabel(t, port.ID, hunex, model.WorkLabelKindPublisher)

	rec, found, err := svc.WorkDetail(ctx, w.ID, PublicInclude{}, false, 0)
	if err != nil || !found {
		t.Fatalf("WorkDetail = %v, %v", found, err)
	}

	byID := map[int64]int{}
	for i, r := range rec.Releases {
		byID[r.ID] = i
	}
	if len(byID) != 3 {
		t.Fatalf("releases[] = %d rows, want 3", len(rec.Releases))
	}

	// ── the original: ONE entry, both capacities ────────────────────────────
	got := rec.Releases[byID[original.ID]].Labels
	if len(got) != 1 {
		t.Fatalf("original edition labels[] = %+v, want one company", got)
	}
	if got[0].ID != cabbage || got[0].DisplayName != "きゃべつそふと" {
		t.Fatalf("original edition company = %+v, want きゃべつそふと", got[0])
	}
	if len(got[0].Kinds) != 2 || got[0].Kinds[0] != "developer" || got[0].Kinds[1] != "publisher" {
		t.Fatalf("kinds = %v, want [developer publisher] — the collapse is not shared with the work grain", got[0].Kinds)
	}
	if got[0].Kind != "developer" {
		t.Fatalf("primary kind = %q, want developer (who made it identifies it better)", got[0].Kind)
	}

	// ── the port: the OTHER publisher, and only on this edition ─────────────
	got = rec.Releases[byID[port.ID]].Labels
	if len(got) != 2 {
		t.Fatalf("port labels[] = %+v, want two companies", got)
	}
	var sawHuneX bool
	for _, l := range got {
		if l.ID == hunex {
			sawHuneX = true
			if l.Kind != "publisher" {
				t.Fatalf("HuneX kind = %q, want publisher", l.Kind)
			}
		}
	}
	if !sawHuneX {
		t.Fatal("the port's publisher is missing from its own edition")
	}
	// The point of the whole wave: HuneX is NOT on the original.
	for _, l := range rec.Releases[byID[original.ID]].Labels {
		if l.ID == hunex {
			t.Fatal("the port's publisher leaked onto the original edition — the grain collapsed")
		}
	}

	// ── an edition with no known company is [], never null ──────────────────
	if labels := rec.Releases[byID[undated.ID]].Labels; labels == nil || len(labels) != 0 {
		t.Fatalf("unattributed edition labels[] = %+v, want an empty slice", labels)
	}
	blob, err := json.Marshal(rec.Releases[byID[undated.ID]])
	if err != nil {
		t.Fatalf("marshal release: %v", err)
	}
	var wire map[string]json.RawMessage
	if err := json.Unmarshal(blob, &wire); err != nil {
		t.Fatalf("unmarshal release: %v", err)
	}
	if string(wire["labels"]) != "[]" {
		t.Fatalf("labels on the wire = %s, want [] (a missing key reads as a parse failure)", wire["labels"])
	}
}

// TestReleaseLabelsDropSoftDeletedLabels: an attribution edge outlives its
// label being merged away, and projecting it anyway renders the merged-away
// twin beside the survivor. Same rule read_labels.go holds at the work grain.
func TestReleaseLabelsDropSoftDeletedLabels(t *testing.T) {
	cleanTables(t)
	ctx := context.Background()
	svc := newPublicSvcCDN()

	w := createWorkX(t, galgameMediumID, model.ContentRatingAllAges, model.WorkStatusLive, "Merged Twin")
	rel := createRelease(t, w.ID, 2022, 8, 1)
	survivor := createLabel(t, "Survivor", model.LabelKindGameBrand)
	merged := createLabel(t, "Survivor", model.LabelKindGameBrand)
	addReleaseLabel(t, rel.ID, survivor, model.WorkLabelKindPublisher)
	addReleaseLabel(t, rel.ID, merged, model.WorkLabelKindPublisher)

	if err := testDB.Delete(&model.CatalogLabel{}, merged).Error; err != nil {
		t.Fatalf("soft-delete label: %v", err)
	}

	rec, found, err := svc.WorkDetail(ctx, w.ID, PublicInclude{}, false, 0)
	if err != nil || !found {
		t.Fatalf("WorkDetail = %v, %v", found, err)
	}
	got := rec.Releases[0].Labels
	if len(got) != 1 || got[0].ID != survivor {
		t.Fatalf("labels[] = %+v, want only the surviving label %d", got, survivor)
	}
}

// TestReleaseLabelWorkCountMatchesTheChipTarget holds the A2-R1 invariant one
// grain down: the number beside a chip must equal what the caller gets by
// following it. The chip links to the LABEL's works, so it is the label's
// work_count — the same number the work-level chip carries.
func TestReleaseLabelWorkCountMatchesTheChipTarget(t *testing.T) {
	cleanTables(t)
	ctx := context.Background()
	svc := newPublicSvcCDN()

	w := createWorkX(t, galgameMediumID, model.ContentRatingAllAges, model.WorkStatusLive, "Counted")
	rel := createRelease(t, w.ID, 2023, 5, 5)
	// Two works under one brand, so a count of 1 cannot pass by coincidence.
	other := createWorkX(t, galgameMediumID, model.ContentRatingAllAges, model.WorkStatusLive, "Counted Sibling")
	// The chip's landing page carries claim_state=live (taxonomyLiveClaim), so
	// an unclaimed work is legitimately counted as zero — claim both, or this
	// test would assert against the wrong population.
	claimLive(t, w.ID, 9401)
	claimLive(t, other.ID, 9402)
	brand := addWorkLabel(t, w.ID, "Counted Brand", model.LabelKindGameBrand, model.WorkLabelKindBrand)
	if err := testDB.Create(&model.CatalogWorkLabel{
		WorkID: other.ID, LabelID: brand, Kind: model.WorkLabelKindBrand,
	}).Error; err != nil {
		t.Fatalf("attach label to sibling: %v", err)
	}
	addReleaseLabel(t, rel.ID, brand, model.WorkLabelKindPublisher)

	rec, found, err := svc.WorkDetail(ctx, w.ID, PublicInclude{}, false, 0)
	if err != nil || !found {
		t.Fatalf("WorkDetail = %v, %v", found, err)
	}
	got := rec.Releases[0].Labels
	if len(got) != 1 {
		t.Fatalf("release labels[] = %+v, want one chip", got)
	}
	if got[0].WorkCount != 2 {
		t.Fatalf("release chip work_count = %d, want 2", got[0].WorkCount)
	}
	assertCountMatchesWorksList(t, svc, WorksListFilter{Sort: "id", LabelID: brand}, got[0].WorkCount)
	// And it agrees with the work-level chip for the same company, which is the
	// reason both grains go through fillWorkLabelCounts.
	if len(rec.Labels) != 1 || rec.Labels[0].WorkCount != got[0].WorkCount {
		t.Fatalf("work chip %+v disagrees with the release chip %+v", rec.Labels, got[0])
	}
}

// TestReleaseFeedCarriesEditionLabels: the timeline item promises
// PublicRelease's shape key for key, and "who is shipping this port" is the
// question a release-grain feed exists to answer.
func TestReleaseFeedCarriesEditionLabels(t *testing.T) {
	cleanTables(t)
	ctx := context.Background()
	svc := newPublicSvcCDN()

	w := createWorkX(t, galgameMediumID, model.ContentRatingAllAges, model.WorkStatusLive, "Feed Work")
	claimLive(t, w.ID, 9403)
	rel := createRelease(t, w.ID, 2024, 2, 2)
	pub := createLabel(t, "Feed Publisher", model.LabelKindGameBrand)
	addReleaseLabel(t, rel.ID, pub, model.WorkLabelKindPublisher)
	// The chip counts the LABEL's works, and this label reaches one.
	if err := testDB.Create(&model.CatalogWorkLabel{
		WorkID: w.ID, LabelID: pub, Kind: model.WorkLabelKindPublisher,
	}).Error; err != nil {
		t.Fatalf("attach feed publisher to the work: %v", err)
	}
	bare := createRelease(t, w.ID, 2024, 3, 3)

	page, err := svc.ReleaseFeed(ctx, ReleaseFeedFilter{}, "", 20)
	if err != nil {
		t.Fatalf("ReleaseFeed: %v", err)
	}
	if len(page.Items) != 2 {
		t.Fatalf("feed = %d items, want 2", len(page.Items))
	}
	for _, it := range page.Items {
		switch it.ID {
		case rel.ID:
			if len(it.Labels) != 1 || it.Labels[0].ID != pub {
				t.Fatalf("feed item labels[] = %+v, want the edition's publisher", it.Labels)
			}
			if it.Labels[0].WorkCount != 1 {
				t.Fatalf("feed chip work_count = %d, want 1", it.Labels[0].WorkCount)
			}
		case bare.ID:
			if it.Labels == nil || len(it.Labels) != 0 {
				t.Fatalf("unattributed feed item labels[] = %+v, want an empty slice", it.Labels)
			}
		}
	}
}
