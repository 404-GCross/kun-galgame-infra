package service

import (
	"testing"

	"api/internal/platform/catalog/dto"
	"api/internal/platform/catalog/model"
)

// NextMoe open-API catalog public projection tests (step 03). Integration
// against the real kun_catalog_test schema (service_test.go TestMain). They pin
// the frozen public contract's load-bearing gates: exact-only lookup (probable
// never surfaces), the galgame-non-stub-non-r18 fetchable set, r18 hiding
// everywhere, and the hidden credit-name→person link doctrine.

const (
	srcVNDB   int16 = 2
	srcDlsite int16 = 4
)

func newPublicSvc() *PublicService {
	return NewPublicService(testDB, NewReadService(testDB), testResolve)
}

// createWorkX creates a work with explicit medium / rating / status.
func createWorkX(t *testing.T, medium, rating, status int16, name string) *model.CatalogWork {
	t.Helper()
	w := &model.CatalogWork{MediumID: medium, OLang: "ja", DisplayName: name, ContentRating: rating, Status: status}
	if err := testDB.Create(w).Error; err != nil {
		t.Fatalf("create work: %v", err)
	}
	return w
}

func claimWork(t *testing.T, id int64, site string, productWorkID int64) {
	t.Helper()
	if err := testDB.Exec(`UPDATE catalog_work SET site = ?, product_work_id = ? WHERE id = ?`,
		site, productWorkID, id).Error; err != nil {
		t.Fatalf("claim work: %v", err)
	}
}

func createRelease(t *testing.T, workID int64, y, m, d int16) *model.CatalogRelease {
	t.Helper()
	r := &model.CatalogRelease{WorkID: workID, Kind: model.ReleaseKindDefault}
	if y != 0 {
		r.ReleasedY = &y
	}
	if m != 0 {
		r.ReleasedM = &m
	}
	if d != 0 {
		r.ReleasedD = &d
	}
	if err := testDB.Create(r).Error; err != nil {
		t.Fatalf("create release: %v", err)
	}
	return r
}

func createWorkRelation(t *testing.T, a, b int64) {
	t.Helper()
	var relTypeID int64
	if err := testDB.Raw(`SELECT id FROM catalog_relation_type ORDER BY id LIMIT 1`).Scan(&relTypeID).Error; err != nil || relTypeID == 0 {
		t.Fatalf("seeded relation type lookup: id=%d err=%v", relTypeID, err)
	}
	if err := testDB.Create(&model.CatalogWorkRelation{AWorkID: a, BWorkID: b, RelationTypeID: relTypeID}).Error; err != nil {
		t.Fatalf("create work relation: %v", err)
	}
}

func TestPublicLookupExactOnly(t *testing.T) {
	cleanTables(t)
	svc := newPublicSvc()
	ctx := t.Context()

	w := createWorkX(t, galgameMediumID, model.ContentRatingAllAges, model.WorkStatusLive, "Karenai")
	claimWork(t, w.ID, "galgame_wiki", 1)
	addExternalRef(t, model.EntityTypeWork, w.ID, srcVNDB, "v19658", model.LinkKindExact)
	// A probable anchor MUST NOT resolve on the public face.
	addExternalRef(t, model.EntityTypeWork, w.ID, srcVNDB, "v99999", model.LinkKindProbable)

	// vndb normalization: input with and without the 'v' prefix both resolve.
	for _, in := range []string{"v19658", "19658"} {
		data, found, err := svc.Lookup(ctx, "vndb", in)
		if err != nil || !found {
			t.Fatalf("lookup %q: found=%v err=%v", in, found, err)
		}
		if data.Work == nil || data.Work.ID != w.ID {
			t.Fatalf("lookup %q: work=%v", in, data.Work)
		}
		if data.ClaimedBy == nil || data.ClaimedBy.Site != "galgame_wiki" || data.ClaimedBy.WorkID != 1 {
			t.Fatalf("lookup %q: claimed_by=%v", in, data.ClaimedBy)
		}
		if data.Work.Medium != "galgame" || data.Work.ContentRating != "all_ages" {
			t.Fatalf("lookup %q: brief=%+v", in, data.Work)
		}
	}
	// probable anchor → not found.
	if _, found, _ := svc.Lookup(ctx, "vndb", "v99999"); found {
		t.Fatal("probable anchor must not resolve on the public face")
	}
	// unknown source / missing id → not found.
	if _, found, _ := svc.Lookup(ctx, "vndb", "v00000"); found {
		t.Fatal("unknown external id must 404")
	}
	if _, found, _ := svc.Lookup(ctx, "nosuchsource", "x"); found {
		t.Fatal("unknown source key must 404")
	}
}

