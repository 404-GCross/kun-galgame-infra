package service

import (
	"reflect"
	"testing"

	"api/internal/platform/catalog/dto"
	"api/internal/platform/catalog/model"
	srcb "api/internal/platform/catalog/srcbangumi"
)

// NextMoe open-API catalog public projection tests (step 03). Integration
// against the real kun_catalog_test schema (service_test.go TestMain). They pin
// the frozen public contract's load-bearing gates: exact-only lookup (probable
// never surfaces), the galgame-non-stub-non-r18 fetchable set, r18 hiding
// everywhere, and the hidden credit-name→person link doctrine.

const (
	srcVNDB         int16 = 2
	srcErogamespace int16 = 5
	srcDlsite       int16 = 4
)

func newPublicSvc() *PublicService {
	return NewPublicService(testDB, NewReadService(testDB), testResolve, "")
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
		data, found, err := svc.Lookup(ctx, "vndb", in, false)
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
	if _, found, _ := svc.Lookup(ctx, "vndb", "v99999", false); found {
		t.Fatal("probable anchor must not resolve on the public face")
	}
	// unknown source / missing id → not found.
	if _, found, _ := svc.Lookup(ctx, "vndb", "v00000", false); found {
		t.Fatal("unknown external id must 404")
	}
	if _, found, _ := svc.Lookup(ctx, "nosuchsource", "x", false); found {
		t.Fatal("unknown source key must 404")
	}

	// ErogameScape spelling contract: the public canonical "erogamescape" AND
	// the internal registry key "erogamespace" both resolve; the projected
	// refs always carry the public spelling.
	addExternalRef(t, model.EntityTypeWork, w.ID, srcErogamespace, "23956", model.LinkKindExact)
	for _, src := range []string{"erogamescape", "erogamespace"} {
		if _, found, err := svc.Lookup(ctx, src, "23956", false); err != nil || !found {
			t.Fatalf("lookup via %q: found=%v err=%v", src, found, err)
		}
	}
	detail, found, err := svc.WorkDetail(ctx, w.ID, PublicInclude{}, false, 0)
	if err != nil || !found {
		t.Fatalf("work detail: found=%v err=%v", found, err)
	}
	for _, r := range detail.Refs {
		if r.Source == "erogamespace" {
			t.Fatal("public refs must carry the public spelling erogamescape, never the registry key")
		}
	}
}

func TestPublicLookupR18Hidden(t *testing.T) {
	cleanTables(t)
	svc := newPublicSvc()
	ctx := t.Context()

	w := createWorkX(t, galgameMediumID, model.ContentRatingR18, model.WorkStatusLive, "R18 game")
	addExternalRef(t, model.EntityTypeWork, w.ID, srcVNDB, "v100", model.LinkKindExact)
	if _, found, err := svc.Lookup(ctx, "vndb", "v100", false); err != nil || found {
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

	data, found, err := svc.Lookup(ctx, "dlsite", "RJ123456", false)
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
	}, false)
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
	// The resolved type token is echoed even when the pair omitted it.
	for i, it := range items {
		if it.Type != "work" {
			t.Fatalf("item%d type=%q want the resolved default \"work\"", i, it.Type)
		}
	}
}

// srcBangumiPub is the seeded bangumi source id — the one registry whose id
// spaces genuinely OVERLAP across families (subject 12345 and character 12345
// are different things), which is why the lookup face needs `type` at all.
const srcBangumiPub int16 = 3

