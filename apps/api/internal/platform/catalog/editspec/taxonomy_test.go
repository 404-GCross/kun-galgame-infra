// taxonomy_test.go — wave 154 W4: the four narrow vocabulary registrations and
// catalog.work's OnMerge.
//
// The acceptance surface for a registration is the engine itself: registering a
// family is supposed to buy proposals, revisions, diffs and revert with no
// per-family code, so the cases exercise a real merge on each family rather
// than asserting that a spec struct has fields.
package editspec_test

import (
	"errors"
	"testing"

	"api/internal/platform/catalog/editspec"
	"api/internal/platform/catalog/model"
	"api/internal/platform/editing"
)

func newTaxonomyEngine(t *testing.T) *editing.Engine {
	t.Helper()
	e := newEngine(t) // truncates the catalog + engine tables
	if err := editspec.RegisterTaxonomy(e.Registry(), testDB); err != nil {
		t.Fatalf("register taxonomy: %v", err)
	}
	return e
}

// mergeOn is mergeField for an arbitrary entity type.
func mergeOn(t *testing.T, e *editing.Engine, entityType string, id int64, patch map[string]any) map[string]any {
	t.Helper()
	prop, _, err := e.CreateProposal(testCtx, editing.CreateProposalInput{
		EntityType: entityType, EntityID: id, Patch: patch, Actor: realActor(100, "admin"),
	})
	if err != nil {
		t.Fatalf("propose on %s: %v", entityType, err)
	}
	if _, err := e.MergeProposal(testCtx, prop.ID, realActor(200, "ren"), ""); err != nil {
		t.Fatalf("merge on %s: %v", entityType, err)
	}
	snap, err := e.CurrentSnapshot(testCtx, entityType, id)
	if err != nil {
		t.Fatalf("snapshot %s: %v", entityType, err)
	}
	return snap
}

func TestLabelFamilyEdits(t *testing.T) {
	e := newTaxonomyEngine(t)
	label := model.CatalogLabel{DisplayName: "旧サークル名", Kind: model.LabelKindDoujinCircle}
	if err := testDB.Create(&label).Error; err != nil {
		t.Fatal(err)
	}

	intros := []any{map[string]any{"lang": "ja", "intro": "同人サークルです"}}
	links := []any{"https://x.com/circle"}
	snap := mergeOn(t, e, editspec.TypeLabel, label.ID, map[string]any{
		editspec.FieldLabelName:   "新サークル名",
		editspec.FieldLabelIntros: intros,
		editspec.FieldLabelLinks:  links,
	})
	if snap[editspec.FieldLabelName] != "新サークル名" {
		t.Fatalf("name: %#v", snap[editspec.FieldLabelName])
	}
	sameJSON(t, "label intros", snap[editspec.FieldLabelIntros], intros)
	sameJSON(t, "label links", snap[editspec.FieldLabelLinks], links)

	// The link landed on the LABEL's own external-ref rows, not the work's.
	var refs []model.CatalogExternalRef
	if err := testDB.Where("entity_type = ? AND entity_id = ?", model.EntityTypeLabel, label.ID).
		Find(&refs).Error; err != nil {
		t.Fatal(err)
	}
	if len(refs) != 1 || refs[0].MatchedBy != "curated" {
		t.Fatalf("label refs: %+v", refs)
	}
}

