// public_taxonomy_test.go — A2-1b: the labels / tags / engines browse lanes,
// the engine detail record, the works-list engine_id filter, the tag intro
// channel and the screenshot metadata enrichment. Integration against
// kun_catalog_test (service_test.go TestMain).
package service

import (
	"context"
	"testing"

	"api/internal/platform/catalog/model"
)

// cleanTaxonomyTables truncates the facet tables this suite writes that
// cleanTables / cleanTagTables do not cover.
func cleanTaxonomyTables(t *testing.T) {
	t.Helper()
	for _, table := range []string{
		"catalog_work_engine", "catalog_engine", "catalog_tag_intro",
		"catalog_work_cover", "catalog_work_screenshot",
	} {
		if err := testDB.Exec("TRUNCATE " + table + " RESTART IDENTITY CASCADE").Error; err != nil {
			t.Fatalf("truncate %s: %v", table, err)
		}
	}
}

// createEngine inserts one engine row and returns its id.
func createEngine(t *testing.T, name string) int64 {
	t.Helper()
	e := &model.CatalogEngine{Name: name, Description: "", Aliases: []byte("[]")}
	if err := testDB.Create(e).Error; err != nil {
		t.Fatalf("create engine %s: %v", name, err)
	}
	return e.ID
}

func attachEngine(t *testing.T, workID, engineID int64) {
	t.Helper()
	if err := testDB.Create(&model.CatalogWorkEngine{
		WorkID: workID, EngineID: engineID, SourceID: srcVNDB,
	}).Error; err != nil {
		t.Fatalf("attach engine: %v", err)
	}
}

// createCanonicalTag inserts a canonical tag and returns its id.
func createCanonicalTag(t *testing.T, name string, tier, kind int16) int64 {
	t.Helper()
	tag := &model.CatalogTag{Name: name, Tier: tier, Kind: kind}
	if err := testDB.Create(tag).Error; err != nil {
		t.Fatalf("create tag %s: %v", name, err)
	}
	return tag.ID
}

