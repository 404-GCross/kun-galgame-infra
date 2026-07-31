// curated_lanes_test.go — the two rulings about which title/series rows a human
// full-replace may touch (wave 155 rulings 3a/3b).
//
// These lived in guard_test.go beside the mirror gate and outlived it: the gate
// was a temporary fence around facets with two writers, while these are
// permanent boundaries between the CURATED lane and the importer lanes. Wave
// 161 retired the gate; nothing about these changed.
package editspec_test

import (
	"testing"

	"api/internal/platform/catalog/editspec"
	"api/internal/platform/catalog/model"
	"api/internal/platform/editing"
)

func titlesValue(title string) []any {
	return []any{map[string]any{"lang": "ja", "title": title, "kind": float64(0)}}
}

// directEdit files and lands a patch in one call by merging it immediately —
// the shape every gated write takes on the real face.
func directEdit(t *testing.T, e *editing.Engine, workID int64, patch map[string]any) error {
	t.Helper()
	prop, _, err := e.CreateProposal(testCtx, editing.CreateProposalInput{
		EntityType: editspec.TypeWork, EntityID: workID, Patch: patch, Actor: realActor(100, "admin"),
	})
	if err != nil {
		return err
	}
	_, err = e.MergeProposal(testCtx, prop.ID, realActor(200, "ren"), "")
	return err
}

// The dlsite search-hint lane is an IMPORTER lane: a human full-replace must
// neither carry it nor reap it (wave 155 ruling 3b). Independent of the mirror
// gate — it stays true after N5 removes the gate.
func TestTitlesReplaceLeavesTheSearchHintLane(t *testing.T) {
	e := newEngine(t)
	work := createWork(t, "検索補助")
	if err := testDB.Create(&model.CatalogWorkTitle{
		WorkID: work.ID, Lang: "ja", Title: "かなよみ", Kind: model.WorkTitleKindSearchHint,
	}).Error; err != nil {
		t.Fatal(err)
	}

	// The snapshot the editor bootstraps from does not show the lane at all …
	snap, err := e.CurrentSnapshot(testCtx, editspec.TypeWork, work.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, el := range snap[editspec.FieldWorkTitles].([]any) {
		if el.(map[string]any)["kind"].(int64) == int64(model.WorkTitleKindSearchHint) {
			t.Fatal("search_hint must not appear in the titles value")
		}
	}
	// … a caller cannot write one …
	if _, _, err := e.CreateProposal(testCtx, editing.CreateProposalInput{
		EntityType: editspec.TypeWork, EntityID: work.ID,
		Patch: map[string]any{editspec.FieldWorkTitles: []any{
			map[string]any{"lang": "ja", "title": "題", "kind": float64(0)},
			map[string]any{"lang": "ja", "title": "補助", "kind": float64(model.WorkTitleKindSearchHint)},
		}},
		Actor: realActor(100, "admin"),
	}); err == nil {
		t.Fatal("kind=3 must be rejected")
	}
	// … and a full replace of the editorial kinds leaves it standing.
	if err := directEdit(t, e, work.ID, map[string]any{
		editspec.FieldWorkTitles: titlesValue("新題"),
	}); err != nil {
		t.Fatal(err)
	}
	var hints int64
	if err := testDB.Model(&model.CatalogWorkTitle{}).
		Where("work_id = ? AND kind = ?", work.ID, model.WorkTitleKindSearchHint).
		Count(&hints).Error; err != nil {
		t.Fatal(err)
	}
	if hints != 1 {
		t.Fatalf("search_hint rows after a full replace: %d, want 1", hints)
	}
}

// Renaming a series an importer reconciles is refused (wave 155 ruling 3a):
// jobs/workseries rewrites — and deletes — dlsite-sourced series rows.
func TestSeriesNameIsCuratedOnly(t *testing.T) {
	e := newTaxonomyEngine(t)
	dlsiteSource := int16(4)
	upstream := model.CatalogSeries{DisplayName: "上流シリーズ", SourceID: dlsiteSource}
	curated := model.CatalogSeries{DisplayName: "人手シリーズ", SourceID: 12}
	if err := testDB.Create(&[]model.CatalogSeries{upstream, curated}).Error; err != nil {
		t.Fatal(err)
	}
	var rows []model.CatalogSeries
	if err := testDB.Order("id").Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	rename := func(id int64) error {
		prop, _, err := e.CreateProposal(testCtx, editing.CreateProposalInput{
			EntityType: editspec.TypeSeries, EntityID: id,
			Patch: map[string]any{editspec.FieldSeriesName: "改名"}, Actor: realActor(100, "admin"),
		})
		if err != nil {
			return err
		}
		_, err = e.MergeProposal(testCtx, prop.ID, realActor(200, "ren"), "")
		return err
	}
	for _, r := range rows {
		err := rename(r.ID)
		if r.SourceID == dlsiteSource && err == nil {
			t.Fatal("renaming an upstream series must be refused")
		}
		if r.SourceID != dlsiteSource && err != nil {
			t.Fatalf("renaming a curated series: %v", err)
		}
	}
}