// TestPublicLookupTypedEntities pins the additive `type` parameter: the same
// exact-anchor reverse-lookup answers for name / character / label too, `type`
// (not the anchor) decides which family answers, and each hit carries ONLY its
// own block — work/claimed_by stay null off the work lane.
func TestPublicLookupTypedEntities(t *testing.T) {
	cleanTables(t)
	svc := newPublicSvc()
	ctx := t.Context()

	w := createWorkX(t, galgameMediumID, model.ContentRatingAllAges, model.WorkStatusLive, "同 id 作品")
	claimWork(t, w.ID, "galgame_wiki", 7)
	addExternalRef(t, model.EntityTypeWork, w.ID, srcBangumiPub, "12345", model.LinkKindExact)

	ch := createCharacter(t, "同 id 角色")
	addExternalRef(t, model.EntityTypeCharacter, ch.ID, srcBangumiPub, "12345", model.LinkKindExact)

	// vndb staff anchors are stored as BARE numbers (characters are c123, labels
	// p123) — the typed lane must pass the id through verbatim instead of
	// applying the work-only "v" prefix rule, or this can never resolve.
	n := createCreditName(t, nil, "テスト脚本家")
	addExternalRef(t, model.EntityTypeCreditName, n.ID, srcVNDB, "54321", model.LinkKindExact)

	lbl := &model.CatalogLabel{DisplayName: "テストブランド", Kind: model.LabelKindGameBrand}
	if err := testDB.Create(lbl).Error; err != nil {
		t.Fatalf("create label: %v", err)
	}
	addExternalRef(t, model.EntityTypeLabel, lbl.ID, srcVNDB, "p129", model.LinkKindExact)
	// Exact-only is the red line on the typed lanes too.
	addExternalRef(t, model.EntityTypeLabel, lbl.ID, srcVNDB, "p999", model.LinkKindProbable)

	// An absent type is the ORIGINAL work contract, unchanged — and the colliding
	// bangumi character anchor must not disturb it.
	untyped, found, err := svc.Lookup(ctx, "bangumi", "12345", false)
	if err != nil || !found || untyped.Work == nil || untyped.Work.ID != w.ID {
		t.Fatalf("absent type must resolve the work: found=%v work=%v err=%v", found, untyped.Work, err)
	}
	if untyped.ClaimedBy == nil || untyped.ClaimedBy.Site != "galgame_wiki" {
		t.Fatalf("absent type must keep claimed_by: %+v", untyped.ClaimedBy)
	}
	if untyped.Name != nil || untyped.Character != nil || untyped.Label != nil {
		t.Fatalf("the work lane must leave the typed blocks empty: %+v", untyped)
	}

	// type=character on the SAME (source, external_id) answers the character.
	got, found, err := svc.LookupTyped(ctx, "bangumi", "12345", model.EntityTypeCharacter, false)
	if err != nil || !found || got.Character == nil || got.Character.ID != ch.ID {
		t.Fatalf("type=character: found=%v character=%+v err=%v", found, got.Character, err)
	}
	if got.Work != nil || got.ClaimedBy != nil || got.Name != nil || got.Label != nil {
		t.Fatalf("type=character must populate character only: %+v", got)
	}

	got, found, err = svc.LookupTyped(ctx, "vndb", "54321", model.EntityTypeCreditName, false)
	if err != nil || !found || got.Name == nil || got.Name.ID != n.ID {
		t.Fatalf("type=name: found=%v name=%+v err=%v", found, got.Name, err)
	}
	if got.Work != nil || got.ClaimedBy != nil || got.Character != nil || got.Label != nil {
		t.Fatalf("type=name must populate name only: %+v", got)
	}

	got, found, err = svc.LookupTyped(ctx, "vndb", "p129", model.EntityTypeLabel, false)
	if err != nil || !found || got.Label == nil || got.Label.ID != lbl.ID {
		t.Fatalf("type=label: found=%v label=%+v err=%v", found, got.Label, err)
	}
	if got.Work != nil || got.ClaimedBy != nil || got.Name != nil || got.Character != nil {
		t.Fatalf("type=label must populate label only: %+v", got)
	}

	// Cross-family misses: the anchor exists, but not for THAT family. And a
	// probable anchor never resolves, typed or not.
	for _, c := range []struct {
		name       string
		source     string
		ext        string
		entityType int16
	}{
		{"work anchor asked as a name", "bangumi", "12345", model.EntityTypeCreditName},
		{"character anchor asked as a label", "bangumi", "12345", model.EntityTypeLabel},
		{"label anchor asked as a work", "vndb", "p129", model.EntityTypeWork},
		{"probable label anchor", "vndb", "p999", model.EntityTypeLabel},
		{"unknown source", "nosuchsource", "p129", model.EntityTypeLabel},
	} {
		if _, found, err := svc.LookupTyped(ctx, c.source, c.ext, c.entityType, false); err != nil || found {
			t.Fatalf("%s must miss: found=%v err=%v", c.name, found, err)
		}
	}
}

// TestPublicLookupTypedNSFWParity pins that a typed lookup INHERITS the entity's
// own visibility rules instead of growing a second, drifting copy: a character
// identity is never hidden by nsfw=false (identity is not content — only its
// sexual traits drop), and the projected record is identical to what GET
// /v1/catalog/characters/{id} serves with the heavy block off.
func TestPublicLookupTypedNSFWParity(t *testing.T) {
	cleanTables(t)
	if err := testDB.Exec("TRUNCATE catalog_character_trait RESTART IDENTITY CASCADE").Error; err != nil {
		t.Fatalf("truncate trait vocab: %v", err)
	}
	svc := newPublicSvc()
	ctx := t.Context()

	ch := createCharacter(t, "隠しヒロイン")
	addExternalRef(t, model.EntityTypeCharacter, ch.ID, srcVNDB, "c777", model.LinkKindExact)
	for _, tr := range []model.CatalogCharacterTrait{
		{VndbTID: "i1", Name: "Long Hair", Sexual: false, Searchable: true, Applicable: true},
		{VndbTID: "i2", Name: "Sexual Trait", Sexual: true, Searchable: true, Applicable: true},
	} {
		trait := tr
		if err := testDB.Create(&trait).Error; err != nil {
			t.Fatalf("create trait %s: %v", trait.Name, err)
		}
		if err := testDB.Create(&model.CatalogCharacterTraitLink{
			CharacterID: ch.ID, TraitID: trait.ID, SpoilerLevel: 0,
		}).Error; err != nil {
			t.Fatalf("link trait %s: %v", trait.Name, err)
		}
	}

	sfw, found, err := svc.LookupTyped(ctx, "vndb", "c777", model.EntityTypeCharacter, false)
	if err != nil || !found || sfw.Character == nil {
		t.Fatalf("nsfw=false must still resolve the identity: found=%v err=%v", found, err)
	}
	if len(sfw.Character.Traits) != 1 || sfw.Character.Traits[0].Name != "Long Hair" {
		t.Fatalf("nsfw=false traits = %+v (want the safe one only)", sfw.Character.Traits)
	}
	direct, found, err := svc.Character(ctx, ch.ID, false, false, 0, 0, 0)
	if err != nil || !found {
		t.Fatalf("direct character: found=%v err=%v", found, err)
	}
	if !reflect.DeepEqual(*sfw.Character, direct) {
		t.Fatalf("lookup record must equal the detail projection:\nlookup=%+v\ndetail=%+v", *sfw.Character, direct)
	}

	nsfwData, found, err := svc.LookupTyped(ctx, "vndb", "c777", model.EntityTypeCharacter, true)
	if err != nil || !found || nsfwData.Character == nil {
		t.Fatalf("nsfw=true character: found=%v err=%v", found, err)
	}
	if len(nsfwData.Character.Traits) != 2 {
		t.Fatalf("nsfw=true traits = %+v (want the sexual trait to join)", nsfwData.Character.Traits)
	}
}