// TestTaxonomyListsKeysetAndCounts is the wave's core case: all three lanes
// walk their pages by keyset, close on a short page, reject a cross-lane
// cursor — and every work_count agrees with what the matching works?<filter>=
// call actually returns for the SAME nsfw setting.
func TestTaxonomyListsKeysetAndCounts(t *testing.T) {
	cleanTables(t)
	cleanTagTables(t)
	cleanTaxonomyTables(t)
	svc := newPublicSvc()
	ctx := t.Context()

	// One all-ages work and one r18 work, both LIVE galgame. Plus a stub work
	// and an ASMR work that must never be counted anywhere.
	wSafe := createWorkX(t, galgameMediumID, model.ContentRatingAllAges, model.WorkStatusLive, "SafeWork")
	wR18 := createWorkX(t, galgameMediumID, model.ContentRatingR18, model.WorkStatusLive, "R18Work")
	wStub := createWorkX(t, galgameMediumID, model.ContentRatingAllAges, model.WorkStatusStub, "StubWork")
	wASMR := createWorkX(t, 5, model.ContentRatingAllAges, model.WorkStatusLive, "AsmrWork")

	// ── labels ───────────────────────────────────────────────────────────────
	// brandID gets all four works (so the count must drop the stub + asmr);
	// circleID gets none.
	brandID := addWorkLabel(t, wSafe.ID, "Alcot", model.LabelKindGameBrand, model.WorkLabelKindBrand)
	for _, w := range []int64{wR18.ID, wStub.ID, wASMR.ID} {
		if err := testDB.Create(&model.CatalogWorkLabel{WorkID: w, LabelID: brandID, Kind: model.WorkLabelKindBrand}).Error; err != nil {
			t.Fatalf("extra label edge: %v", err)
		}
	}
	// A SECOND edge kind on the same (work, label) pair — catalog_work_label is
	// keyed (work_id, label_id, kind), so a naive count(*) would double-count.
	if err := testDB.Create(&model.CatalogWorkLabel{WorkID: wSafe.ID, LabelID: brandID, Kind: model.WorkLabelKindPublisher}).Error; err != nil {
		t.Fatalf("second-kind label edge: %v", err)
	}
	circleID := addWorkLabel(t, wSafe.ID, "Circle Zero", model.LabelKindDoujinCircle, model.WorkLabelKindCircle)
	if err := testDB.Exec(`DELETE FROM catalog_work_label WHERE label_id = ?`, circleID).Error; err != nil {
		t.Fatalf("detach circle: %v", err)
	}

	labels, err := svc.LabelsList(ctx, LabelsListFilter{}, "", 50)
	if err != nil {
		t.Fatalf("LabelsList sfw: %v", err)
	}
	if len(labels.Items) != 2 || labels.NextCursor != nil {
		t.Fatalf("labels page = %d items cursor=%v, want 2 + terminal", len(labels.Items), labels.NextCursor)
	}
	if labels.Items[0].ID != brandID || labels.Items[0].Kind != "game_brand" {
		t.Fatalf("labels[0] = %+v", labels.Items[0])
	}
	if labels.Items[0].WorkCount != 1 {
		t.Fatalf("sfw brand work_count = %d, want 1 (r18/stub/asmr excluded, double edge counted once)", labels.Items[0].WorkCount)
	}
	if labels.Items[1].WorkCount != 0 {
		t.Fatalf("unattached label work_count = %d, want 0", labels.Items[1].WorkCount)
	}

	labelsNSFW, err := svc.LabelsList(ctx, LabelsListFilter{NSFW: true}, "", 50)
	if err != nil {
		t.Fatalf("LabelsList nsfw: %v", err)
	}
	if labelsNSFW.Items[0].WorkCount != 2 {
		t.Fatalf("nsfw brand work_count = %d, want 2 (r18 now counted)", labelsNSFW.Items[0].WorkCount)
	}

	// The count IS the member list: works?label_id= returns exactly work_count
	// rows for the same caller. This equality is the whole contract.
	assertCountMatchesWorksList(t, svc, WorksListFilter{Sort: "id", LabelID: brandID}, labels.Items[0].WorkCount)
	assertCountMatchesWorksList(t, svc, WorksListFilter{Sort: "id", LabelID: brandID, NSFW: true}, labelsNSFW.Items[0].WorkCount)

	// kind filter.
	kind := model.LabelKindDoujinCircle
	filtered, err := svc.LabelsList(ctx, LabelsListFilter{Kind: &kind}, "", 50)
	if err != nil {
		t.Fatalf("LabelsList kind: %v", err)
	}
	if len(filtered.Items) != 1 || filtered.Items[0].ID != circleID {
		t.Fatalf("kind=doujin_circle = %+v, want only %d", filtered.Items, circleID)
	}

	// A soft-deleted (merged-away) label leaves the lane.
	if err := testDB.Exec(`UPDATE catalog_label SET deleted_at = now() WHERE id = ?`, circleID).Error; err != nil {
		t.Fatalf("soft delete label: %v", err)
	}
	afterDelete, err := svc.LabelsList(ctx, LabelsListFilter{}, "", 50)
	if err != nil {
		t.Fatalf("LabelsList after soft delete: %v", err)
	}
	if len(afterDelete.Items) != 1 || afterDelete.Items[0].ID != brandID {
		t.Fatalf("soft-deleted label still listed: %+v", afterDelete.Items)
	}

	// ── tags ─────────────────────────────────────────────────────────────────
	// TWO source tags mapping onto ONE canonical tag, both on wSafe — the
	// canonical count must still be 1.
	coreTagID := createCanonicalTag(t, "fantasy", model.TagTierCore, model.TagKindContent)
	metaTagID := createCanonicalTag(t, "PC", model.TagTierHidden, model.TagKindMeta)
	const srcBangumi int16 = 3
	for _, name := range []string{"ファンタジー", "奇幻"} {
		if err := testDB.Create(&model.CatalogTagSourceMap{SourceID: srcBangumi, SourceName: name, TagID: coreTagID}).Error; err != nil {
			t.Fatalf("map source tag %s: %v", name, err)
		}
		for _, w := range []int64{wSafe.ID, wR18.ID} {
			if err := testDB.Create(&model.CatalogWorkTag{WorkID: w, Name: name, Count: 1, SourceID: srcBangumi}).Error; err != nil {
				t.Fatalf("work tag %s: %v", name, err)
			}
		}
	}

	tagPage, err := svc.TagsList(ctx, TagsListFilter{}, "", 50)
	if err != nil {
		t.Fatalf("TagsList: %v", err)
	}
	if len(tagPage.Items) != 2 {
		t.Fatalf("tags page = %d, want 2", len(tagPage.Items))
	}
	if tagPage.Items[0].ID != coreTagID || tagPage.Items[0].Tier != "core" || tagPage.Items[0].Kind != "content" {
		t.Fatalf("tags[0] = %+v", tagPage.Items[0])
	}
	if tagPage.Items[0].WorkCount != 1 {
		t.Fatalf("sfw tag work_count = %d, want 1 (two mapped source tags on one work count once)", tagPage.Items[0].WorkCount)
	}
	tagPageNSFW, err := svc.TagsList(ctx, TagsListFilter{NSFW: true}, "", 50)
	if err != nil {
		t.Fatalf("TagsList nsfw: %v", err)
	}
	if tagPageNSFW.Items[0].WorkCount != 2 {
		t.Fatalf("nsfw tag work_count = %d, want 2", tagPageNSFW.Items[0].WorkCount)
	}
	assertCountMatchesWorksList(t, svc, WorksListFilter{Sort: "id", TagIDs: []int64{coreTagID}}, tagPage.Items[0].WorkCount)
	assertCountMatchesWorksList(t, svc, WorksListFilter{Sort: "id", TagIDs: []int64{coreTagID}, NSFW: true}, tagPageNSFW.Items[0].WorkCount)

	tier := model.TagTierHidden
	tagKind := model.TagKindMeta
	onlyMeta, err := svc.TagsList(ctx, TagsListFilter{Tier: &tier, Kind: &tagKind}, "", 50)
	if err != nil {
		t.Fatalf("TagsList filtered: %v", err)
	}
	if len(onlyMeta.Items) != 1 || onlyMeta.Items[0].ID != metaTagID {
		t.Fatalf("tier=hidden&kind=meta = %+v, want only %d", onlyMeta.Items, metaTagID)
	}

	// ── engines ──────────────────────────────────────────────────────────────
	kirikiri := createEngine(t, "KiriKiri")
	renpy := createEngine(t, "Ren'Py")
	attachEngine(t, wSafe.ID, kirikiri)
	attachEngine(t, wR18.ID, kirikiri)
	attachEngine(t, wStub.ID, kirikiri) // stub: never counted

	engines, err := svc.EnginesList(ctx, EnginesListFilter{}, "", 50)
	if err != nil {
		t.Fatalf("EnginesList: %v", err)
	}
	if len(engines.Items) != 2 || engines.Items[0].ID != kirikiri || engines.Items[0].Name != "KiriKiri" {
		t.Fatalf("engines page = %+v", engines.Items)
	}
	if engines.Items[0].WorkCount != 1 || engines.Items[1].WorkCount != 0 {
		t.Fatalf("engine counts = [%d,%d], want [1,0]", engines.Items[0].WorkCount, engines.Items[1].WorkCount)
	}
	enginesNSFW, err := svc.EnginesList(ctx, EnginesListFilter{NSFW: true}, "", 50)
	if err != nil {
		t.Fatalf("EnginesList nsfw: %v", err)
	}
	if enginesNSFW.Items[0].WorkCount != 2 {
		t.Fatalf("nsfw engine work_count = %d, want 2", enginesNSFW.Items[0].WorkCount)
	}
	assertCountMatchesWorksList(t, svc, WorksListFilter{Sort: "id", EngineID: kirikiri}, engines.Items[0].WorkCount)
	assertCountMatchesWorksList(t, svc, WorksListFilter{Sort: "id", EngineID: kirikiri, NSFW: true}, enginesNSFW.Items[0].WorkCount)
	_ = renpy
	_ = wASMR

	// ── keyset walk + cursor lane pinning (engines lane stands for all three,
	// they share the codec) ──────────────────────────────────────────────────
	p1, err := svc.EnginesList(ctx, EnginesListFilter{}, "", 1)
	if err != nil {
		t.Fatalf("engines p1: %v", err)
	}
	if len(p1.Items) != 1 || p1.Items[0].ID != kirikiri || p1.NextCursor == nil {
		t.Fatalf("engines p1 = %+v cursor=%v", p1.Items, p1.NextCursor)
	}
	p2, err := svc.EnginesList(ctx, EnginesListFilter{}, *p1.NextCursor, 1)
	if err != nil {
		t.Fatalf("engines p2: %v", err)
	}
	if len(p2.Items) != 1 || p2.Items[0].ID != renpy {
		t.Fatalf("engines p2 = %+v, want [%d]", p2.Items, renpy)
	}
	p3, err := svc.EnginesList(ctx, EnginesListFilter{}, *p2.NextCursor, 1)
	if err != nil {
		t.Fatalf("engines p3: %v", err)
	}
	if len(p3.Items) != 0 || p3.NextCursor != nil {
		t.Fatalf("engines p3 should be the empty terminal page: %+v cursor=%v", p3.Items, p3.NextCursor)
	}
	if _, err := svc.EnginesList(ctx, EnginesListFilter{}, "!!!not-base64!!!", 50); err != ErrBadCursor {
		t.Fatalf("malformed engines cursor = %v, want ErrBadCursor", err)
	}
	// A cursor minted on one taxonomy lane must not replay on another.
	if _, err := svc.LabelsList(ctx, LabelsListFilter{}, *p1.NextCursor, 50); err != ErrBadCursor {
		t.Fatalf("engines cursor on the labels lane = %v, want ErrBadCursor", err)
	}
	if _, err := svc.TagsList(ctx, TagsListFilter{}, *p1.NextCursor, 50); err != ErrBadCursor {
		t.Fatalf("engines cursor on the tags lane = %v, want ErrBadCursor", err)
	}
	// ...nor on the works lane, and vice versa.
	worksPage, err := svc.WorksList(ctx, WorksListFilter{Sort: "id"}, "", 1)
	if err != nil {
		t.Fatalf("works p1: %v", err)
	}
	if _, err := svc.EnginesList(ctx, EnginesListFilter{}, *worksPage.NextCursor, 50); err != ErrBadCursor {
		t.Fatalf("works cursor on the engines lane = %v, want ErrBadCursor", err)
	}
}

