package editspec_test

import (
	"errors"
	"testing"

	"api/internal/platform/catalog/editspec"
	"api/internal/platform/catalog/model"
	"api/internal/platform/editing"
)

// The mirror gate (wave 155 ruling 2). The hazard is scoped to the works the
// duty chain mirrors — site='galgame_wiki' with a product_work_id — so every
// case here is the SAME patch applied twice: once to a mirrored work (refused)
// and once to a plain registry work (lands). A gate that fired on both would be
// the blunt field-level lock the acceptance explicitly did not choose.

// claimForMirror puts a work inside the mirror steps' ownership scope.
func claimForMirror(t *testing.T, workID, productWorkID int64) {
	t.Helper()
	if err := testDB.Exec(
		`UPDATE catalog_work SET site = 'galgame_wiki', product_work_id = ? WHERE id = ?`,
		productWorkID, workID).Error; err != nil {
		t.Fatalf("claim for mirror: %v", err)
	}
}

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

func TestMirrorGateRefusesTheFiveClaimedFacets(t *testing.T) {
	e := newEngine(t)
	mirrored := createWork(t, "ミラー作品")
	claimForMirror(t, mirrored.ID, 9001)
	free := createWork(t, "自由作品")

	// Non-empty values throughout: mergeLocked only calls Apply for fields that
	// actually CHANGE, so an empty array over an already-empty facet would be a
	// no-op and would never reach the gate (which is itself the documented
	// behaviour — an honest round-trip is never refused).
	tag := model.CatalogTag{Name: "純愛", Tier: model.TagTierCore, Kind: model.TagKindContent}
	if err := testDB.Create(&tag).Error; err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name  string
		patch map[string]any
	}{
		{"titles", map[string]any{editspec.FieldWorkTitles: titlesValue("新しい題")}},
		{"display_nsfw", map[string]any{editspec.FieldWorkDisplayNSFW: true}},
		{"tag_ids", map[string]any{editspec.FieldWorkTagIDs: []any{float64(tag.ID)}}},
		{"covers", map[string]any{editspec.FieldWorkCovers: []any{
			map[string]any{"image_hash": "gate-cover"},
		}}},
		{"screenshots", map[string]any{editspec.FieldWorkScreenshots: []any{
			map[string]any{"image_hash": "gate-shot"},
		}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var gate *editspec.MirrorGateError
			if err := directEdit(t, e, mirrored.ID, tc.patch); !errors.As(err, &gate) {
				t.Fatalf("mirrored work: want MirrorGateError, got %v", err)
			}
			if gate.WorkID != mirrored.ID {
				t.Fatalf("gate names work %d, want %d", gate.WorkID, mirrored.ID)
			}
			// The same patch on a work no mirror step owns must land.
			if err := directEdit(t, e, free.ID, tc.patch); err != nil {
				t.Fatalf("unmirrored work: %v", err)
			}
		})
	}
}

