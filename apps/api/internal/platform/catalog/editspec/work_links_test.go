package editspec_test

import (
	"strings"
	"testing"

	"api/internal/platform/catalog/editspec"
	"api/internal/platform/catalog/model"
)

func TestLinksUpstreamCollisionRejected(t *testing.T) {
	e := newEngine(t)
	work := createWork(t, "リンク衝突")
	if err := testDB.Create(&model.CatalogExternalRef{
		EntityType: model.EntityTypeWork, EntityID: work.ID,
		SourceID: vndbSource, ExternalID: "v999", LinkKind: model.LinkKindExact,
		MatchedBy: "importer",
	}).Error; err != nil {
		t.Fatal(err)
	}

	own := []any{"https://x.com/studio_x"}
	snap := mergeField(t, e, work.ID, editspec.FieldWorkLinks, own)
	sameJSON(t, "links", snap[editspec.FieldWorkLinks], own)

	own = []any{"https://x.com/studio_x", "https://example.com/product/kimi"}
	snap = mergeField(t, e, work.ID, editspec.FieldWorkLinks, own)
	sameJSON(t, "links own-lane", snap[editspec.FieldWorkLinks], own)

	valErr := mergeMustReject(t, e, editspec.TypeWork, work.ID, editspec.FieldWorkLinks, []any{
		"https://x.com/studio_x",
		"https://example.com/product/kimi",
		"https://vndb.org/v999",
	})
	if !strings.Contains(valErr.Reason, "vndb:v999") {
		t.Fatalf("the 422 must name the colliding anchor, got %q", valErr.Reason)
	}

	var curated []model.CatalogExternalRef
	if err := testDB.Where("entity_type = ? AND entity_id = ? AND matched_by = ?",
		model.EntityTypeWork, work.ID, "curated").Order("source_id, external_id").Find(&curated).Error; err != nil {
		t.Fatal(err)
	}
	if len(curated) != 2 {
		t.Fatalf("the rejected merge must leave the curated lane: %+v", curated)
	}
}

func TestLabelLinksUpstreamCollisionRejected(t *testing.T) {
	e := newTaxonomyEngine(t)
	label := model.CatalogLabel{DisplayName: "リンク衝突サークル", Kind: model.LabelKindDoujinCircle}
	if err := testDB.Create(&label).Error; err != nil {
		t.Fatal(err)
	}
	if err := testDB.Create(&model.CatalogExternalRef{
		EntityType: model.EntityTypeLabel, EntityID: label.ID,
		SourceID: 10, ExternalID: "studio_x", LinkKind: model.LinkKindRelated,
		MatchedBy: "importer",
	}).Error; err != nil {
		t.Fatal(err)
	}

	own := []any{"https://example.com/circle"}
	snap := mergeOn(t, e, editspec.TypeLabel, label.ID, map[string]any{
		editspec.FieldLabelLinks: own,
	})
	sameJSON(t, "label links", snap[editspec.FieldLabelLinks], own)

	valErr := mergeMustReject(t, e, editspec.TypeLabel, label.ID, editspec.FieldLabelLinks, []any{
		"https://example.com/circle",
		"https://x.com/studio_x",
	})
	if !strings.Contains(valErr.Reason, "twitter:studio_x") {
		t.Fatalf("the 422 must name the colliding anchor, got %q", valErr.Reason)
	}

	var curated []model.CatalogExternalRef
	if err := testDB.Where("entity_type = ? AND entity_id = ? AND matched_by = ?",
		model.EntityTypeLabel, label.ID, "curated").Find(&curated).Error; err != nil {
		t.Fatal(err)
	}
	if len(curated) != 1 || curated[0].ExternalID != "https://example.com/circle" {
		t.Fatalf("the rejected merge must leave the curated lane: %+v", curated)
	}
}