// TestPublicLookupBatchMixedTypes pins per-pair types: order preserved, each
// slot carries only its own family's block, and every item echoes the RESOLVED
// token (work when the pair omitted it).
func TestPublicLookupBatchMixedTypes(t *testing.T) {
	cleanTables(t)
	svc := newPublicSvc()
	ctx := t.Context()

	w := createWorkX(t, galgameMediumID, model.ContentRatingAllAges, model.WorkStatusLive, "batch 作品")
	addExternalRef(t, model.EntityTypeWork, w.ID, srcVNDB, "v1", model.LinkKindExact)
	ch := createCharacter(t, "batch 角色")
	addExternalRef(t, model.EntityTypeCharacter, ch.ID, srcBangumiPub, "12345", model.LinkKindExact)
	lbl := &model.CatalogLabel{DisplayName: "batch ブランド", Kind: model.LabelKindGameBrand}
	if err := testDB.Create(lbl).Error; err != nil {
		t.Fatalf("create label: %v", err)
	}
	addExternalRef(t, model.EntityTypeLabel, lbl.ID, srcVNDB, "p129", model.LinkKindExact)

	items, err := svc.LookupBatch(ctx, []dto.PublicLookupPair{
		{Source: "vndb", ExternalID: "v1"},                          // omitted type → work
		{Source: "bangumi", ExternalID: "12345", Type: "character"}, //
		{Source: "vndb", ExternalID: "p129", Type: "label"},         //
		{Source: "vndb", ExternalID: "p129", Type: "work"},          // wrong family → miss
		{Source: "vndb", ExternalID: "v1", Type: "name"},            // wrong family → miss
	}, false)
	if err != nil {
		t.Fatalf("batch: %v", err)
	}
	if len(items) != 5 {
		t.Fatalf("batch len=%d want 5", len(items))
	}
	wantTypes := []string{"work", "character", "label", "work", "name"}
	for i, want := range wantTypes {
		if items[i].Type != want {
			t.Fatalf("item%d type=%q want %q", i, items[i].Type, want)
		}
	}
	if items[0].Work == nil || items[0].Work.ID != w.ID || items[0].Character != nil {
		t.Fatalf("item0 (work) = %+v", items[0])
	}
	if items[1].Character == nil || items[1].Character.ID != ch.ID || items[1].Work != nil {
		t.Fatalf("item1 (character) = %+v", items[1])
	}
	if items[2].Label == nil || items[2].Label.ID != lbl.ID || items[2].Work != nil {
		t.Fatalf("item2 (label) = %+v", items[2])
	}
	for _, i := range []int{3, 4} {
		it := items[i]
		if it.Work != nil || it.ClaimedBy != nil || it.Name != nil || it.Character != nil || it.Label != nil {
			t.Fatalf("item%d must be an all-null miss: %+v", i, it)
		}
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
		_, found, err := svc.WorkDetail(ctx, c.id, PublicInclude{}, false, 0)
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

	rec, found, err := svc.WorkDetail(ctx, w.ID, PublicInclude{Relations: true}, false, 0)
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

	rec, found, err := svc.WorkDetail(ctx, w.ID, PublicInclude{Credits: true}, false, 0)
	if err != nil || !found {
		t.Fatalf("detail: found=%v err=%v", found, err)
	}
	if len(rec.Credits) != 1 || len(rec.Credits[0].Credits) != 1 || rec.Credits[0].Credits[0].ID != name.ID {
		t.Fatalf("credits projection: %+v", rec.Credits)
	}
	// The bare record (no include) omits credits entirely.
	bare, _, _ := svc.WorkDetail(ctx, w.ID, PublicInclude{}, false, 0)
	if bare.Credits != nil {
		t.Fatalf("bare record must omit credits: %+v", bare.Credits)
	}
}

// publicPhotoHash is the person photograph the public name face publishes
// (wave 172) — the same content-hash currency as a work cover's image_hash.
const publicPhotoHash = "0f1e2d3c4b5a69788796a5b4c3d2e1f00f1e2d3c4b5a69788796a5b4c3d2e1f0"

func TestPublicNameHiddenLinkAndR18Drop(t *testing.T) {
	cleanTables(t)
	svc := newPublicSvc()
	ctx := t.Context()

	p := createPerson(t, "Key")
	// The wave-172 person block, so the hidden-link half below is a real
	// withholding rather than an empty row proving nothing.
	i16 := func(v int16) *int16 { return &v }
	if err := testDB.Model(p).Updates(map[string]any{
		"photo_hash": publicPhotoHash, "gender": i16(1),
		"birth_y": i16(1975), "birth_m": i16(1), "birth_d": i16(3),
	}).Error; err != nil {
		t.Fatalf("set person block: %v", err)
	}
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
	got, found, err := svc.Name(ctx, nPublic.ID, true, false, 50, 0)
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
	// wave 172: the person block travels with the public link.
	if got.PhotoHash != publicPhotoHash {
		t.Fatalf("public link must expose photo_hash: %q", got.PhotoHash)
	}
	if got.Gender == nil || *got.Gender != 1 {
		t.Fatalf("public link must expose gender: %v", got.Gender)
	}
	if got.BirthY == nil || *got.BirthY != 1975 || got.BirthM == nil || got.BirthD == nil {
		t.Fatalf("public link must expose the fuzzy birth date: %v/%v/%v", got.BirthY, got.BirthM, got.BirthD)
	}

	// Hidden-linked name: appears as an independent identity (no person_id).
	h, found, err := svc.Name(ctx, nHidden.ID, false, false, 50, 0)
	if err != nil || !found {
		t.Fatalf("hidden person: found=%v err=%v", found, err)
	}
	if h.PersonID != 0 {
		t.Fatalf("hidden link must withhold person_id: %d", h.PersonID)
	}
	// …and the whole person block with it: the person genuinely has a photo, a
	// gender and a birthday, and publishing any of them under a hidden link
	// would leak the association the doctrine is withholding.
	if h.PhotoHash != "" || h.Gender != nil || h.BirthY != nil || h.BirthM != nil || h.BirthD != nil {
		t.Fatalf("hidden link must withhold the person block: %q %v %v/%v/%v",
			h.PhotoHash, h.Gender, h.BirthY, h.BirthM, h.BirthD)
	}
}

// TestPublicLabelIntrosLinks covers the E2c read-face addition to GET
// /v1/catalog/labels/{id}: intros[] (per-language merge, lowest source_id wins —
// step 65) and links[] (entity_type=3 link_kind=related refs → templated URLs),
// including the two hard invariants: identity anchors never leak into links, and
// a supply-less label serializes [] (never null).
func TestPublicLabelIntrosLinks(t *testing.T) {
	cleanTables(t)
	// catalog_label_intro is not in cleanTables' list; truncate it explicitly
	// (like the character-intro test) for a deterministic fixture.
	if err := testDB.Exec("TRUNCATE catalog_label_intro RESTART IDENTITY CASCADE").Error; err != nil {
		t.Fatalf("truncate label intro: %v", err)
	}
	svc := newPublicSvc()
	ctx := t.Context()

	// Seeded catalog_source ids for the E2b link sources + bangumi intro source.
	const (
		srcBangumi      int16 = 3
		srcOfficialSite int16 = 9
		srcTwitter      int16 = 10
		srcCien         int16 = 14
	)

	lbl := &model.CatalogLabel{DisplayName: "ALcot", Kind: model.LabelKindPublisher}
	if err := testDB.Create(lbl).Error; err != nil {
		t.Fatalf("create label: %v", err)
	}

	// Intros: en (vndb) + ja from two sources — bangumi (3) beats dlsite (4) for
	// the ja language after the per-language merge.
	for _, in := range []model.CatalogLabelIntro{
		{LabelID: lbl.ID, Lang: "en", Intro: "English intro.", SourceID: srcVNDB},
		{LabelID: lbl.ID, Lang: "ja", Intro: "勝つ紹介。", SourceID: srcBangumi},
		{LabelID: lbl.ID, Lang: "ja", Intro: "負ける紹介。", SourceID: srcDlsite}, // higher source → merged away
	} {
		if err := testDB.Create(&in).Error; err != nil {
			t.Fatalf("create label intro: %v", err)
		}
	}

	// Links: three related sources, one row each → three links, sorted by
	// (source_id, external_id): official_site(9) < twitter(10) < cien(14).
	addExternalRef(t, model.EntityTypeLabel, lbl.ID, srcOfficialSite, "www.alcot.biz", model.LinkKindRelated)
	addExternalRef(t, model.EntityTypeLabel, lbl.ID, srcTwitter, "alcot_official", model.LinkKindRelated)
	addExternalRef(t, model.EntityTypeLabel, lbl.ID, srcCien, "29601", model.LinkKindRelated)
	// Identity anchors (exact + probable) MUST NOT surface as links.
	addExternalRef(t, model.EntityTypeLabel, lbl.ID, srcVNDB, "p129", model.LinkKindExact)
	addExternalRef(t, model.EntityTypeLabel, lbl.ID, srcDlsite, "VG02192", model.LinkKindProbable)

	got, found, err := svc.Label(ctx, lbl.ID, false, false, 50, 0)
	if err != nil || !found {
		t.Fatalf("label: found=%v err=%v", found, err)
	}

	// intros: one element per language after the source merge, lang ASC.
	if len(got.Intros) != 2 {
		t.Fatalf("intros len=%d want 2: %+v", len(got.Intros), got.Intros)
	}
	if got.Intros[0] != (dto.PublicLabelIntro{Lang: "en", Intro: "English intro.", Source: "vndb"}) {
		t.Fatalf("intros[0]=%+v", got.Intros[0])
	}
	if got.Intros[1] != (dto.PublicLabelIntro{Lang: "ja", Intro: "勝つ紹介。", Source: "bangumi"}) {
		t.Fatalf("intros[1] (lowest source_id must win the language)=%+v", got.Intros[1])
	}

	// links: three, URL templates asserted byte-for-byte, deterministic order.
	wantLinks := []dto.PublicLabelLink{
		{Source: "official_site", URL: "https://www.alcot.biz"},
		{Source: "twitter", URL: "https://x.com/alcot_official"},
		{Source: "cien", URL: "https://ci-en.dlsite.com/creator/29601"},
	}
	if len(got.Links) != len(wantLinks) {
		t.Fatalf("links len=%d want %d: %+v", len(got.Links), len(wantLinks), got.Links)
	}
	for i, w := range wantLinks {
		if got.Links[i] != w {
			t.Fatalf("links[%d]=%+v want %+v", i, got.Links[i], w)
		}
	}
	// The exact/probable identity anchors (vndb / dlsite) must be absent.
	for _, lk := range got.Links {
		if lk.Source == "vndb" || lk.Source == "dlsite" {
			t.Fatalf("identity anchor leaked into links: %+v", lk)
		}
	}

	// A label with no intro / link rows serializes [] (never null).
	bare := &model.CatalogLabel{DisplayName: "Bare", Kind: model.LabelKindGameBrand}
	if err := testDB.Create(bare).Error; err != nil {
		t.Fatalf("create bare label: %v", err)
	}
	b, found, err := svc.Label(ctx, bare.ID, false, false, 50, 0)
	if err != nil || !found {
		t.Fatalf("bare label: found=%v err=%v", found, err)
	}
	if b.Intros == nil || len(b.Intros) != 0 {
		t.Fatalf("intros must be [] non-null: %+v", b.Intros)
	}
	if b.Links == nil || len(b.Links) != 0 {
		t.Fatalf("links must be [] non-null: %+v", b.Links)
	}
}

// TestPublicNSFWGate pins the wave-104 caller-controlled r18 switch: an r18
// work 404s by default (Phase-1 bit-identical) but is served in full — facets,
// relations, lookup — with nsfw=1; r18 relation ends follow the same switch.
func TestPublicNSFWGate(t *testing.T) {
	cleanTables(t)
	svc := newPublicSvc()
	ctx := t.Context()

	r18 := createWorkX(t, galgameMediumID, model.ContentRatingR18, model.WorkStatusLive, "R18作品")
	safe := createWorkX(t, galgameMediumID, model.ContentRatingAllAges, model.WorkStatusLive, "全年齢作品")
	createWorkRelation(t, r18.ID, safe.ID)
	addExternalRef(t, model.EntityTypeWork, r18.ID, srcVNDB, "v104", model.LinkKindExact)
	if err := testDB.Create(&model.CatalogWorkPopularity{WorkID: r18.ID, SourceID: 3, Metric: model.PopularityMetricBgmWish, Value: 42}).Error; err != nil {
		t.Fatalf("popularity fixture: %v", err)
	}
	if err := testDB.Create(&model.CatalogWorkTag{WorkID: r18.ID, Name: "泣きゲー", Count: 7, SourceID: 3}).Error; err != nil {
		t.Fatalf("tag fixture: %v", err)
	}

	// Default: hidden — detail 404, lookup miss (Phase-1 bit-identical).
	if _, found, err := svc.WorkDetail(ctx, r18.ID, PublicInclude{}, false, 0); err != nil || found {
		t.Fatalf("default r18 detail: found=%v err=%v (want hidden)", found, err)
	}
	if _, found, _ := svc.Lookup(ctx, "vndb", "v104", false); found {
		t.Fatal("default r18 lookup resolved (want miss)")
	}

	// nsfw=1: served in full, facets projected to public conventions.
	rec, found, err := svc.WorkDetail(ctx, r18.ID, PublicInclude{Relations: true}, true, 0)
	if err != nil || !found {
		t.Fatalf("nsfw r18 detail: found=%v err=%v", found, err)
	}
	if rec.ContentRating != "r18" {
		t.Fatalf("content_rating = %q, want r18", rec.ContentRating)
	}
	if len(rec.Popularity) != 1 || rec.Popularity[0].Metric != "bgm_wish" || rec.Popularity[0].Value != 42 || rec.Popularity[0].Source != "bangumi" {
		t.Fatalf("popularity facet = %+v", rec.Popularity)
	}
	if len(rec.Tags) != 1 || rec.Tags[0].Name != "泣きゲー" || rec.Tags[0].Source != "bangumi" {
		t.Fatalf("tags facet = %+v", rec.Tags)
	}
	if len(rec.Relations) != 1 || rec.Relations[0].Work.ID != safe.ID {
		t.Fatalf("relations = %+v", rec.Relations)
	}
	if _, found, _ = svc.Lookup(ctx, "vndb", "v104", true); !found {
		t.Fatal("nsfw r18 lookup missed (want hit)")
	}

	// The safe work's relations: the r18 end drops by default, joins with nsfw.
	recSafe, _, err := svc.WorkDetail(ctx, safe.ID, PublicInclude{Relations: true}, false, 0)
	if err != nil {
		t.Fatalf("safe detail: %v", err)
	}
	if len(recSafe.Relations) != 0 {
		t.Fatalf("safe relations nsfw-off = %+v (want r18 end dropped)", recSafe.Relations)
	}
	recSafe, _, _ = svc.WorkDetail(ctx, safe.ID, PublicInclude{Relations: true}, true, 0)
	if len(recSafe.Relations) != 1 || recSafe.Relations[0].Work.ID != r18.ID {
		t.Fatalf("safe relations nsfw-on = %+v", recSafe.Relations)
	}
}

// TestPublicCharacterTraits pins the wave-104 public trait block: safe default
// (spoilers=0, sexual dropped), the spoilers ceiling, and the nsfw switch for
// sexual-family traits.
func TestPublicCharacterTraits(t *testing.T) {
	cleanTables(t)
	if err := testDB.Exec("TRUNCATE catalog_character_trait RESTART IDENTITY CASCADE").Error; err != nil {
		t.Fatalf("truncate trait vocab: %v", err)
	}
	svc := newPublicSvc()
	ctx := t.Context()

	ch := &model.CatalogCharacter{DisplayName: "テスト嬢", Lang: "ja"}
	if err := testDB.Create(ch).Error; err != nil {
		t.Fatalf("create character: %v", err)
	}
	mkTrait := func(tid, name string, sexual bool) int64 {
		tr := &model.CatalogCharacterTrait{VndbTID: tid, Name: name, Sexual: sexual, Searchable: true, Applicable: true}
		if err := testDB.Create(tr).Error; err != nil {
			t.Fatalf("create trait %s: %v", name, err)
		}
		return tr.ID
	}
	tSafe := mkTrait("i1", "Long Hair", false)
	tSexual := mkTrait("i2", "Sexual Trait", true)
	tSpoiler := mkTrait("i3", "Hidden Past", false)
	link := func(traitID int64, spoiler int16) {
		if err := testDB.Create(&model.CatalogCharacterTraitLink{CharacterID: ch.ID, TraitID: traitID, SpoilerLevel: spoiler}).Error; err != nil {
			t.Fatalf("link trait: %v", err)
		}
	}
	link(tSafe, 0)
	link(tSexual, 0)
	link(tSpoiler, 2)

	rec, found, err := svc.Character(ctx, ch.ID, false, false, 0, 50, 0)
	if err != nil || !found {
		t.Fatalf("character: found=%v err=%v", found, err)
	}
	if len(rec.Traits) != 1 || rec.Traits[0].Name != "Long Hair" {
		t.Fatalf("default traits = %+v (want safe only)", rec.Traits)
	}
	rec, _, _ = svc.Character(ctx, ch.ID, false, true, 0, 50, 0)
	if len(rec.Traits) != 2 {
		t.Fatalf("nsfw traits = %+v (want 2: sexual joins)", rec.Traits)
	}
	rec, _, _ = svc.Character(ctx, ch.ID, false, true, 2, 50, 0)
	if len(rec.Traits) != 3 {
		t.Fatalf("nsfw+spoilers traits = %+v (want 3)", rec.Traits)
	}
	rec, _, _ = svc.Character(ctx, ch.ID, false, false, 2, 50, 0)
	if len(rec.Traits) != 2 {
		t.Fatalf("spoilers-only traits = %+v (want 2: sexual still dropped)", rec.Traits)
	}
}

// TestPublicCharacterIntrosImage pins the wave-107 additions: intros[] (one
// element per language, lowest source_id wins) and the portrait CDN URL.
func TestPublicCharacterIntrosImage(t *testing.T) {
	cleanTables(t)
	svc := NewPublicService(testDB, NewReadService(testDB), testResolve, "http://cdn.test/img")
	ctx := t.Context()

	c := &model.CatalogCharacter{DisplayName: "紹介娘", Lang: "ja"}
	if err := testDB.Create(c).Error; err != nil {
		t.Fatalf("create character: %v", err)
	}
	if err := testDB.Exec(`UPDATE catalog_character SET image_hash = ? WHERE id = ?`, "abc123hash", c.ID).Error; err != nil {
		t.Fatalf("set image: %v", err)
	}
	for _, row := range []model.CatalogCharacterIntro{
		{CharacterID: c.ID, Lang: "ja", Intro: "vndb の紹介", SourceID: 2},
		{CharacterID: c.ID, Lang: "zh-Hans", Intro: "bgm 简介", SourceID: 3},
	} {
		if err := testDB.Create(&row).Error; err != nil {
			t.Fatalf("intro fixture: %v", err)
		}
	}

	rec, found, err := svc.Character(ctx, c.ID, false, false, 0, 50, 0)
	if err != nil || !found {
		t.Fatalf("character: found=%v err=%v", found, err)
	}
	if len(rec.Intros) != 2 {
		t.Fatalf("intros = %+v (want ja + zh-Hans)", rec.Intros)
	}
	if rec.Intros[0].Lang != "ja" || rec.Intros[0].Source != "vndb" || rec.Intros[0].Intro != "vndb の紹介" {
		t.Fatalf("ja intro = %+v", rec.Intros[0])
	}
	if rec.Intros[1].Lang != "zh-Hans" || rec.Intros[1].Source != "bangumi" {
		t.Fatalf("zh intro = %+v", rec.Intros[1])
	}
	if rec.Image == "" {
		t.Fatal("image URL empty (want CDN URL from hash)")
	}
}

// TestPublicNameIntros pins the wave-108 bridge: a credit name's description
// reads from its OWN bangumi anchor at read time (per-name provenance, never a
// person assertion), kana → ja heuristic, source key not id.
func TestPublicNameIntros(t *testing.T) {
	cleanTables(t)
	if err := srcb.EnsureSchema(testDB); err != nil {
		t.Fatalf("src_bangumi schema: %v", err)
	}
	if err := testDB.Exec(`TRUNCATE src_bangumi.person RESTART IDENTITY CASCADE`).Error; err != nil {
		t.Fatalf("truncate person: %v", err)
	}
	svc := newPublicSvc()
	ctx := t.Context()

	n := &model.CatalogCreditName{Name: "テスト声優", Lang: "ja"}
	if err := testDB.Create(n).Error; err != nil {
		t.Fatalf("create name: %v", err)
	}
	addExternalRef(t, model.EntityTypeCreditName, n.ID, int16(3), "999001", model.LinkKindExact)
	if err := testDB.Exec(`INSERT INTO src_bangumi.person (id, name, type, infobox_raw, parse_error, summary, comments, collects, parser_version, ingested_at)
		VALUES (999001, 'p', 1, '', '', '日本の声優。', 0, 0, 'x', now())`).Error; err != nil {
		t.Fatalf("person fixture: %v", err)
	}

	rec, found, err := svc.Name(ctx, n.ID, false, false, 50, 0)
	if err != nil || !found {
		t.Fatalf("name: found=%v err=%v", found, err)
	}
	if len(rec.Intros) != 1 || rec.Intros[0].Lang != "ja" || rec.Intros[0].Source != "bangumi" || rec.Intros[0].Intro != "日本の声優。" {
		t.Fatalf("intros = %+v", rec.Intros)
	}
}

// TestPublicNameAliases pins the wave-175 addition to GET
// /v1/catalog/names/{id}: aliases[] carries THIS credit name's alternate
// spellings — the surface that makes the bangumi zh-Hans name lane visible —
// in the labelAliases shape (flat, deduplicated, the name itself excluded,
// always present) and never folds a sibling's spellings in.
func TestPublicNameAliases(t *testing.T) {
	cleanTables(t)
	svc := newPublicSvc()
	ctx := t.Context()

	p := createPerson(t, "緒方剛志")
	head := createCreditName(t, &p.ID, "緒方剛志")
	sibling := createCreditName(t, &p.ID, "Ogata Takeshi")

	createNameAlias(t, head.ID, "绪方刚志", "zh-Hans") // the zh rendering the wave writes
	createNameAlias(t, head.ID, "绪方刚", "zh-Hans")
	createNameAlias(t, head.ID, "绪方刚志", "")   // same spelling, other lang → renders once
	createNameAlias(t, head.ID, "緒方剛志", "ja") // the name itself → excluded
	createNameAlias(t, sibling.ID, "尾形武", "zh-Hans")
	// A search hint is findability-only by its kind's contract — never displayed.
	hint := &model.CatalogNameAlias{CreditNameID: head.ID, Name: "ogatakoji-hint",
		Kind: model.AliasKindSearchHint}
	if err := testDB.Create(hint).Error; err != nil {
		t.Fatalf("create search hint: %v", err)
	}

	rec, found, err := svc.Name(ctx, head.ID, false, false, 50, 0)
	if err != nil || !found {
		t.Fatalf("name: found=%v err=%v", found, err)
	}
	// Ordered by (name, id) exactly like labelAliases.
	if len(rec.Aliases) != 2 || rec.Aliases[0] != "绪方刚" || rec.Aliases[1] != "绪方刚志" {
		t.Fatalf("aliases = %+v (want the two zh spellings, deduped, display name excluded)", rec.Aliases)
	}
	for _, a := range rec.Aliases {
		if a == "尾形武" {
			t.Fatal("a sibling's alias must never be attributed to this name")
		}
	}
	// The sibling's own record answers for the sibling's own spellings.
	sib, found, err := svc.Name(ctx, sibling.ID, false, false, 50, 0)
	if err != nil || !found {
		t.Fatalf("sibling: found=%v err=%v", found, err)
	}
	if len(sib.Aliases) != 1 || sib.Aliases[0] != "尾形武" {
		t.Fatalf("sibling aliases = %+v", sib.Aliases)
	}

	// A name with no aliases serializes [], never null.
	bare := createCreditName(t, nil, "無別名")
	rec, _, err = svc.Name(ctx, bare.ID, false, false, 50, 0)
	if err != nil {
		t.Fatalf("bare name: %v", err)
	}
	if rec.Aliases == nil || len(rec.Aliases) != 0 {
		t.Fatalf("an alias-less name must serialize []: %#v", rec.Aliases)
	}
}

// createSameSeriesEdge wires a same_series (type 7) edge — the vndb series grain.
func createSameSeriesEdge(t *testing.T, a, b int64) {
	t.Helper()
	if err := testDB.Create(&model.CatalogWorkRelation{AWorkID: a, BWorkID: b, RelationTypeID: 7}).Error; err != nil {
		t.Fatalf("create same_series edge: %v", err)
	}
}

// TestSeriesSiblingsTransitiveClosure pins wave 113: a star-topology series
// (hub + leaves, the shape 68.6% of vndb series nodes live in) resolves the
// WHOLE family from any member — a leaf sees the hub AND the other leaves,
// which the pairwise relations face alone never shows it.
func TestSeriesSiblingsTransitiveClosure(t *testing.T) {
	cleanTables(t)
	ctx := t.Context()
	svc := newPublicSvc()

	hub := createWork(t, "シリーズ中枢")
	l1 := createWork(t, "シリーズ枝1")
	l2 := createWork(t, "シリーズ枝2")
	l3 := createWork(t, "シリーズ枝3")
	lone := createWork(t, "無関係作品")
	// Star: hub—l1, hub—l2, hub—l3 (leaves connect ONLY to the hub).
	createSameSeriesEdge(t, hub.ID, l1.ID)
	createSameSeriesEdge(t, hub.ID, l2.ID)
	createSameSeriesEdge(t, hub.ID, l3.ID)

	ids := func(bs []dto.PublicWorkBrief) map[int64]bool {
		m := map[int64]bool{}
		for _, b := range bs {
			m[b.ID] = true
		}
		return m
	}

	// A leaf sees the hub AND the two other leaves (transitive closure).
	rec, found, err := svc.WorkDetail(ctx, l1.ID, PublicInclude{}, false, 0)
	if err != nil || !found {
		t.Fatalf("leaf detail: found=%v err=%v", found, err)
	}
	got := ids(rec.SeriesSiblings)
	if len(got) != 3 || !got[hub.ID] || !got[l2.ID] || !got[l3.ID] || got[l1.ID] {
		t.Fatalf("leaf l1 siblings = %v (want hub,l2,l3; not self)", got)
	}

	// The hub sees all three leaves.
	recH, _, err := svc.WorkDetail(ctx, hub.ID, PublicInclude{}, false, 0)
	if err != nil {
		t.Fatalf("hub detail: %v", err)
	}
	if gh := ids(recH.SeriesSiblings); len(gh) != 3 || !gh[l1.ID] || !gh[l2.ID] || !gh[l3.ID] {
		t.Fatalf("hub siblings = %v (want l1,l2,l3)", gh)
	}

	// A work with no series edge has an empty (non-nil) list.
	recL, _, err := svc.WorkDetail(ctx, lone.ID, PublicInclude{}, false, 0)
	if err != nil {
		t.Fatalf("lone detail: %v", err)
	}
	if recL.SeriesSiblings == nil || len(recL.SeriesSiblings) != 0 {
		t.Fatalf("lone siblings = %v (want empty non-nil)", recL.SeriesSiblings)
	}
}

// siblingIDs runs the closure walk straight against the read service (the layer
// wave 117 rewrote) and returns the sibling ids in the returned order.
func siblingIDs(t *testing.T, workID int64) []int64 {
	t.Helper()
	rows, err := NewReadService(testDB).loadSeriesSiblings(t.Context(), workID)
	if err != nil {
		t.Fatalf("loadSeriesSiblings(%d): %v", workID, err)
	}
	out := make([]int64, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.WorkID)
	}
	return out
}