// assertCountMatchesWorksList walks the works list under the given filter and
// checks the row count equals the work_count the taxonomy lane advertised.
func assertCountMatchesWorksList(t *testing.T, svc *PublicService, f WorksListFilter, want int) {
	t.Helper()
	page, err := svc.WorksList(t.Context(), f, "", 100)
	if err != nil {
		t.Fatalf("WorksList %+v: %v", f, err)
	}
	if len(page.Items) != want {
		t.Fatalf("works?filter=%+v returned %d rows but work_count advertised %d — a count must never disagree with its own member list",
			f, len(page.Items), want)
	}
}

// TestEngineDetailRefsExactOnly pins the engine record: refs ride the generic
// exact-only loader (A2-0's wiki eid anchors surface, a probable one never
// does), an unknown id is a miss, and work_count follows the nsfw switch.
func TestEngineDetailRefsExactOnly(t *testing.T) {
	cleanTables(t)
	cleanTaxonomyTables(t)
	svc := newPublicSvc()
	ctx := t.Context()

	eng := createEngine(t, "RealLive")
	wSafe := createWorkX(t, galgameMediumID, model.ContentRatingAllAges, model.WorkStatusLive, "EngSafe")
	wR18 := createWorkX(t, galgameMediumID, model.ContentRatingR18, model.WorkStatusLive, "EngR18")
	attachEngine(t, wSafe.ID, eng)
	attachEngine(t, wR18.ID, eng)

	const srcWiki int16 = 12 // galgame_wiki — the A2-0 registrar rescue's source
	addExternalRef(t, model.EntityTypeEngine, eng, srcWiki, "1001", model.LinkKindExact)
	addExternalRef(t, model.EntityTypeEngine, eng, srcVNDB, "e999", model.LinkKindProbable)
	addExternalRef(t, model.EntityTypeEngine, eng, srcDlsite, "https://example.test", model.LinkKindRelated)

	rec, found, err := svc.EngineDetail(ctx, eng, false)
	if err != nil || !found {
		t.Fatalf("EngineDetail: found=%v err=%v", found, err)
	}
	if rec.Name != "RealLive" || rec.WorkCount != 1 {
		t.Fatalf("engine detail = %+v, want RealLive/work_count 1", rec)
	}
	if len(rec.Refs) != 1 || rec.Refs[0].Source != "galgame_wiki" || rec.Refs[0].ExternalID != "1001" {
		t.Fatalf("engine refs = %+v, want only the exact wiki anchor", rec.Refs)
	}

	nsfwRec, _, err := svc.EngineDetail(ctx, eng, true)
	if err != nil {
		t.Fatalf("EngineDetail nsfw: %v", err)
	}
	if nsfwRec.WorkCount != 2 {
		t.Fatalf("nsfw engine work_count = %d, want 2", nsfwRec.WorkCount)
	}

	if _, found, _ := svc.EngineDetail(ctx, 999999, false); found {
		t.Fatal("unknown engine id must be found=false")
	}
}

