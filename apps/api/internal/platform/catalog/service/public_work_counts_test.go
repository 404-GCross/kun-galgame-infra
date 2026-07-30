// public_work_counts_test.go — A2-R1 区 B: work_count on the work-record chips
// (labels[] / tags[] / engines[]) and on the list's include=labels block.
// Integration against kun_catalog_test (service_test.go TestMain).
//
// The property under test is not "a number appears" but the invariant
// public_taxonomy.go exists to hold: the number beside a chip equals the total
// the SAME caller gets by following that chip — under both nsfw settings, and
// identically on the detail record and the list row.
package service

import (
	"testing"

	"api/internal/platform/catalog/model"
)

// TestWorkChipCountsMatchTheirLandingPages is the wave's core case for 区 B.
func TestWorkChipCountsMatchTheirLandingPages(t *testing.T) {
	cleanTables(t)
	cleanTagTables(t)
	cleanTaxonomyTables(t)
	svc := newPublicSvc()
	ctx := t.Context()

	// One all-ages work (the record under test) and one r18 work sharing every
	// chip, so each count moves with the nsfw switch. A stub work carries the
	// same chips and must never be counted.
	safe := createWorkX(t, galgameMediumID, model.ContentRatingAllAges, model.WorkStatusLive, "ChipSafe")
	r18 := createWorkX(t, galgameMediumID, model.ContentRatingR18, model.WorkStatusLive, "ChipR18")
	stub := createWorkX(t, galgameMediumID, model.ContentRatingAllAges, model.WorkStatusStub, "ChipStub")
	for i, id := range []int64{safe.ID, r18.ID, stub.ID} {
		claimLive(t, id, int64(9320+i))
	}

	brandID := addWorkLabel(t, safe.ID, "Alcot", model.LabelKindGameBrand, model.WorkLabelKindBrand)
	for _, w := range []int64{r18.ID, stub.ID} {
		if err := testDB.Create(&model.CatalogWorkLabel{
			WorkID: w, LabelID: brandID, Kind: model.WorkLabelKindBrand,
		}).Error; err != nil {
			t.Fatalf("attach label to %d: %v", w, err)
		}
	}

	engineID := createEngine(t, "KiriKiri")
	for _, w := range []int64{safe.ID, r18.ID, stub.ID} {
		attachEngine(t, w, engineID)
	}

	// A MAPPED source tag (canonical landing page) and an UNMAPPED one (none).
	const srcBangumi int16 = 3
	tagID := createCanonicalTag(t, "fantasy", model.TagTierCore, model.TagKindContent)
	if err := testDB.Create(&model.CatalogTagSourceMap{
		SourceID: srcBangumi, SourceName: "ファンタジー", TagID: tagID,
	}).Error; err != nil {
		t.Fatalf("map source tag: %v", err)
	}
	for _, w := range []int64{safe.ID, r18.ID, stub.ID} {
		for _, name := range []string{"ファンタジー", "未映射タグ"} {
			if err := testDB.Create(&model.CatalogWorkTag{
				WorkID: w, Name: name, Count: 1, SourceID: srcBangumi,
			}).Error; err != nil {
				t.Fatalf("work tag %s: %v", name, err)
			}
		}
	}

	for _, tc := range []struct {
		nsfw bool
		want int
	}{{false, 1}, {true, 2}} {
		rec, found, err := svc.WorkDetail(ctx, safe.ID, PublicInclude{}, tc.nsfw, 0)
		if err != nil || !found {
			t.Fatalf("nsfw=%v: WorkDetail = %v, %v", tc.nsfw, found, err)
		}

		if len(rec.Labels) != 1 || rec.Labels[0].WorkCount != tc.want {
			t.Fatalf("nsfw=%v: labels[] = %+v, want one chip with work_count %d", tc.nsfw, rec.Labels, tc.want)
		}
		assertCountMatchesWorksList(t, svc,
			WorksListFilter{Sort: "id", LabelID: brandID, NSFW: tc.nsfw}, rec.Labels[0].WorkCount)

		if len(rec.Engines) != 1 || rec.Engines[0].WorkCount != tc.want {
			t.Fatalf("nsfw=%v: engines[] = %+v, want one chip with work_count %d", tc.nsfw, rec.Engines, tc.want)
		}
		assertCountMatchesWorksList(t, svc,
			WorksListFilter{Sort: "id", EngineID: engineID, NSFW: tc.nsfw}, rec.Engines[0].WorkCount)

		// tags: the mapped row carries the count, the unmapped row has NO key.
		var mapped, unmapped int
		for _, tag := range rec.Tags {
			if tag.CanonicalID == 0 {
				unmapped++
				if tag.WorkCount != nil {
					t.Fatalf("nsfw=%v: unmapped tag %q carries work_count %d — it has no landing page",
						tc.nsfw, tag.Name, *tag.WorkCount)
				}
				continue
			}
			mapped++
			if tag.WorkCount == nil || *tag.WorkCount != tc.want {
				t.Fatalf("nsfw=%v: mapped tag %q work_count = %v, want %d", tc.nsfw, tag.Name, tag.WorkCount, tc.want)
			}
			assertCountMatchesWorksList(t, svc,
				WorksListFilter{Sort: "id", TagIDs: []int64{tagID}, NSFW: tc.nsfw}, *tag.WorkCount)
		}
		if mapped != 1 || unmapped != 1 {
			t.Fatalf("nsfw=%v: tags[] = %+v, want one mapped + one unmapped", tc.nsfw, rec.Tags)
		}

		// The taxonomy DETAIL faces must report the very same numbers.
		tagRec, ok, err := svc.TagDetail(ctx, tagID, false, tc.nsfw, 20, 0)
		if err != nil || !ok {
			t.Fatalf("nsfw=%v: TagDetail = %v, %v", tc.nsfw, ok, err)
		}
		if tagRec.WorkCount != tc.want {
			t.Fatalf("nsfw=%v: tags/{id}.work_count = %d, want %d (same aggregate as the chip)",
				tc.nsfw, tagRec.WorkCount, tc.want)
		}
		engRec, ok, err := svc.EngineDetail(ctx, engineID, tc.nsfw)
		if err != nil || !ok {
			t.Fatalf("nsfw=%v: EngineDetail = %v, %v", tc.nsfw, ok, err)
		}
		if engRec.WorkCount != tc.want {
			t.Fatalf("nsfw=%v: engines/{id}.work_count = %d, want %d", tc.nsfw, engRec.WorkCount, tc.want)
		}

		// And the LIST's include=labels block must agree with the detail record —
		// a chip that says a different number depending on which face rendered it
		// is exactly the failure this shared aggregate exists to prevent.
		page, err := svc.WorksList(ctx, WorksListFilter{
			Sort: "id", NSFW: tc.nsfw, Include: ParseWorksListInclude("labels"),
		}, "", 100)
		if err != nil {
			t.Fatalf("nsfw=%v: WorksList include=labels: %v", tc.nsfw, err)
		}
		var seen bool
		for _, it := range page.Items {
			if it.ID != safe.ID {
				continue
			}
			seen = true
			if len(it.Labels) != 1 || it.Labels[0].WorkCount != rec.Labels[0].WorkCount {
				t.Fatalf("nsfw=%v: list labels = %+v, detail said %d",
					tc.nsfw, it.Labels, rec.Labels[0].WorkCount)
			}
		}
		if !seen {
			t.Fatalf("nsfw=%v: the work under test is missing from the list page", tc.nsfw)
		}
	}
}

