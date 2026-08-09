// public_label_rollup_test.go — wave 199: the imprint roll-up.
//
// What is pinned here is the pair of promises the feature makes, because both
// are the kind that fail silently: the counts stay DISJOINT (nothing is counted
// twice, so their sum is the length of the rolled-up list), and every rolled-up
// row is ATTRIBUTED (a company page never claims an imprint's work as its own).
package service

import (
	"testing"

	"api/internal/platform/catalog/model"
)

// rollupFixture builds one holding company as the data really looks: a parent
// with one work of its own, an imprint with two, a work BOTH claim, a spin-off
// whose catalogue is its own, and an r18 work under the imprint for the nsfw
// axis. Returns the parent, the imprint, the spin-off, and the work ids by role.
type rollupFixture struct {
	parent, imprint, spinoff     int64
	own, viaImprint, shared, r18 int64
	spinoffWork                  int64
}

func seedRollup(t *testing.T) rollupFixture {
	t.Helper()
	cleanTables(t)
	cleanLabelGraphTables(t)

	f := rollupFixture{
		parent:  mkLabel(t, "VISUAL ARTS", ""),
		imprint: mkLabel(t, "Key", ""),
		spinoff: mkLabel(t, "fairys", ""),
	}
	// The imprint and the subsidiary are followed; the spin-off is not. Facts
	// are written mirrored, exactly as the wave-186 importer stores them.
	relateMirrored(t, f.parent, f.imprint, model.LabelRelationImprint)
	relateMirrored(t, f.parent, f.spinoff, model.LabelRelationSpawned)

	mk := func(name string, rating int16, claimID int64, labels ...int64) int64 {
		w := createWorkX(t, galgameMediumID, rating, model.WorkStatusLive, name)
		claimLive(t, w.ID, claimID)
		for _, l := range labels {
			if err := testDB.Create(&model.CatalogWorkLabel{
				WorkID: w.ID, LabelID: l, Kind: model.WorkLabelKindBrand,
			}).Error; err != nil {
				t.Fatalf("attribute %s: %v", name, err)
			}
		}
		return w.ID
	}
	f.own = mk("Kanon Memorial", model.ContentRatingAllAges, 9901, f.parent)
	f.viaImprint = mk("CLANNAD", model.ContentRatingAllAges, 9902, f.imprint)
	f.shared = mk("Rewrite", model.ContentRatingAllAges, 9903, f.parent, f.imprint)
	f.r18 = mk("Little Busters EX", model.ContentRatingR18, 9904, f.imprint)
	f.spinoffWork = mk("Canvas", model.ContentRatingAllAges, 9905, f.spinoff)
	return f
}

// TestLabelRollupCountsAreDisjoint is the arithmetic the two numbers promise.
// A work BOTH the company and its imprint attribute must be counted ONCE, on
// the company — otherwise work_count + imprint_work_count over-reports and the
// reader is told the rolled-up page holds more rows than it does.
func TestLabelRollupCountsAreDisjoint(t *testing.T) {
	f := seedRollup(t)
	svc := newPublicSvc()
	ctx := t.Context()

	l, ok, err := svc.Label(ctx, f.parent, false, false, 50, 0)
	if err != nil || !ok {
		t.Fatalf("label: %v ok=%v", err, ok)
	}
	// own + shared, unchanged by the roll-up existing at all.
	if l.WorkCount != 2 {
		t.Fatalf("work_count = %d, want 2 (own + shared)", l.WorkCount)
	}
	// The imprint holds three works, but `shared` is already on the parent and
	// the r18 one is invisible to an sfw caller.
	if l.ImprintWorkCount != 1 {
		t.Fatalf("imprint_work_count = %d, want 1 (CLANNAD only)", l.ImprintWorkCount)
	}

	// The nsfw axis moves BOTH numbers the same way it moves work_count today.
	l, _, err = svc.Label(ctx, f.parent, false, true, 50, 0)
	if err != nil {
		t.Fatalf("label nsfw: %v", err)
	}
	if l.ImprintWorkCount != 2 {
		t.Fatalf("nsfw imprint_work_count = %d, want 2", l.ImprintWorkCount)
	}

	// The spin-off's catalogue is NOT this company's. Rolling it up would put
	// another company's works on this page under this company's name.
	if l.WorkCount+l.ImprintWorkCount != 4 {
		t.Fatalf("rolled-up total = %d, want 4 (the spin-off's work is not ours)", l.WorkCount+l.ImprintWorkCount)
	}
}