// TestTagDetailIntrosAlwaysPresent pins the A2-1b intro channel: the block is
// ALWAYS present (empty → [], never null), merges to one row per language with
// the lowest source_id winning, and renders the public source key.
func TestTagDetailIntrosAlwaysPresent(t *testing.T) {
	cleanTables(t)
	cleanTagTables(t)
	cleanTaxonomyTables(t)
	svc := newPublicSvc()
	ctx := t.Context()

	bare := createCanonicalTag(t, "bare", model.TagTierCore, model.TagKindContent)
	described := createCanonicalTag(t, "described", model.TagTierCore, model.TagKindContent)

	rec, found, err := svc.TagDetail(ctx, bare, false, false, 50, 0)
	if err != nil || !found {
		t.Fatalf("TagDetail bare: found=%v err=%v", found, err)
	}
	if rec.Intros == nil || len(rec.Intros) != 0 {
		t.Fatalf("intros must be an empty slice (serialized []), got %#v", rec.Intros)
	}

	const srcWiki int16 = 12 // galgame_wiki — higher id, so it loses the merge
	for _, in := range []struct {
		lang, body string
		src        int16
	}{
		{"zh-Hans", "低位来源胜出", srcErogamespace},
		{"zh-Hans", "另一来源", srcWiki},
		{"ja", "日本語", srcVNDB},
	} {
		if err := testDB.Create(&model.CatalogTagIntro{
			TagID: described, Lang: in.lang, Intro: in.body, SourceID: in.src,
		}).Error; err != nil {
			t.Fatalf("create tag intro: %v", err)
		}
	}
	rec, _, err = svc.TagDetail(ctx, described, false, false, 50, 0)
	if err != nil {
		t.Fatalf("TagDetail described: %v", err)
	}
	if len(rec.Intros) != 2 {
		t.Fatalf("intros = %+v, want one row per language", rec.Intros)
	}
	if rec.Intros[0].Lang != "ja" || rec.Intros[0].Intro != "日本語" {
		t.Fatalf("intros[0] = %+v, want the ja row first (lang ASC)", rec.Intros[0])
	}
	if rec.Intros[1].Lang != "zh-Hans" || rec.Intros[1].Intro != "低位来源胜出" {
		t.Fatalf("intros[1] = %+v, want the lowest source_id to win the language", rec.Intros[1])
	}
	// The registry spells this source "erogamespace"; the public wire must
	// always carry the site's real spelling.
	if rec.Intros[1].Source != "erogamescape" {
		t.Fatalf("intro source = %q, want the PUBLIC source key", rec.Intros[1].Source)
	}
}

