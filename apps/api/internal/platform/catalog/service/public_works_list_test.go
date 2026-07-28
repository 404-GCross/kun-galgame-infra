// public_works_list_test.go — doc 106 W2 query coverage: the works browse
// lane (filters + keyset), the changes feed (order + cursor resume), and the
// canonical tag detail (works attach). Integration against kun_catalog_test.
package service

import (
	"testing"

	"api/internal/platform/catalog/model"
)

// cleanTagTables truncates the tag facet + canonical vocabulary tables that
// cleanTables (service_test.go) does not cover — the W2 tag/tag-map/work-tag
// rows this suite creates.
func cleanTagTables(t *testing.T) {
	t.Helper()
	for _, table := range []string{"catalog_work_tag", "catalog_tag_source_map", "catalog_tag", "catalog_work_platform", "catalog_series_member", "catalog_series"} {
		if err := testDB.Exec("TRUNCATE " + table + " RESTART IDENTITY CASCADE").Error; err != nil {
			t.Fatalf("truncate %s: %v", table, err)
		}
	}
}

func TestWorksListFiltersAndKeyset(t *testing.T) {
	cleanTables(t)
	cleanTagTables(t)
	svc := newPublicSvc()

	// Three galgame works: one bodyless all-ages, one claimed sensitive, one
	// r18. Plus one ASMR work (medium 5) that must never appear.
	wA := createWorkX(t, 1, model.ContentRatingAllAges, model.WorkStatusLive, "Alpha")
	wB := createWorkX(t, 1, model.ContentRatingSensitive, model.WorkStatusLive, "Bravo")
	claimWork(t, wB.ID, "galgame_wiki", 42)
	_ = createWorkX(t, 1, model.ContentRatingR18, model.WorkStatusLive, "Roughage")
	_ = createWorkX(t, 5, model.ContentRatingAllAges, model.WorkStatusLive, "ASMRthing")

	// Default (sfw): the two non-r18 galgame works, id ASC.
	page, err := svc.WorksList(t.Context(), WorksListFilter{Sort: "id"}, "", 50)
	if err != nil {
		t.Fatalf("WorksList sfw: %v", err)
	}
	if len(page.Items) != 2 {
		t.Fatalf("sfw page = %d items, want 2 (r18 + asmr excluded)", len(page.Items))
	}
	if page.Items[0].ID != wA.ID || page.Items[1].ID != wB.ID {
		t.Fatalf("sfw order = [%d,%d], want [%d,%d]", page.Items[0].ID, page.Items[1].ID, wA.ID, wB.ID)
	}
	if page.Items[1].ClaimedBy == nil || page.Items[1].ClaimedBy.Site != "galgame_wiki" {
		t.Fatalf("claimed brief missing claimed_by pointer")
	}

	// nsfw=1: r18 work now visible.
	page, err = svc.WorksList(t.Context(), WorksListFilter{Sort: "id", NSFW: true}, "", 50)
	if err != nil {
		t.Fatalf("WorksList nsfw: %v", err)
	}
	if len(page.Items) != 3 {
		t.Fatalf("nsfw page = %d items, want 3", len(page.Items))
	}

	// claimed=true → only wB.
	cl := true
	page, err = svc.WorksList(t.Context(), WorksListFilter{Sort: "id", Claimed: &cl, NSFW: true}, "", 50)
	if err != nil {
		t.Fatalf("WorksList claimed: %v", err)
	}
	if len(page.Items) != 1 || page.Items[0].ID != wB.ID {
		t.Fatalf("claimed=true = %+v, want only %d", page.Items, wB.ID)
	}

	// claimed=false → wA, wR (bodyless).
	cl2 := false
	page, err = svc.WorksList(t.Context(), WorksListFilter{Sort: "id", Claimed: &cl2, NSFW: true}, "", 50)
	if err != nil {
		t.Fatalf("WorksList bodyless: %v", err)
	}
	if len(page.Items) != 2 {
		t.Fatalf("claimed=false = %d, want 2 (wA,wR)", len(page.Items))
	}

	// Keyset: limit 1 walks the sfw set in two pages then stops.
	page, err = svc.WorksList(t.Context(), WorksListFilter{Sort: "id"}, "", 1)
	if err != nil {
		t.Fatalf("keyset p1: %v", err)
	}
	if len(page.Items) != 1 || page.Items[0].ID != wA.ID || page.NextCursor == nil {
		t.Fatalf("keyset p1 unexpected: items=%d cursor=%v", len(page.Items), page.NextCursor)
	}
	page2, err := svc.WorksList(t.Context(), WorksListFilter{Sort: "id"}, *page.NextCursor, 1)
	if err != nil {
		t.Fatalf("keyset p2: %v", err)
	}
	if len(page2.Items) != 1 || page2.Items[0].ID != wB.ID {
		t.Fatalf("keyset p2 = %+v, want [%d]", page2.Items, wB.ID)
	}
	page3, err := svc.WorksList(t.Context(), WorksListFilter{Sort: "id"}, *page2.NextCursor, 1)
	if err != nil {
		t.Fatalf("keyset p3: %v", err)
	}
	if len(page3.Items) != 0 || page3.NextCursor != nil {
		t.Fatalf("keyset p3 should be empty terminal page: items=%d cursor=%v", len(page3.Items), page3.NextCursor)
	}

	// A malformed cursor is ErrBadCursor.
	if _, err := svc.WorksList(t.Context(), WorksListFilter{Sort: "id"}, "!!!not-base64!!!", 50); err != ErrBadCursor {
		t.Fatalf("bad cursor err = %v, want ErrBadCursor", err)
	}
	// A cursor minted for the id lane must not replay on the updated lane.
	if _, err := svc.WorksList(t.Context(), WorksListFilter{Sort: "updated"}, *page.NextCursor, 50); err != ErrBadCursor {
		t.Fatalf("cross-lane cursor err = %v, want ErrBadCursor", err)
	}

	// released_after: only wA carries a 2020 release; wB a 2010 one.
	createRelease(t, wA.ID, 2020, 3, 1)
	createRelease(t, wB.ID, 2010, 0, 0)
	page, err = svc.WorksList(t.Context(), WorksListFilter{Sort: "id", ReleasedAfter: 2015 * 10000}, "", 50)
	if err != nil {
		t.Fatalf("WorksList released_after: %v", err)
	}
	if len(page.Items) != 1 || page.Items[0].ID != wA.ID {
		t.Fatalf("released_after=2015 = %+v, want only %d", page.Items, wA.ID)
	}
	if page.Items[0].ReleaseDate == nil || *page.Items[0].ReleaseDate != "2020-03-01" {
		t.Fatalf("wA release_date = %v, want 2020-03-01", page.Items[0].ReleaseDate)
	}
}

