package editspec_test

import (
	"errors"
	"strings"
	"testing"

	"api/internal/platform/catalog/editspec"
	"api/internal/platform/catalog/model"
	"api/internal/platform/editing"
)

// E1 acceptance lines: the catalog.work.titles list field (validate / apply
// full-replace / display_name derivation / round-trips) and the letmoe site
// overlay (trusted propose + owner automerge) end to end.

// title builds one wire-shaped element (decoded JSON: map + float64 kind).
func title(lang, name string, kind int16) map[string]any {
	return map[string]any{"lang": lang, "title": name, "kind": float64(kind)}
}

func titleLatin(lang, name, latin string, kind int16) map[string]any {
	el := title(lang, name, kind)
	el["latin"] = latin
	return el
}

// letmoeActor mirrors the letmoe BFF assertion posture: site=letmoe, roles
// asserted EMPTY (nil HasPerm fails closed — tenant users never carry the
// global curation perms), rights come from trust tier + the site overlay.
func letmoeActor(uid int64, tier int16) editing.PolicyContext {
	return editing.PolicyContext{UserID: uid, Site: "letmoe", TrustTier: tier}
}

// nextProductID keeps (medium, site, product_work_id) claims unique across
// subtests (uq_catalog_work_claim).
var nextProductID int64 = 4242

func createClaimedWork(t *testing.T, site string, displayName, olang string) *model.CatalogWork {
	t.Helper()
	nextProductID++
	productID := nextProductID
	w := &model.CatalogWork{
		MediumID: 1, Site: &site, ProductWorkID: &productID,
		OLang: olang, DisplayName: displayName,
		ContentRating: model.ContentRatingAllAges, Status: model.WorkStatusLive,
	}
	if err := testDB.Create(w).Error; err != nil {
		t.Fatalf("create claimed work: %v", err)
	}
	return w
}

func loadTitleRows(t *testing.T, workID int64) []model.CatalogWorkTitle {
	t.Helper()
	var rows []model.CatalogWorkTitle
	if err := testDB.Where("work_id = ?", workID).Order("id").Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	return rows
}

func TestTitlesValidator(t *testing.T) {
	e := newEngine(t)
	work := createWork(t, "validator target")
	editor := realActor(100, "admin")

	bad := []struct {
		name  string
		value any
	}{
		{"not an array", "x"},
		{"empty array", []any{}},
		{"non-object element", []any{"x"}},
		{"unknown key", []any{map[string]any{"lang": "ja", "title": "a", "kind": float64(0), "extra": 1}}},
		{"missing lang", []any{map[string]any{"title": "a", "kind": float64(0)}}},
		{"bad lang", []any{title("xx", "a", 0)}},
		{"empty title", []any{title("ja", "  ", 0)}},
		{"long title", []any{title("ja", strings.Repeat("あ", 501), 0)}},
		{"kind out of range", []any{title("ja", "a", 4)}},
		{"kind not integer", []any{map[string]any{"lang": "ja", "title": "a", "kind": 1.5}}},
		{"empty latin", []any{titleLatin("ja", "a", " ", 0)}},
		{"duplicate element", []any{title("ja", "a", 0), title("ja", "a", 0)}},
		{"no official title", []any{title("ja", "a", 1)}},
	}
	var valErr *editing.ValidationError
	for _, c := range bad {
		if _, _, err := e.CreateProposal(testCtx, editing.CreateProposalInput{
			EntityType: editspec.TypeWork, EntityID: work.ID,
			Patch: map[string]any{editspec.FieldWorkTitles: c.value}, Actor: editor,
		}); !errors.As(err, &valErr) {
			t.Errorf("%s: want ValidationError, got %v", c.name, err)
		}
	}
}