// TestScreenshotMetaEnrichment closes the A2-1a carry-over: screenshots carry
// width/height/thumbhash from the same batched image_service lookup covers use,
// and degrade gracefully — unknown hash, and the lookup unwired entirely.
func TestScreenshotMetaEnrichment(t *testing.T) {
	cleanTables(t)
	cleanTaxonomyTables(t)
	ctx := t.Context()

	w := createWorkX(t, galgameMediumID, model.ContentRatingAllAges, model.WorkStatusLive, "ShotHost")
	known, unknown := hash64("bb22"), hash64("cc33")
	for i, h := range []string{known, unknown} {
		if err := testDB.Create(&model.CatalogWorkScreenshot{
			WorkID: w.ID, ImageHash: h, SortOrder: i, Caption: "cap", SourceID: srcVNDB,
		}).Error; err != nil {
			t.Fatalf("create screenshot: %v", err)
		}
	}

	// Enrichment unwired: the URLs still render, the three keys stay zero.
	bare := newPublicSvcCDN()
	rec, found, err := bare.WorkDetail(ctx, w.ID, PublicInclude{}, false, 0)
	if err != nil || !found {
		t.Fatalf("WorkDetail unwired: found=%v err=%v", found, err)
	}
	if len(rec.Screenshots) != 2 {
		t.Fatalf("screenshots = %d, want 2", len(rec.Screenshots))
	}
	for _, sc := range rec.Screenshots {
		if sc.URL == "" {
			t.Fatalf("screenshot url must render without enrichment: %+v", sc)
		}
		if sc.Width != 0 || sc.Height != 0 || sc.Thumbhash != "" {
			t.Fatalf("unwired lookup must leave the meta keys empty: %+v", sc)
		}
	}

	// Wired: the known hash lights up, the unknown one degrades to skeleton.
	svc := newPublicSvcCDN().WithImageMeta(stubMeta(map[string]ImageMeta{
		known: {Width: 1280, Height: 720, Thumbhash: "shot-hash"},
	}))
	rec, _, err = svc.WorkDetail(ctx, w.ID, PublicInclude{}, false, 0)
	if err != nil {
		t.Fatalf("WorkDetail wired: %v", err)
	}
	if rec.Screenshots[0].Width != 1280 || rec.Screenshots[0].Height != 720 || rec.Screenshots[0].Thumbhash != "shot-hash" {
		t.Fatalf("known screenshot not enriched: %+v", rec.Screenshots[0])
	}
	if rec.Screenshots[1].Width != 0 || rec.Screenshots[1].Height != 0 || rec.Screenshots[1].Thumbhash != "" {
		t.Fatalf("unknown hash must degrade, not guess: %+v", rec.Screenshots[1])
	}
}