// TestWorkCountCountsOnlyLiveClaims is wave 146's core case: the number beside a
// chip counts a work only when that work's claim is LIVE. A draft (submitted,
// not published), a hidden (banned / declined) and an unclaimed registry row
// each carry the identical taxonomy edges here and must each be absent from the
// number — because they are absent from the member list an entity page renders
// (works?<filter>=&claim_state=live). Before this, a tag page promised several
// times the works a reader could actually reach.
//
// All three families are exercised on BOTH the taxonomy lane and the chip,
// because they share one aggregate: a regression in it would be a regression in
// all six faces at once.
func TestWorkCountCountsOnlyLiveClaims(t *testing.T) {
	cleanTables(t)
	cleanTagTables(t)
	cleanTaxonomyTables(t)
	svc := newPublicSvc()
	ctx := t.Context()

	// Four all-ages LIVE galgame works, identical in every respect except the
	// claim axis — so the count can only be moved by that axis.
	live := createWorkX(t, galgameMediumID, model.ContentRatingAllAges, model.WorkStatusLive, "StateLive")
	draft := createWorkX(t, galgameMediumID, model.ContentRatingAllAges, model.WorkStatusLive, "StateDraft")
	hidden := createWorkX(t, galgameMediumID, model.ContentRatingAllAges, model.WorkStatusLive, "StateHidden")
	none := createWorkX(t, galgameMediumID, model.ContentRatingAllAges, model.WorkStatusLive, "StateNone")
	claimLive(t, live.ID, 9401)
	claimWork(t, draft.ID, "galgame_wiki", 9402)
	setClaimState(t, draft.ID, i16(model.ClaimStateDraft))
	claimWork(t, hidden.ID, "galgame_wiki", 9403)
	setClaimState(t, hidden.ID, i16(model.ClaimStateHidden))
	// `none` stays unclaimed: no site, no product_work_id.

	all := []int64{live.ID, draft.ID, hidden.ID, none.ID}

	labelID := addWorkLabel(t, live.ID, "Alcot", model.LabelKindGameBrand, model.WorkLabelKindBrand)
	for _, w := range []int64{draft.ID, hidden.ID, none.ID} {
		if err := testDB.Create(&model.CatalogWorkLabel{
			WorkID: w, LabelID: labelID, Kind: model.WorkLabelKindBrand,
		}).Error; err != nil {
			t.Fatalf("attach label to %d: %v", w, err)
		}
	}

	engineID := createEngine(t, "KiriKiri")
	for _, w := range all {
		attachEngine(t, w, engineID)
	}

	const srcBangumi int16 = 3
	tagID := createCanonicalTag(t, "fantasy", model.TagTierCore, model.TagKindContent)
	if err := testDB.Create(&model.CatalogTagSourceMap{
		SourceID: srcBangumi, SourceName: "ファンタジー", TagID: tagID,
	}).Error; err != nil {
		t.Fatalf("map source tag: %v", err)
	}
	for _, w := range all {
		if err := testDB.Create(&model.CatalogWorkTag{
			WorkID: w, Name: "ファンタジー", Count: 1, SourceID: srcBangumi,
		}).Error; err != nil {
			t.Fatalf("work tag on %d: %v", w, err)
		}
	}

	// ── the three browse lanes ───────────────────────────────────────────────
	labels, err := svc.LabelsList(ctx, LabelsListFilter{}, "", 50)
	if err != nil {
		t.Fatalf("LabelsList: %v", err)
	}
	if len(labels.Items) != 1 || labels.Items[0].WorkCount != 1 {
		t.Fatalf("labels lane = %+v, want one row counting only the live claim", labels.Items)
	}
	assertCountMatchesWorksList(t, svc, WorksListFilter{Sort: "id", LabelID: labelID}, 1)

	tags, err := svc.TagsList(ctx, TagsListFilter{}, "", 50)
	if err != nil {
		t.Fatalf("TagsList: %v", err)
	}
	if len(tags.Items) != 1 || tags.Items[0].WorkCount != 1 {
		t.Fatalf("tags lane = %+v, want work_count 1", tags.Items)
	}
	assertCountMatchesWorksList(t, svc, WorksListFilter{Sort: "id", TagIDs: []int64{tagID}}, 1)

	engines, err := svc.EnginesList(ctx, EnginesListFilter{}, "", 50)
	if err != nil {
		t.Fatalf("EnginesList: %v", err)
	}
	if len(engines.Items) != 1 || engines.Items[0].WorkCount != 1 {
		t.Fatalf("engines lane = %+v, want work_count 1", engines.Items)
	}
	assertCountMatchesWorksList(t, svc, WorksListFilter{Sort: "id", EngineID: engineID}, 1)

	// ── the three detail records ─────────────────────────────────────────────
	labelRec, ok, err := svc.Label(ctx, labelID, false, false, 20, 0)
	if err != nil || !ok {
		t.Fatalf("Label = %v, %v", ok, err)
	}
	if labelRec.WorkCount != 1 {
		t.Fatalf("labels/{id}.work_count = %d, want 1", labelRec.WorkCount)
	}
	tagRec, ok, err := svc.TagDetail(ctx, tagID, false, false, 20, 0)
	if err != nil || !ok {
		t.Fatalf("TagDetail = %v, %v", ok, err)
	}
	if tagRec.WorkCount != 1 {
		t.Fatalf("tags/{id}.work_count = %d, want 1", tagRec.WorkCount)
	}
	engRec, ok, err := svc.EngineDetail(ctx, engineID, false)
	if err != nil || !ok {
		t.Fatalf("EngineDetail = %v, %v", ok, err)
	}
	if engRec.WorkCount != 1 {
		t.Fatalf("engines/{id}.work_count = %d, want 1", engRec.WorkCount)
	}

	// ── the chips on a work record, and the list's include=labels block ──────
	// Read them off the DRAFT work on purpose: a row hidden from every count
	// still renders its own chips, and those chips must report the live number.
	rec, found, err := svc.WorkDetail(ctx, draft.ID, PublicInclude{}, false, 0)
	if err != nil || !found {
		t.Fatalf("WorkDetail = %v, %v", found, err)
	}
	if len(rec.Labels) != 1 || rec.Labels[0].WorkCount != 1 {
		t.Fatalf("labels[] chip = %+v, want work_count 1", rec.Labels)
	}
	if len(rec.Engines) != 1 || rec.Engines[0].WorkCount != 1 {
		t.Fatalf("engines[] chip = %+v, want work_count 1", rec.Engines)
	}
	if len(rec.Tags) != 1 || rec.Tags[0].WorkCount == nil || *rec.Tags[0].WorkCount != 1 {
		t.Fatalf("tags[] chip = %+v, want work_count 1", rec.Tags)
	}

	page, err := svc.WorksList(ctx, WorksListFilter{
		Sort: "id", Include: ParseWorksListInclude("labels"),
	}, "", 100)
	if err != nil {
		t.Fatalf("WorksList include=labels: %v", err)
	}
	if len(page.Items) != len(all) {
		t.Fatalf("the ungated list must still serve all %d rows, got %d", len(all), len(page.Items))
	}
	for _, it := range page.Items {
		if len(it.Labels) != 1 || it.Labels[0].WorkCount != 1 {
			t.Fatalf("list row %d labels = %+v, want work_count 1 on every row", it.ID, it.Labels)
		}
	}

	// Publishing the draft moves the number: the count follows the claim state,
	// it is not frozen at claim time.
	setClaimState(t, draft.ID, i16(model.ClaimStateLive))
	tags, err = svc.TagsList(ctx, TagsListFilter{}, "", 50)
	if err != nil {
		t.Fatalf("TagsList after publish: %v", err)
	}
	if tags.Items[0].WorkCount != 2 {
		t.Fatalf("after publishing the draft, tag work_count = %d, want 2", tags.Items[0].WorkCount)
	}
	assertCountMatchesWorksList(t, svc, WorksListFilter{Sort: "id", TagIDs: []int64{tagID}}, 2)
}