func TestChangesFeedOrderAndResume(t *testing.T) {
	cleanTables(t)
	svc := newPublicSvc()

	wA := createWorkX(t, 1, model.ContentRatingAllAges, model.WorkStatusLive, "First")
	wB := createWorkX(t, 1, model.ContentRatingR18, model.WorkStatusLive, "Second")
	// Force a deterministic updated_at ordering (wA older than wB).
	if err := testDB.Exec(`UPDATE catalog_work SET updated_at = ? WHERE id = ?`, "2026-01-01T00:00:00Z", wA.ID).Error; err != nil {
		t.Fatalf("stamp wA: %v", err)
	}
	if err := testDB.Exec(`UPDATE catalog_work SET updated_at = ? WHERE id = ?`, "2026-02-01T00:00:00Z", wB.ID).Error; err != nil {
		t.Fatalf("stamp wB: %v", err)
	}

	// The changes feed does NOT nsfw-gate: both ids appear (r18 wB included).
	page, err := svc.Changes(t.Context(), "", 1)
	if err != nil {
		t.Fatalf("Changes p1: %v", err)
	}
	if len(page.Items) != 1 || page.Items[0].ID != wA.ID || page.Items[0].EntityType != "work" {
		t.Fatalf("changes p1 = %+v, want [work %d]", page.Items, wA.ID)
	}
	if page.NextCursor == "" {
		t.Fatalf("changes next_cursor must always be present")
	}
	page2, err := svc.Changes(t.Context(), page.NextCursor, 1)
	if err != nil {
		t.Fatalf("Changes p2: %v", err)
	}
	if len(page2.Items) != 1 || page2.Items[0].ID != wB.ID {
		t.Fatalf("changes p2 = %+v, want [work %d]", page2.Items, wB.ID)
	}
	// A short/empty page echoes the cursor back (poll-for-new semantics).
	page3, err := svc.Changes(t.Context(), page2.NextCursor, 1)
	if err != nil {
		t.Fatalf("Changes p3: %v", err)
	}
	if len(page3.Items) != 0 || page3.NextCursor != page2.NextCursor {
		t.Fatalf("changes p3 empty-page cursor drift: items=%d cur=%q prev=%q", len(page3.Items), page3.NextCursor, page2.NextCursor)
	}
}