// TestWorkMediaMetaBatchesCoversAndScreenshots pins that the detail face makes
// ONE image_service call for the whole record — covers and screenshots share
// the batch, never a round-trip each.
func TestWorkMediaMetaBatchesCoversAndScreenshots(t *testing.T) {
	cleanTables(t)
	cleanTaxonomyTables(t)
	ctx := t.Context()

	w := createWorkX(t, galgameMediumID, model.ContentRatingAllAges, model.WorkStatusLive, "BatchHost")
	coverHash, shotHash := hash64("dd44"), hash64("ee55")
	addWorkCover(t, w.ID, coverHash, 0, "main", true, 0, srcVNDB)
	if err := testDB.Create(&model.CatalogWorkScreenshot{
		WorkID: w.ID, ImageHash: shotHash, SortOrder: 0, SourceID: srcVNDB,
	}).Error; err != nil {
		t.Fatalf("create screenshot: %v", err)
	}

	calls := 0
	seen := map[string]bool{}
	svc := newPublicSvcCDN().WithImageMeta(func(_ context.Context, hashes []string) (map[string]ImageMeta, error) {
		calls++
		out := make(map[string]ImageMeta, len(hashes))
		for _, h := range hashes {
			seen[h] = true
			out[h] = ImageMeta{Width: 800, Height: 600, Thumbhash: "th-" + h[:4]}
		}
		return out, nil
	})
	rec, found, err := svc.WorkDetail(ctx, w.ID, PublicInclude{}, false, 0)
	if err != nil || !found {
		t.Fatalf("WorkDetail: found=%v err=%v", found, err)
	}
	if calls != 1 {
		t.Fatalf("image_service called %d times, want exactly 1 batch for the whole record", calls)
	}
	if !seen[coverHash] || !seen[shotHash] {
		t.Fatalf("the single batch must carry both grains: cover=%v shot=%v", seen[coverHash], seen[shotHash])
	}
	if rec.Covers[0].Thumbhash == "" || rec.Screenshots[0].Thumbhash == "" {
		t.Fatalf("both grains must be enriched: cover=%+v shot=%+v", rec.Covers[0], rec.Screenshots[0])
	}
}