func TestTitlesApplyAndDerivation(t *testing.T) {
	e := newEngine(t)
	reviewer := realActor(200, "ren")

	mergeTitles := func(t *testing.T, workID int64, titles []any, extra map[string]any) *editing.Revision {
		t.Helper()
		patch := map[string]any{editspec.FieldWorkTitles: titles}
		for k, v := range extra {
			patch[k] = v
		}
		prop, _, err := e.CreateProposal(testCtx, editing.CreateProposalInput{
			EntityType: editspec.TypeWork, EntityID: workID, Patch: patch, Actor: realActor(100, "admin"),
		})
		if err != nil {
			t.Fatalf("propose titles: %v", err)
		}
		rev, err := e.MergeProposal(testCtx, prop.ID, reviewer, "")
		if err != nil {
			t.Fatalf("merge titles: %v", err)
		}
		return rev
	}

	t.Run("full replace + olang official wins", func(t *testing.T) {
		work := createWork(t, "旧名")
		// Pre-existing row that the full replace must remove.
		if err := testDB.Create(&model.CatalogWorkTitle{
			WorkID: work.ID, Lang: "en", Title: "stale", Kind: model.WorkTitleKindAlias,
		}).Error; err != nil {
			t.Fatal(err)
		}
		mergeTitles(t, work.ID, []any{
			title("zh", "中文名", 0),
			titleLatin("ja", "日本語名", "nihongo mei", 0),
			title("ja", "にほんごめい", 3),
		}, nil)

		rows := loadTitleRows(t, work.ID)
		if len(rows) != 3 {
			t.Fatalf("rows after replace: %+v", rows)
		}
		if rows[0].Title != "中文名" || rows[1].Title != "日本語名" || rows[2].Kind != model.WorkTitleKindSearchHint {
			t.Fatalf("row order/content: %+v", rows)
		}
		if rows[1].Latin == nil || *rows[1].Latin != "nihongo mei" {
			t.Fatalf("latin: %+v", rows[1])
		}
		var w model.CatalogWork
		if err := testDB.First(&w, work.ID).Error; err != nil {
			t.Fatal(err)
		}
		// olang=ja → the ja official wins even though zh comes first.
		if w.DisplayName != "日本語名" {
			t.Fatalf("derived display_name: %q", w.DisplayName)
		}
	})

	t.Run("no olang official falls back to first official", func(t *testing.T) {
		work := createWork(t, "旧名") // olang=ja
		mergeTitles(t, work.ID, []any{
			title("zh", "中文名", 0),
			title("en", "English Name", 0),
		}, nil)
		var w model.CatalogWork
		if err := testDB.First(&w, work.ID).Error; err != nil {
			t.Fatal(err)
		}
		if w.DisplayName != "中文名" {
			t.Fatalf("fallback display_name: %q", w.DisplayName)
		}
	})

	t.Run("same-merge olang change feeds the derivation", func(t *testing.T) {
		work := createWork(t, "旧名") // olang=ja
		mergeTitles(t, work.ID, []any{
			title("ja", "日本語名", 0),
			title("en", "English Name", 0),
		}, map[string]any{editspec.FieldWorkOLang: "en"})
		var w model.CatalogWork
		if err := testDB.First(&w, work.ID).Error; err != nil {
			t.Fatal(err)
		}
		// olang applied before titles (sorted key order) → en official wins.
		if w.OLang != "en" || w.DisplayName != "English Name" {
			t.Fatalf("after olang+titles merge: olang=%q display=%q", w.OLang, w.DisplayName)
		}
	})

	t.Run("titles derivation overrides a same-merge display_name", func(t *testing.T) {
		work := createWork(t, "旧名")
		mergeTitles(t, work.ID, []any{title("ja", "タイトル", 0)},
			map[string]any{editspec.FieldWorkDisplayName: "手書き名"})
		var w model.CatalogWork
		if err := testDB.First(&w, work.ID).Error; err != nil {
			t.Fatal(err)
		}
		// display_name applies before titles (sorted key order); the titles
		// derivation is the invariant keeper and lands last. Pinned behavior.
		if w.DisplayName != "タイトル" {
			t.Fatalf("derivation must win: %q", w.DisplayName)
		}
	})

	t.Run("identical titles are a no-op", func(t *testing.T) {
		work := createWork(t, "旧名")
		value := []any{titleLatin("ja", "同一", "douitsu", 0), title("zh", "同一中文", 1)}
		mergeTitles(t, work.ID, value, nil)
		// The same value again: the effective patch has no effective change.
		prop, _, err := e.CreateProposal(testCtx, editing.CreateProposalInput{
			EntityType: editspec.TypeWork, EntityID: work.ID,
			Patch: map[string]any{editspec.FieldWorkTitles: value}, Actor: realActor(100, "admin"),
		})
		if err != nil {
			t.Fatalf("no-op propose: %v", err)
		}
		if _, err := e.MergeProposal(testCtx, prop.ID, reviewer, ""); !errors.Is(err, editing.ErrNoEffectiveChanges) {
			t.Fatalf("no-op merge: %v", err)
		}
	})

	t.Run("revert restores titles and the derived display_name", func(t *testing.T) {
		work := createWork(t, "旧名")
		mergeTitles(t, work.ID, []any{title("ja", "初版", 0)}, nil)
		mergeTitles(t, work.ID, []any{title("ja", "第二版", 0), title("zh", "第二版中文", 1)}, nil)

		if _, _, err := e.Revert(testCtx, editing.RevertInput{
			EntityType: editspec.TypeWork, EntityID: work.ID, ToSeq: 1, Actor: reviewer,
		}); err != nil {
			t.Fatalf("revert: %v", err)
		}
		rows := loadTitleRows(t, work.ID)
		if len(rows) != 1 || rows[0].Title != "初版" {
			t.Fatalf("reverted rows: %+v", rows)
		}
		var w model.CatalogWork
		if err := testDB.First(&w, work.ID).Error; err != nil {
			t.Fatal(err)
		}
		if w.DisplayName != "初版" {
			t.Fatalf("reverted display_name: %q", w.DisplayName)
		}
	})
}