// TestTagFamilyIsIntroOnly pins the deliberate narrowness: a canonical tag's
// NAME is the join key of the whole convergence layer, so it is not a field.
func TestTagFamilyIsIntroOnly(t *testing.T) {
	e := newTaxonomyEngine(t)
	tag := model.CatalogTag{Name: "純愛", Tier: model.TagTierCore, Kind: model.TagKindContent}
	if err := testDB.Create(&tag).Error; err != nil {
		t.Fatal(err)
	}

	intros := []any{map[string]any{"lang": "zh-Hans", "intro": "以恋爱为主线"}}
	snap := mergeOn(t, e, editspec.TypeTag, tag.ID, map[string]any{editspec.FieldTagIntros: intros})
	sameJSON(t, "tag intros", snap[editspec.FieldTagIntros], intros)

	var unknown *editing.UnknownFieldError
	_, _, err := e.CreateProposal(testCtx, editing.CreateProposalInput{
		EntityType: editspec.TypeTag, EntityID: tag.ID,
		Patch: map[string]any{"catalog.tag.name": "純愛2"}, Actor: realActor(100, "admin"),
	})
	if !errors.As(err, &unknown) {
		t.Fatalf("a tag name patch must be an unknown-field error, got %v", err)
	}

	var row model.CatalogTag
	if err := testDB.First(&row, tag.ID).Error; err != nil {
		t.Fatal(err)
	}
	if row.Name != "純愛" {
		t.Fatalf("the tag name moved: %q", row.Name)
	}
}

func TestEngineAndSeriesFamilies(t *testing.T) {
	e := newTaxonomyEngine(t)
	eng := model.CatalogEngine{Name: "KiriKiri", Description: "", Aliases: []byte(`["吉里吉里"]`)}
	series := model.CatalogSeries{DisplayName: "旧シリーズ名", SourceID: curatedSource, ExternalID: "c1"}
	for _, row := range []any{&eng, &series} {
		if err := testDB.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}

	aliases := []any{"吉里吉里", "krkr"}
	snap := mergeOn(t, e, editspec.TypeEngine, eng.ID, map[string]any{
		editspec.FieldEngineName:    "KiriKiri Z",
		editspec.FieldEngineIntro:   "A scripting engine.",
		editspec.FieldEngineAliases: aliases,
	})
	if snap[editspec.FieldEngineName] != "KiriKiri Z" || snap[editspec.FieldEngineIntro] != "A scripting engine." {
		t.Fatalf("engine scalars: %#v", snap)
	}
	sameJSON(t, "engine aliases", snap[editspec.FieldEngineAliases], aliases)

	intros := []any{map[string]any{"lang": "ja", "intro": "シリーズ紹介"}}
	snap = mergeOn(t, e, editspec.TypeSeries, series.ID, map[string]any{
		editspec.FieldSeriesName:   "新シリーズ名",
		editspec.FieldSeriesIntros: intros,
	})
	if snap[editspec.FieldSeriesName] != "新シリーズ名" {
		t.Fatalf("series name: %#v", snap[editspec.FieldSeriesName])
	}
	sameJSON(t, "series intros", snap[editspec.FieldSeriesIntros], intros)
}

// TestWorkOnMergeTouchesTheWork pins the wave-149 gap being closed: a field
// edit that writes only a CHILD table must still move catalog_work.updated_at,
// or the public changes feed never learns the work was edited.
func TestWorkOnMergeTouchesTheWork(t *testing.T) {
	e := newEngine(t)
	work := createWork(t, "作品")

	var before model.CatalogWork
	if err := testDB.First(&before, work.ID).Error; err != nil {
		t.Fatal(err)
	}
	// Push the watermark into the past so a same-transaction now() is strictly
	// newer than it (the touch is the only thing under test here).
	if err := testDB.Exec(
		`UPDATE catalog_work SET updated_at = now() - interval '1 hour' WHERE id = ?`, work.ID).Error; err != nil {
		t.Fatal(err)
	}
	if err := testDB.First(&before, work.ID).Error; err != nil {
		t.Fatal(err)
	}

	mergeField(t, e, work.ID, editspec.FieldWorkIntros, []any{
		map[string]any{"lang": "ja", "intro": "紹介文"},
	})

	var after model.CatalogWork
	if err := testDB.First(&after, work.ID).Error; err != nil {
		t.Fatal(err)
	}
	if !after.UpdatedAt.After(before.UpdatedAt) {
		t.Fatalf("OnMerge did not touch the work: %v -> %v", before.UpdatedAt, after.UpdatedAt)
	}
}