func TestPublicLookupR18Hidden(t *testing.T) {
	cleanTables(t)
	svc := newPublicSvc()
	ctx := t.Context()

	w := createWorkX(t, galgameMediumID, model.ContentRatingR18, model.WorkStatusLive, "R18 game")
	addExternalRef(t, model.EntityTypeWork, w.ID, srcVNDB, "v100", model.LinkKindExact)
	if _, found, err := svc.Lookup(ctx, "vndb", "v100"); err != nil || found {
		t.Fatalf("r18 work must be hidden from lookup: found=%v err=%v", found, err)
	}
}

func TestPublicLookupReleaseAnchor(t *testing.T) {
	cleanTables(t)
	svc := newPublicSvc()
	ctx := t.Context()

	w := createWorkX(t, galgameMediumID, model.ContentRatingAllAges, model.WorkStatusLive, "SKU work")
	rel := createRelease(t, w.ID, 2016, 11, 25)
	addExternalRef(t, model.EntityTypeRelease, rel.ID, srcDlsite, "RJ123456", model.LinkKindExact)

	data, found, err := svc.Lookup(ctx, "dlsite", "RJ123456")
	if err != nil || !found || data.Work == nil || data.Work.ID != w.ID {
		t.Fatalf("release anchor lookup: found=%v work=%v err=%v", found, data.Work, err)
	}
}

func TestPublicLookupBatchOrder(t *testing.T) {
	cleanTables(t)
	svc := newPublicSvc()
	ctx := t.Context()

	w := createWorkX(t, galgameMediumID, model.ContentRatingAllAges, model.WorkStatusLive, "batch hit")
	addExternalRef(t, model.EntityTypeWork, w.ID, srcVNDB, "v1", model.LinkKindExact)

	items, err := svc.LookupBatch(ctx, []dto.PublicLookupPair{
		{Source: "vndb", ExternalID: "v1"},
		{Source: "vndb", ExternalID: "v404"},
		{Source: "vndb", ExternalID: "1"}, // normalizes to v1 → same hit
	})
	if err != nil {
		t.Fatalf("batch: %v", err)
	}
	if len(items) != 3 {
		t.Fatalf("batch len=%d", len(items))
	}
	if items[0].Work == nil || items[0].Work.ID != w.ID {
		t.Fatalf("item0 miss: %+v", items[0])
	}
	if items[1].Work != nil {
		t.Fatalf("item1 should be a null miss: %+v", items[1])
	}
	if items[2].Work == nil || items[2].Work.ID != w.ID {
		t.Fatalf("item2 (normalized) miss: %+v", items[2])
	}
}

func TestPublicWorkDetailFetchableSet(t *testing.T) {
	cleanTables(t)
	svc := newPublicSvc()
	ctx := t.Context()

	live := createWorkX(t, galgameMediumID, model.ContentRatingAllAges, model.WorkStatusLive, "live galgame")
	anime := createWorkX(t, 4, model.ContentRatingAllAges, model.WorkStatusLive, "anime")
	stub := createWorkX(t, galgameMediumID, model.ContentRatingAllAges, model.WorkStatusStub, "stub")
	r18 := createWorkX(t, galgameMediumID, model.ContentRatingR18, model.WorkStatusLive, "r18")
	merged := createWorkX(t, galgameMediumID, model.ContentRatingAllAges, model.WorkStatusMerged, "merged")

	cases := []struct {
		id   int64
		want bool
	}{
		{live.ID, true}, {anime.ID, false}, {stub.ID, false}, {r18.ID, false}, {merged.ID, false},
		{99999, false},
	}
	for _, c := range cases {
		_, found, err := svc.WorkDetail(ctx, c.id, PublicInclude{})
		if err != nil {
			t.Fatalf("work %d: %v", c.id, err)
		}
		if found != c.want {
			t.Fatalf("work %d: found=%v want=%v", c.id, found, c.want)
		}
	}
}