func TestTagDetailWorksAttach(t *testing.T) {
	cleanTables(t)
	cleanTagTables(t)
	svc := newPublicSvc()

	// Canonical tag 'fantasy' (core/content), mapped from a bangumi source tag.
	if err := testDB.Create(&model.CatalogTag{Name: "fantasy", Tier: model.TagTierCore, Kind: model.TagKindContent}).Error; err != nil {
		t.Fatalf("create tag: %v", err)
	}
	var tagID int64
	if err := testDB.Raw(`SELECT id FROM catalog_tag WHERE name = 'fantasy'`).Scan(&tagID).Error; err != nil {
		t.Fatalf("tag id: %v", err)
	}
	const srcBangumi int16 = 3
	if err := testDB.Create(&model.CatalogTagSourceMap{SourceID: srcBangumi, SourceName: "ファンタジー", TagID: tagID}).Error; err != nil {
		t.Fatalf("create tag map: %v", err)
	}

	wA := createWorkX(t, 1, model.ContentRatingAllAges, model.WorkStatusLive, "TaggedA")
	wR := createWorkX(t, 1, model.ContentRatingR18, model.WorkStatusLive, "TaggedR18")
	for _, id := range []int64{wA.ID, wR.ID} {
		if err := testDB.Create(&model.CatalogWorkTag{WorkID: id, Name: "ファンタジー", Count: 5, SourceID: srcBangumi}).Error; err != nil {
			t.Fatalf("create work tag: %v", err)
		}
	}

	// Head only (no include=works).
	rec, found, err := svc.TagDetail(t.Context(), tagID, false, false, 50, 0)
	if err != nil || !found {
		t.Fatalf("TagDetail head: found=%v err=%v", found, err)
	}
	if rec.Name != "fantasy" || rec.Tier != "core" || rec.Kind != "content" {
		t.Fatalf("tag head = %+v", rec)
	}
	if rec.Works != nil {
		t.Fatalf("works must be nil without include")
	}

	// include=works, sfw: only wA (r18 dropped).
	rec, _, err = svc.TagDetail(t.Context(), tagID, true, false, 50, 0)
	if err != nil {
		t.Fatalf("TagDetail works sfw: %v", err)
	}
	if len(rec.Works) != 1 || rec.Works[0].ID != wA.ID {
		t.Fatalf("tag works sfw = %+v, want only %d", rec.Works, wA.ID)
	}

	// include=works, nsfw: both.
	rec, _, err = svc.TagDetail(t.Context(), tagID, true, true, 50, 0)
	if err != nil {
		t.Fatalf("TagDetail works nsfw: %v", err)
	}
	if len(rec.Works) != 2 {
		t.Fatalf("tag works nsfw = %d, want 2", len(rec.Works))
	}

	// Unknown id → not found.
	if _, found, _ := svc.TagDetail(t.Context(), 999999, false, false, 50, 0); found {
		t.Fatalf("unknown tag id should be found=false")
	}
}