// TestSeriesSiblingsClosureShapes pins the wave-117 two-stage rewrite against
// the topologies the single-statement form used to absorb implicitly: a long
// chain (the walk must not stop at depth 1), a cycle (UNION dedup is what
// terminates it), no edges at all (the stage-2 short circuit), and a
// soft-deleted node (drops from the result but still bridges the component).
func TestSeriesSiblingsClosureShapes(t *testing.T) {
	cleanTables(t)

	t.Run("multi-hop chain", func(t *testing.T) {
		cleanTables(t)
		// a—b—c—d—e, each work linked only to its neighbour.
		w := make([]*model.CatalogWork, 5)
		for i := range w {
			w[i] = createWork(t, "鎖"+string(rune('A'+i)))
		}
		for i := 0; i < len(w)-1; i++ {
			createSameSeriesEdge(t, w[i].ID, w[i+1].ID)
		}
		// The far end sees all four others, four hops away included.
		got := siblingIDs(t, w[4].ID)
		want := []int64{w[0].ID, w[1].ID, w[2].ID, w[3].ID}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("tail siblings = %v (want %v, ascending by id)", got, want)
		}
		// A middle node reaches both directions.
		if got := siblingIDs(t, w[2].ID); len(got) != 4 {
			t.Fatalf("middle siblings = %v (want 4)", got)
		}
	})

	t.Run("cycle terminates", func(t *testing.T) {
		cleanTables(t)
		a := createWork(t, "環A")
		b := createWork(t, "環B")
		c := createWork(t, "環C")
		createSameSeriesEdge(t, a.ID, b.ID)
		createSameSeriesEdge(t, b.ID, c.ID)
		createSameSeriesEdge(t, c.ID, a.ID) // closes the loop
		// A duplicate edge in the reverse direction must not duplicate rows.
		createSameSeriesEdge(t, b.ID, a.ID)
		got := siblingIDs(t, a.ID)
		if want := []int64{b.ID, c.ID}; !reflect.DeepEqual(got, want) {
			t.Fatalf("cycle siblings = %v (want %v, no dups, no self)", got, want)
		}
	})

	t.Run("no edges", func(t *testing.T) {
		cleanTables(t)
		lone := createWork(t, "孤立作品")
		createWork(t, "無関係")
		if got := siblingIDs(t, lone.ID); len(got) != 0 {
			t.Fatalf("lone siblings = %v (want none)", got)
		}
		// A work id that does not exist at all walks to nothing too.
		if got := siblingIDs(t, lone.ID+9999); len(got) != 0 {
			t.Fatalf("missing work siblings = %v (want none)", got)
		}
	})

	t.Run("soft-deleted node bridges but drops", func(t *testing.T) {
		cleanTables(t)
		a := createWork(t, "橋A")
		mid := createWork(t, "橋M")
		c := createWork(t, "橋C")
		createSameSeriesEdge(t, a.ID, mid.ID)
		createSameSeriesEdge(t, mid.ID, c.ID)
		if err := testDB.Delete(&model.CatalogWork{}, mid.ID).Error; err != nil {
			t.Fatalf("soft delete mid: %v", err)
		}
		// mid is gone from the result, yet a still reaches c through it —
		// the walk is over edges, the liveness filter is on the briefs.
		if got := siblingIDs(t, a.ID); !reflect.DeepEqual(got, []int64{c.ID}) {
			t.Fatalf("siblings across deleted node = %v (want [%d])", got, c.ID)
		}
		// Walking FROM the deleted node still sees its live neighbours: the
		// caller's own liveness is the read face's business, not this walk's.
		if got := siblingIDs(t, mid.ID); !reflect.DeepEqual(got, []int64{a.ID, c.ID}) {
			t.Fatalf("deleted node's own siblings = %v (want [%d %d])", got, a.ID, c.ID)
		}
	})
}
