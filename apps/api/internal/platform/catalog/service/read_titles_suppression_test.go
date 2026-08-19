package service

import (
	"testing"

	"api/internal/platform/catalog/editspec"
	"api/internal/platform/catalog/model"
	"api/internal/platform/editing"
)

func suppressTitle(t *testing.T, workID int64, kind int16, lang, title string) {
	t.Helper()
	row := editing.SuppressedRow{
		EntityType:  editspec.TypeWork,
		EntityID:    workID,
		FieldKey:    editspec.FieldWorkTitles,
		IdentityKey: editspec.WorkTitleIdentity(kind, lang, title),
	}
	if err := testDB.Create(&row).Error; err != nil {
		t.Fatalf("suppress %q: %v", title, err)
	}
}

func TestReadFacesExcludeSuppressedTitles(t *testing.T) {
	cleanTables(t)
	ctx := t.Context()
	read := NewReadService(testDB)

	w := createWorkX(t, galgameMediumID, model.ContentRatingAllAges, model.WorkStatusLive, "本編")
	rows := []model.CatalogWorkTitle{
		{WorkID: w.ID, Lang: "ja", Title: "抑制テスト本編", Kind: model.WorkTitleKindOfficial},
		{WorkID: w.ID, Lang: "", Title: "抑制テスト誤別名", Kind: model.WorkTitleKindAlias},
		{WorkID: w.ID, Lang: "ja", Title: "抑制テストかな", Kind: model.WorkTitleKindSearchHint},
	}
	if err := testDB.Create(&rows).Error; err != nil {
		t.Fatal(err)
	}

	before, err := read.loadWorkDetailTitles(ctx, []claimSubject{{WorkID: w.ID}})
	if err != nil {
		t.Fatal(err)
	}
	if len(before[w.ID]) != 3 {
		t.Fatalf("detail titles before suppression = %d", len(before[w.ID]))
	}
	if hits, err := read.SearchWorks(ctx, "抑制テスト誤別名", -1, 50, nil, ""); err != nil || len(hits) != 1 {
		t.Fatalf("search before suppression: %d hits err=%v", len(hits), err)
	}

	suppressTitle(t, w.ID, model.WorkTitleKindAlias, "", "抑制テスト誤別名")
	suppressTitle(t, w.ID, model.WorkTitleKindSearchHint, "ja", "抑制テストかな")

	after, err := read.loadWorkDetailTitles(ctx, []claimSubject{{WorkID: w.ID}})
	if err != nil {
		t.Fatal(err)
	}
	if len(after[w.ID]) != 1 || after[w.ID][0].Title != "抑制テスト本編" {
		t.Fatalf("detail titles after suppression = %+v", after[w.ID])
	}
	list, err := read.loadWorkTitles(ctx, []claimSubject{{WorkID: w.ID}})
	if err != nil {
		t.Fatal(err)
	}
	if len(list[w.ID]) != 1 || list[w.ID][0].Title != "抑制テスト本編" {
		t.Fatalf("list titles after suppression = %+v", list[w.ID])
	}
	if hits, err := read.SearchWorks(ctx, "抑制テスト誤別名", -1, 50, nil, ""); err != nil || len(hits) != 0 {
		t.Fatalf("a suppressed alias must leave the name index: %d hits err=%v", len(hits), err)
	}
	if hits, err := read.SearchWorks(ctx, "抑制テスト本編", -1, 50, nil, ""); err != nil || len(hits) != 1 {
		t.Fatalf("search on a live title: %d hits err=%v", len(hits), err)
	}

	// The suppressed rows are still in the table: the importer owns them and
	// the exclusion is a read-path rule, not a delete.
	var live int64
	if err := testDB.Model(&model.CatalogWorkTitle{}).Where("work_id = ?", w.ID).Count(&live).Error; err != nil {
		t.Fatal(err)
	}
	if live != 3 {
		t.Fatalf("catalog_work_title rows = %d, want the upstream rows untouched", live)
	}
}

func TestMergeCarriesTitleSuppressions(t *testing.T) {
	cleanTables(t)
	ctx := t.Context()

	target := createWork(t, "同じ作品")
	source := createWork(t, "同じ 作品")
	rows := []model.CatalogWorkTitle{
		{WorkID: target.ID, Lang: "ja", Title: "生き残る本編", Kind: model.WorkTitleKindOfficial},
		{WorkID: source.ID, Lang: "", Title: "誤別名", Kind: model.WorkTitleKindAlias},
		{WorkID: source.ID, Lang: "", Title: "共通別名", Kind: model.WorkTitleKindAlias},
		{WorkID: target.ID, Lang: "", Title: "共通別名", Kind: model.WorkTitleKindAlias},
	}
	if err := testDB.Create(&rows).Error; err != nil {
		t.Fatal(err)
	}
	suppressTitle(t, source.ID, model.WorkTitleKindAlias, "", "誤別名")
	suppressTitle(t, source.ID, model.WorkTitleKindAlias, "", "共通別名")
	suppressTitle(t, target.ID, model.WorkTitleKindAlias, "", "共通別名")

	p, err := testMerge.ProposeMerge(ctx, model.EntityTypeWork, source.ID, target.ID, 7, "suppression rehang")
	if err != nil {
		t.Fatal(err)
	}
	approveAndForceExecutable(t, p.ID)
	if err := testMerge.ExecuteMerge(ctx, p.ID, nil); err != nil {
		t.Fatal(err)
	}

	keys, err := editing.LoadSuppressedKeys(ctx, testDB, editspec.TypeWork, target.ID, editspec.FieldWorkTitles)
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 2 {
		t.Fatalf("suppressions on the survivor = %v", keys)
	}
	left, err := editing.LoadSuppressedKeys(ctx, testDB, editspec.TypeWork, source.ID, editspec.FieldWorkTitles)
	if err != nil {
		t.Fatal(err)
	}
	if len(left) != 0 {
		t.Fatalf("suppressions stranded on the merged-away work: %v", left)
	}

	read := NewReadService(testDB)
	got, err := read.loadWorkDetailTitles(ctx, []claimSubject{{WorkID: target.ID}})
	if err != nil {
		t.Fatal(err)
	}
	if len(got[target.ID]) != 1 || got[target.ID][0].Title != "生き残る本編" {
		t.Fatalf("titles on the survivor = %+v", got[target.ID])
	}
}