// The intros gate is per LANGUAGE: zh is the one facet 03 §2 wants open on
// exactly these works, and step q never touches it.
func TestMirrorGateIntrosGatesJaEnOnly(t *testing.T) {
	e := newEngine(t)
	work := createWork(t, "紹介作品")
	claimForMirror(t, work.ID, 9002)

	intros := func(pairs ...[2]string) []any {
		out := make([]any, 0, len(pairs))
		for _, p := range pairs {
			out = append(out, map[string]any{"lang": p[0], "intro": p[1]})
		}
		return out
	}

	// zh alone: allowed.
	if err := directEdit(t, e, work.ID, map[string]any{
		editspec.FieldWorkIntros: intros([2]string{"zh-Hans", "简介"}),
	}); err != nil {
		t.Fatalf("zh intro on a mirrored work must be allowed: %v", err)
	}

	// Adding a ja body is step q's coordinate: refused.
	var gate *editspec.MirrorGateError
	if err := directEdit(t, e, work.ID, map[string]any{
		editspec.FieldWorkIntros: intros([2]string{"zh-Hans", "简介"}, [2]string{"ja", "あらすじ"}),
	}); !errors.As(err, &gate) {
		t.Fatalf("ja intro: want MirrorGateError, got %v", err)
	}

	// A ja row the mirror itself wrote round-trips unchanged while zh changes:
	// the honest edit a submitter makes, and it must not be collateral damage.
	if err := testDB.Exec(
		`INSERT INTO catalog_work_intro (work_id, lang, intro, source_id, provenance, src_hash, mt_model, created_at, updated_at)
		 VALUES (?, 'ja', 'あらすじ', 12, 0, '', '', now(), now())`, work.ID).Error; err != nil {
		t.Fatal(err)
	}
	if err := directEdit(t, e, work.ID, map[string]any{
		editspec.FieldWorkIntros: intros([2]string{"zh-Hans", "简介・改"}, [2]string{"ja", "あらすじ"}),
	}); err != nil {
		t.Fatalf("unchanged ja alongside a zh edit must be allowed: %v", err)
	}

	// Changing that ja body IS a change, and so is dropping it.
	if err := directEdit(t, e, work.ID, map[string]any{
		editspec.FieldWorkIntros: intros([2]string{"zh-Hans", "简介・改"}, [2]string{"ja", "別のあらすじ"}),
	}); !errors.As(err, &gate) {
		t.Fatalf("changed ja: want MirrorGateError, got %v", err)
	}
	if err := directEdit(t, e, work.ID, map[string]any{
		editspec.FieldWorkIntros: intros([2]string{"zh-Hans", "简介・改"}),
	}); !errors.As(err, &gate) {
		t.Fatalf("dropped ja: want MirrorGateError, got %v", err)
	}
}

// Revert lands through the same mergeLocked path, so it must meet the same
// gate — otherwise "restore an old revision" is the way around it.
func TestMirrorGateCoversRevert(t *testing.T) {
	e := newEngine(t)
	work := createWork(t, "巻き戻し作品")

	// Two title revisions while the work is still free of the mirror.
	if err := directEdit(t, e, work.ID, map[string]any{editspec.FieldWorkTitles: titlesValue("第一版")}); err != nil {
		t.Fatal(err)
	}
	if err := directEdit(t, e, work.ID, map[string]any{editspec.FieldWorkTitles: titlesValue("第二版")}); err != nil {
		t.Fatal(err)
	}

	// The wiki claims it — from here the duty chain owns its titles.
	claimForMirror(t, work.ID, 9003)

	var gate *editspec.MirrorGateError
	if _, _, err := e.Revert(testCtx, editing.RevertInput{
		EntityType: editspec.TypeWork, EntityID: work.ID, ToSeq: 1,
		Actor: realActor(200, "ren"), Note: "back to v1",
	}); !errors.As(err, &gate) {
		t.Fatalf("revert through the gate: want MirrorGateError, got %v", err)
	}

	var title string
	if err := testDB.Raw(`SELECT title FROM catalog_work_title WHERE work_id = ? AND kind = 0`, work.ID).
		Scan(&title).Error; err != nil {
		t.Fatal(err)
	}
	if title != "第二版" {
		t.Fatalf("refused revert must change nothing, got %q", title)
	}
}

// The scalar lanes wave 154 protected with curated-override, plus links and the
// three edge fields, stay open on a mirrored work — the gate is five facets, not
// a lock on the whole entity.
func TestMirrorGateLeavesTheOpenFieldsAlone(t *testing.T) {
	e := newEngine(t)
	work := createWork(t, "開放作品")
	claimForMirror(t, work.ID, 9004)

	if err := directEdit(t, e, work.ID, map[string]any{
		editspec.FieldWorkDisplayName:   "改名",
		editspec.FieldWorkOLang:         "en",
		editspec.FieldWorkContentRating: float64(model.ContentRatingR18),
		editspec.FieldWorkLinks:         []any{},
		editspec.FieldWorkLabels:        []any{},
		editspec.FieldWorkEngineIDs:     []any{},
		editspec.FieldWorkSeriesIDs:     []any{},
	}); err != nil {
		t.Fatalf("open fields on a mirrored work: %v", err)
	}
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