func TestPublicWorkRefsExactOnlyAndRelations(t *testing.T) {
	cleanTables(t)
	svc := newPublicSvc()
	ctx := t.Context()

	w := createWorkX(t, galgameMediumID, model.ContentRatingAllAges, model.WorkStatusLive, "main")
	addExternalRef(t, model.EntityTypeWork, w.ID, srcVNDB, "v42", model.LinkKindExact)
	addExternalRef(t, model.EntityTypeWork, w.ID, 3, "555", model.LinkKindProbable) // bangumi probable

	sfwOther := createWorkX(t, galgameMediumID, model.ContentRatingAllAges, model.WorkStatusLive, "sfw related")
	r18Other := createWorkX(t, galgameMediumID, model.ContentRatingR18, model.WorkStatusLive, "r18 related")
	createWorkRelation(t, w.ID, sfwOther.ID)
	createWorkRelation(t, w.ID, r18Other.ID)

	rec, found, err := svc.WorkDetail(ctx, w.ID, PublicInclude{Relations: true})
	if err != nil || !found {
		t.Fatalf("detail: found=%v err=%v", found, err)
	}
	if len(rec.Refs) != 1 || rec.Refs[0].Source != "vndb" || rec.Refs[0].ExternalID != "v42" {
		t.Fatalf("refs must be exact-only: %+v", rec.Refs)
	}
	if len(rec.Relations) != 1 || rec.Relations[0].Work.ID != sfwOther.ID {
		t.Fatalf("relations must drop the r18 end: %+v", rec.Relations)
	}
}

func TestPublicWorkCreditsInclude(t *testing.T) {
	cleanTables(t)
	svc := newPublicSvc()
	ctx := t.Context()

	w := createWorkX(t, galgameMediumID, model.ContentRatingAllAges, model.WorkStatusLive, "credited")
	name := createCreditName(t, nil, "麻枝准")
	createCredit(t, w.ID, name.ID, seededRoleID(t), nil)

	rec, found, err := svc.WorkDetail(ctx, w.ID, PublicInclude{Credits: true})
	if err != nil || !found {
		t.Fatalf("detail: found=%v err=%v", found, err)
	}
	if len(rec.Credits) != 1 || len(rec.Credits[0].Credits) != 1 || rec.Credits[0].Credits[0].ID != name.ID {
		t.Fatalf("credits projection: %+v", rec.Credits)
	}
	// The bare record (no include) omits credits entirely.
	bare, _, _ := svc.WorkDetail(ctx, w.ID, PublicInclude{})
	if bare.Credits != nil {
		t.Fatalf("bare record must omit credits: %+v", bare.Credits)
	}
}

func TestPublicPersonHiddenLinkAndR18Drop(t *testing.T) {
	cleanTables(t)
	svc := newPublicSvc()
	ctx := t.Context()

	p := createPerson(t, "Key")
	nPublic := createCreditName(t, &p.ID, "麻枝准")
	// A second name of the same person, but HIDDEN-linked.
	nHidden := &model.CatalogCreditName{
		PersonID: &p.ID, Name: "裏名義", Kind: model.CreditNameKindDistinctPersona,
		LinkVisibility: model.LinkVisibilityHidden,
	}
	if err := testDB.Create(nHidden).Error; err != nil {
		t.Fatalf("create hidden name: %v", err)
	}

	sfw := createWorkX(t, galgameMediumID, model.ContentRatingAllAges, model.WorkStatusLive, "sfw")
	r18 := createWorkX(t, galgameMediumID, model.ContentRatingR18, model.WorkStatusLive, "r18")
	role := seededRoleID(t)
	createCredit(t, sfw.ID, nPublic.ID, role, nil)
	createCredit(t, r18.ID, nPublic.ID, role, nil)

	// Public-linked name: person grouping surfaces, but the hidden sibling never
	// does; r18 credits are dropped.
	got, found, err := svc.Person(ctx, nPublic.ID, true, 50, 0)
	if err != nil || !found {
		t.Fatalf("person: found=%v err=%v", found, err)
	}
	if got.PersonID != p.ID {
		t.Fatalf("public link must expose person_id: %d", got.PersonID)
	}
	if len(got.Siblings) != 0 {
		t.Fatalf("hidden sibling must not surface: %+v", got.Siblings)
	}
	if len(got.Credits) != 1 || got.Credits[0].Work.ID != sfw.ID {
		t.Fatalf("r18 credit must be dropped: %+v", got.Credits)
	}

	// Hidden-linked name: appears as an independent identity (no person_id).
	h, found, err := svc.Person(ctx, nHidden.ID, false, 50, 0)
	if err != nil || !found {
		t.Fatalf("hidden person: found=%v err=%v", found, err)
	}
	if h.PersonID != 0 {
		t.Fatalf("hidden link must withhold person_id: %d", h.PersonID)
	}
}
