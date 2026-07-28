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
	detail, found, err := svc.WorkDetail(ctx, w.ID, PublicInclude{}, false)
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
		_, found, err := svc.WorkDetail(ctx, c.id, PublicInclude{}, false)
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

	rec, found, err := svc.WorkDetail(ctx, w.ID, PublicInclude{Relations: true}, false)
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

	rec, found, err := svc.WorkDetail(ctx, w.ID, PublicInclude{Credits: true}, false)
	if err != nil || !found {
		t.Fatalf("detail: found=%v err=%v", found, err)
	}
	if len(rec.Credits) != 1 || len(rec.Credits[0].Credits) != 1 || rec.Credits[0].Credits[0].ID != name.ID {
		t.Fatalf("credits projection: %+v", rec.Credits)
	}
	// The bare record (no include) omits credits entirely.
	bare, _, _ := svc.WorkDetail(ctx, w.ID, PublicInclude{}, false)
	if bare.Credits != nil {
		t.Fatalf("bare record must omit credits: %+v", bare.Credits)
	}
}

func TestPublicNameHiddenLinkAndR18Drop(t *testing.T) {
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

	// Hidden-linked name: appears as an independent identity (no person_id).
	h, found, err := svc.Name(ctx, nHidden.ID, false, false, 50, 0)
	if err != nil || !found {
		t.Fatalf("hidden person: found=%v err=%v", found, err)
	}
	if h.PersonID != 0 {
		t.Fatalf("hidden link must withhold person_id: %d", h.PersonID)
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
	if _, found, err := svc.WorkDetail(ctx, r18.ID, PublicInclude{}, false); err != nil || found {
		t.Fatalf("default r18 detail: found=%v err=%v (want hidden)", found, err)
	}
	if _, found, _ := svc.Lookup(ctx, "vndb", "v104", false); found {
		t.Fatal("default r18 lookup resolved (want miss)")
	}

	// nsfw=1: served in full, facets projected to public conventions.
	rec, found, err := svc.WorkDetail(ctx, r18.ID, PublicInclude{Relations: true}, true)
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
	recSafe, _, err := svc.WorkDetail(ctx, safe.ID, PublicInclude{Relations: true}, false)
	if err != nil {
		t.Fatalf("safe detail: %v", err)
	}
	if len(recSafe.Relations) != 0 {
		t.Fatalf("safe relations nsfw-off = %+v (want r18 end dropped)", recSafe.Relations)
	}
	recSafe, _, _ = svc.WorkDetail(ctx, safe.ID, PublicInclude{Relations: true}, true)
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
