package service

import (
	"testing"

	"api/internal/platform/catalog/model"
)

func TestSearchWorksSiteFilter(t *testing.T) {
	cleanTables(t)
	ctx := t.Context()
	read := NewReadService(testDB)

	titled := func(name, site string, productWorkID int64, state int16) int64 {
		t.Helper()
		w := createWorkX(t, galgameMediumID, model.ContentRatingAllAges, model.WorkStatusLive, name)
		if err := testDB.Create(&model.CatalogWorkTitle{
			WorkID: w.ID, Lang: "ja", Title: "租户テスト", Kind: model.WorkTitleKindOfficial,
		}).Error; err != nil {
			t.Fatal(err)
		}
		if site != "" {
			claimWork(t, w.ID, site, productWorkID)
			setClaimState(t, w.ID, i16(state))
		}
		return w.ID
	}
	mine := titled("うちの投稿", "kungal", 9700, model.ClaimStatePending)
	titled("よその投稿", "moyu", 9701, model.ClaimStatePending)
	titled("未認領", "", 0, 0)

	all, err := read.SearchWorks(ctx, "租户テスト", -1, 50, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 {
		t.Fatalf("absent site must not gate: %d hits", len(all))
	}

	ours, err := read.SearchWorks(ctx, "租户テスト", -1, 50, nil, "kungal")
	if err != nil {
		t.Fatal(err)
	}
	if len(ours) != 1 || ours[0].WorkID != mine {
		t.Fatalf("site gate: %+v", ours)
	}

	queue, err := read.SearchWorks(ctx, "租户テスト", -1, 50,
		[]string{model.ClaimStateKeyPending}, "kungal")
	if err != nil {
		t.Fatal(err)
	}
	if len(queue) != 1 || queue[0].WorkID != mine {
		t.Fatalf("site + claim_state: %+v", queue)
	}
	empty, err := read.SearchWorks(ctx, "租户テスト", -1, 50,
		[]string{model.ClaimStateKeyLive}, "kungal")
	if err != nil {
		t.Fatal(err)
	}
	if len(empty) != 0 {
		t.Fatalf("the gates must AND, not OR: %+v", empty)
	}

	page, err := read.SearchWorks(ctx, "租户テスト", -1, 1, nil, "moyu")
	if err != nil {
		t.Fatal(err)
	}
	if len(page) != 1 {
		t.Fatalf("gate must be applied before LIMIT: %d hits", len(page))
	}
}