// The letmoe tenant policy end to end (02 号裁定 4): trusted propose + owner
// automerge, with roles asserted empty (the BFF posture).
func TestLetmoeSiteOverlay(t *testing.T) {
	e := newEngine(t)

	t.Run("own claimed work direct-edits in one revision", func(t *testing.T) {
		work := createClaimedWork(t, "letmoe", "自家作品", "ja")
		prop, rev, err := e.CreateProposal(testCtx, editing.CreateProposalInput{
			EntityType: editspec.TypeWork, EntityID: work.ID,
			Patch: map[string]any{editspec.FieldWorkTitles: []any{
				title("ja", "自家作品・改", 0),
			}},
			Actor: letmoeActor(500, 2),
		})
		if err != nil {
			t.Fatalf("letmoe direct edit: %v", err)
		}
		if rev == nil || rev.Action != editing.ActionDirect || prop.Status != editing.StatusMerged {
			t.Fatalf("must automerge: rev=%+v status=%d", rev, prop.Status)
		}
		revs, err := e.ListRevisions(testCtx, editspec.TypeWork, work.ID, 0)
		if err != nil || len(revs) != 1 {
			t.Fatalf("single revision: %d err=%v", len(revs), err)
		}
		var w model.CatalogWork
		if err := testDB.First(&w, work.ID).Error; err != nil {
			t.Fatal(err)
		}
		if w.DisplayName != "自家作品・改" {
			t.Fatalf("derived display_name: %q", w.DisplayName)
		}
	})

	t.Run("unclaimed work proposal stays open", func(t *testing.T) {
		work := createWork(t, "公共作品") // unclaimed (Site nil)
		prop, rev, err := e.CreateProposal(testCtx, editing.CreateProposalInput{
			EntityType: editspec.TypeWork, EntityID: work.ID,
			Patch: map[string]any{editspec.FieldWorkDisplayName: "公共作品・提案"},
			Actor: letmoeActor(500, 2),
		})
		if err != nil {
			t.Fatalf("letmoe public proposal: %v", err)
		}
		if rev != nil || prop.Status != editing.StatusOpen {
			t.Fatalf("must stay open: rev=%v status=%d", rev, prop.Status)
		}
		// The existing review authority can still merge it.
		if _, err := e.MergeProposal(testCtx, prop.ID, realActor(200, "ren"), "ok"); err != nil {
			t.Fatalf("reviewer merge: %v", err)
		}
	})

	t.Run("another site's claim is not letmoe's to direct-edit", func(t *testing.T) {
		work := createClaimedWork(t, "moyu", "他站作品", "ja")
		prop, rev, err := e.CreateProposal(testCtx, editing.CreateProposalInput{
			EntityType: editspec.TypeWork, EntityID: work.ID,
			Patch: map[string]any{editspec.FieldWorkDisplayName: "他站作品・提案"},
			Actor: letmoeActor(500, 2),
		})
		if err != nil {
			t.Fatalf("cross-site proposal: %v", err)
		}
		if rev != nil || prop.Status != editing.StatusOpen {
			t.Fatalf("must stay open: rev=%v status=%d", rev, prop.Status)
		}
	})

	t.Run("below trusted tier cannot propose on letmoe sites", func(t *testing.T) {
		work := createClaimedWork(t, "letmoe", "自家作品", "ja")
		var permErr *editing.PermissionError
		if _, _, err := e.CreateProposal(testCtx, editing.CreateProposalInput{
			EntityType: editspec.TypeWork, EntityID: work.ID,
			Patch: map[string]any{editspec.FieldWorkDisplayName: "x"},
			Actor: letmoeActor(501, 0),
		}); !errors.As(err, &permErr) {
			t.Fatalf("tier 0 propose: %v", err)
		}
	})

	t.Run("nextmoe default site is unaffected by the overlay", func(t *testing.T) {
		work := createClaimedWork(t, "letmoe", "自家作品", "ja")
		// A trusted-but-permless actor on the DEFAULT site still may not propose.
		nextmoeTrusted := editing.PolicyContext{UserID: 502, Site: "nextmoe", TrustTier: 4}
		var permErr *editing.PermissionError
		if _, _, err := e.CreateProposal(testCtx, editing.CreateProposalInput{
			EntityType: editspec.TypeWork, EntityID: work.ID,
			Patch: map[string]any{editspec.FieldWorkDisplayName: "x"},
			Actor: nextmoeTrusted,
		}); !errors.As(err, &permErr) {
			t.Fatalf("default-site propose: %v", err)
		}
	})
}