// TestLabelRollupPageMatchesTheCountsAndIsAttributed is the invariant the whole
// taxonomy lane rests on — the number beside a chip is the number you get by
// following it — extended to the second number. And it pins the attribution:
// the imprint's rows say `via Key`, the company's own rows say nothing.
func TestLabelRollupPageMatchesTheCountsAndIsAttributed(t *testing.T) {
	f := seedRollup(t)
	svc := newPublicSvc()
	ctx := t.Context()

	base := WorksListFilter{LabelID: f.parent, ClaimStates: []string{model.ClaimStateKeyLive}}

	// Without the flag nothing changes: the frozen contract for every existing
	// caller of works?label_id=.
	plain, err := svc.WorksList(ctx, base, "", 50)
	if err != nil {
		t.Fatalf("plain list: %v", err)
	}
	if len(plain.Items) != 2 {
		t.Fatalf("plain page = %d items, want 2", len(plain.Items))
	}
	for _, it := range plain.Items {
		if it.ViaLabel != nil {
			t.Fatalf("work %d carries via_label outside the roll-up", it.ID)
		}
	}

	rolled := base
	rolled.LabelRollup = true
	page, err := svc.WorksList(ctx, rolled, "", 50)
	if err != nil {
		t.Fatalf("rolled list: %v", err)
	}
	// 2 + 1: exactly work_count + imprint_work_count from the sibling test.
	if len(page.Items) != 3 {
		t.Fatalf("rolled page = %d items, want 3", len(page.Items))
	}
	via := map[int64]string{}
	for _, it := range page.Items {
		if it.ID == f.spinoffWork {
			t.Fatalf("the spin-off's work reached a rolled-up company page")
		}
		if it.ViaLabel != nil {
			via[it.ID] = it.ViaLabel.Name
		}
	}
	// Only CLANNAD came up through the imprint. `shared` is the company's own
	// work too, so claiming it came `via Key` would be a second lie in the
	// other direction.
	if len(via) != 1 || via[f.viaImprint] != "Key" {
		t.Fatalf("via_label = %v, want only work %d via Key", via, f.viaImprint)
	}
}

// TestLabelRollupIgnoresAMergedImprint: a label merged away must stop
// contributing works under its old identity, on this face as on every other.
// The redirect makes it unreachable, and a roll-up that still counted it would
// resurrect a dead page's catalogue inside a live one.
func TestLabelRollupIgnoresAMergedImprint(t *testing.T) {
	f := seedRollup(t)
	svc := newPublicSvc()
	ctx := t.Context()

	if err := testDB.Exec(`UPDATE catalog_label SET deleted_at = now() WHERE id = ?`, f.imprint).Error; err != nil {
		t.Fatalf("soft-delete imprint: %v", err)
	}
	l, ok, err := svc.Label(ctx, f.parent, false, true, 50, 0)
	if err != nil || !ok {
		t.Fatalf("label: %v ok=%v", err, ok)
	}
	if l.ImprintWorkCount != 0 {
		t.Fatalf("imprint_work_count = %d, want 0 after the imprint was merged away", l.ImprintWorkCount)
	}
	rolled := WorksListFilter{LabelID: f.parent, LabelRollup: true, ClaimStates: []string{model.ClaimStateKeyLive}}
	page, err := svc.WorksList(ctx, rolled, "", 50)
	if err != nil {
		t.Fatalf("rolled list: %v", err)
	}
	if len(page.Items) != 2 {
		t.Fatalf("rolled page = %d items, want 2 (the company's own works only)", len(page.Items))
	}
}
