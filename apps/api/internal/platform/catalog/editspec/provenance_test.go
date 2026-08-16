package editspec_test

import (
	"encoding/json"
	"sort"
	"testing"

	"api/internal/platform/catalog/editspec"
	"api/internal/platform/catalog/model"
	"api/internal/platform/editing"
	"api/internal/platform/provenance"

	"gorm.io/datatypes"
)

func workProv(t *testing.T, workID int64) datatypes.JSON {
	t.Helper()
	var w model.CatalogWork
	if err := testDB.Select("id", "field_provenance").First(&w, workID).Error; err != nil {
		t.Fatalf("reload work %d: %v", workID, err)
	}
	return w.FieldProvenance
}

func labelProv(t *testing.T, labelID int64) datatypes.JSON {
	t.Helper()
	var l model.CatalogLabel
	if err := testDB.Select("id", "field_provenance").First(&l, labelID).Error; err != nil {
		t.Fatalf("reload label %d: %v", labelID, err)
	}
	return l.FieldProvenance
}

func TestEngineStampsCuratedProvenance(t *testing.T) {
	e := newTaxonomyEngine(t)
	work := createWork(t, "初期名")

	if got := provenance.FirstSource(workProv(t, work.ID), "display_name"); got != "" {
		t.Fatalf("a fresh work must carry no stamp, got %q", got)
	}

	mergeOn(t, e, editspec.TypeWork, work.ID, map[string]any{
		editspec.FieldWorkDisplayName:   "正式名",
		editspec.FieldWorkOLang:         "en",
		editspec.FieldWorkContentRating: float64(model.ContentRatingR18),
		editspec.FieldWorkDisplayNSFW:   true,
	})

	prov := workProv(t, work.ID)
	for _, column := range []string{"display_name", "olang", "content_rating", "display_nsfw"} {
		if got := provenance.FirstSource(prov, column); got != provenance.SourceCurated {
			t.Errorf("catalog_work.field_provenance[%s] head = %q, want %q", column, got, provenance.SourceCurated)
		}
	}

	label := model.CatalogLabel{DisplayName: "旧ブランド", Kind: model.LabelKindDoujinCircle}
	if err := testDB.Create(&label).Error; err != nil {
		t.Fatal(err)
	}
	mergeOn(t, e, editspec.TypeLabel, label.ID, map[string]any{editspec.FieldLabelName: "新ブランド"})
	if got := provenance.FirstSource(labelProv(t, label.ID), "display_name"); got != provenance.SourceCurated {
		t.Errorf("catalog_label.field_provenance[display_name] head = %q, want %q", got, provenance.SourceCurated)
	}
}

func TestProvenanceStampGoesToTheArrayHead(t *testing.T) {
	e := newEngine(t)
	work := createWork(t, "importer name")
	if err := testDB.Exec(
		`UPDATE catalog_work SET field_provenance =
		 '{"display_name":[{"source":"vndb","at":"2026-01-01T00:00:00Z"}]}' WHERE id = ?`,
		work.ID).Error; err != nil {
		t.Fatal(err)
	}

	mergeField(t, e, work.ID, editspec.FieldWorkDisplayName, "human name")

	var doc map[string][]provenance.Entry
	if err := json.Unmarshal(workProv(t, work.ID), &doc); err != nil {
		t.Fatal(err)
	}
	entries := doc["display_name"]
	if len(entries) != 2 {
		t.Fatalf("entries = %+v, want the curated stamp unshifted onto the vndb one", entries)
	}
	if entries[0].Source != provenance.SourceCurated || entries[1].Source != "vndb" {
		t.Fatalf("order = %+v, want curated first", entries)
	}
	if entries[0].At == "" {
		t.Error("the stamp must carry a timestamp")
	}
}

// The list is the contract: a field that stamps a head-table column must not be
// one of doc 21 §8.2's machine-owned fields (anchors, external rating snapshots,
// derived/rollup columns, machine image-grade rows). Adding a target here is a
// deliberate act, so the test names every one of them.
func TestOnlyDeclaredFieldsStampProvenance(t *testing.T) {
	reg := editing.NewRegistry()
	if err := editspec.RegisterWork(reg, testDB); err != nil {
		t.Fatalf("register catalog.work: %v", err)
	}
	if err := editspec.RegisterTaxonomy(reg, testDB); err != nil {
		t.Fatalf("register taxonomy: %v", err)
	}

	var got []string
	for _, entityType := range []string{
		editspec.TypeWork, editspec.TypeLabel, editspec.TypeTag, editspec.TypeEngine, editspec.TypeSeries,
	} {
		spec, ok := reg.Type(entityType)
		if !ok {
			t.Fatalf("%s is not registered", entityType)
		}
		for i := range spec.Fields {
			f := &spec.Fields[i]
			if f.Provenance == nil {
				continue
			}
			got = append(got, f.Key+" -> "+f.Provenance.Table+"."+f.Provenance.Column)
		}
	}
	sort.Strings(got)

	want := []string{
		"catalog.label.name -> catalog_label.display_name",
		"catalog.work.content_rating -> catalog_work.content_rating",
		"catalog.work.display_name -> catalog_work.display_name",
		"catalog.work.display_nsfw -> catalog_work.display_nsfw",
		"catalog.work.olang -> catalog_work.olang",
	}
	if len(got) != len(want) {
		t.Fatalf("stamping fields = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("stamping field %d = %q, want %q", i, got[i], want[i])
		}
	}
}
