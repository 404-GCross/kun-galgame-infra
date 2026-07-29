// public_works_include_test.go — A2-1a: the works LIST include= rich-brief
// blocks, and the hard gate that the DEFAULT (no include=) response is
// byte-identical to the frozen W1 contract. Integration against
// kun_catalog_test (service_test.go TestMain).
package service

import (
	"context"
	"encoding/json"
	"testing"

	"api/internal/platform/catalog/model"
)

const testCDNBase = "https://cdn.example.test/img"

// newPublicSvcCDN builds the public service with a real CDN base so cover URLs
// (and therefore the cover blocks) are non-empty.
func newPublicSvcCDN() *PublicService {
	return NewPublicService(testDB, NewReadService(testDB), testResolve, testCDNBase)
}

// hash64 pads a short seed to the 64-char shape image hashes have (the URL
// builder needs at least 4 chars; the padding keeps the fixtures readable).
func hash64(seed string) string {
	out := seed
	for len(out) < 64 {
		out += "0"
	}
	return out[:64]
}

func addWorkTitle(t *testing.T, workID int64, lang, title string, kind int16) {
	t.Helper()
	if err := testDB.Create(&model.CatalogWorkTitle{WorkID: workID, Lang: lang, Title: title, Kind: kind}).Error; err != nil {
		t.Fatalf("create work title %s/%s: %v", lang, title, err)
	}
}

func addWorkIntro(t *testing.T, workID int64, lang, intro string, sourceID, provenance int16) {
	t.Helper()
	if err := testDB.Create(&model.CatalogWorkIntro{
		WorkID: workID, Lang: lang, Intro: intro, SourceID: sourceID, Provenance: provenance,
	}).Error; err != nil {
		t.Fatalf("create work intro %s: %v", lang, err)
	}
}

func addWorkCover(t *testing.T, workID int64, hash string, sortOrder int, kind string, pinned bool, sexual int16, sourceID int16) {
	t.Helper()
	if err := testDB.Create(&model.CatalogWorkCover{
		WorkID: workID, ImageHash: hash, SortOrder: sortOrder, Kind: kind,
		PortraitPinned: pinned, Sexual: sexual, SourceID: sourceID,
	}).Error; err != nil {
		t.Fatalf("create work cover %s: %v", hash, err)
	}
}

func addWorkRating(t *testing.T, workID int64, sourceID int16, score float64, votes int) {
	t.Helper()
	if err := testDB.Create(&model.CatalogWorkRating{
		WorkID: workID, SourceID: sourceID, Score: score, VoteCount: votes,
	}).Error; err != nil {
		t.Fatalf("create work rating: %v", err)
	}
}

func addWorkLabel(t *testing.T, workID int64, displayName string, labelKind, edgeKind int16) int64 {
	t.Helper()
	l := &model.CatalogLabel{DisplayName: displayName, Kind: labelKind}
	if err := testDB.Create(l).Error; err != nil {
		t.Fatalf("create label: %v", err)
	}
	if err := testDB.Create(&model.CatalogWorkLabel{WorkID: workID, LabelID: l.ID, Kind: edgeKind}).Error; err != nil {
		t.Fatalf("create work label edge: %v", err)
	}
	return l.ID
}

// stubMeta wires a fixed hash → ImageMeta table as the image_service lookup.
func stubMeta(table map[string]ImageMeta) ImageMetaFunc {
	return func(_ context.Context, hashes []string) (map[string]ImageMeta, error) {
		out := make(map[string]ImageMeta, len(hashes))
		for _, h := range hashes {
			if m, ok := table[h]; ok {
				out[h] = m
			}
		}
		return out, nil
	}
}

// seedRichWork creates one bodyless galgame work carrying content for EVERY
// include= block, so a leak into the default response cannot hide behind an
// empty facet.
func seedRichWork(t *testing.T) int64 {
	t.Helper()
	w := createWorkX(t, galgameMediumID, model.ContentRatingAllAges, model.WorkStatusLive, "Rich Brief")
	addWorkTitle(t, w.ID, "ja", "リッチ", model.WorkTitleKindOfficial)
	addWorkTitle(t, w.ID, "zh-Hans", "富简介", model.WorkTitleKindOfficial)
	addWorkTitle(t, w.ID, "zh-Hant", "富簡介", model.WorkTitleKindOfficial)
	addWorkTitle(t, w.ID, "en", "Rich Brief", model.WorkTitleKindOfficial)
	addWorkIntro(t, w.ID, "ja", "日本語の紹介", srcVNDB, 0)
	addWorkCover(t, w.ID, hash64("aa11"), 0, "main", false, 0, srcVNDB)
	addWorkRating(t, w.ID, srcVNDB, 8.5, 1200)
	addWorkLabel(t, w.ID, "Test Brand", model.LabelKindGameBrand, model.WorkLabelKindBrand)
	createRelease(t, w.ID, 2021, 6, 4)
	return w.ID
}

// TestWorksListDefaultResponseIsByteIdentical is THE hard gate of the A2-1a
// wave: with no include= the serialized page must be exactly the frozen W1
// contract — same keys, same order, nothing added. The work carries content
// for every new block, so any block that forgot its omitempty (or any loader
// that ran unasked) shows up as a byte difference here.
func TestWorksListDefaultResponseIsByteIdentical(t *testing.T) {
	cleanTables(t)
	svc := newPublicSvcCDN()
	// Enrichment wired ON: it must not leak into the default response either.
	svc.WithImageMeta(stubMeta(map[string]ImageMeta{
		hash64("aa11"): {Width: 800, Height: 1200, Thumbhash: "TH-AA"},
	}))

	id := seedRichWork(t)
	// Freeze updated_at so the golden is stable.
	if err := testDB.Exec(`UPDATE catalog_work SET updated_at = ? WHERE id = ?`, "2026-01-02T03:04:05Z", id).Error; err != nil {
		t.Fatalf("stamp updated_at: %v", err)
	}

	page, err := svc.WorksList(t.Context(), WorksListFilter{Sort: "id"}, "", 50)
	if err != nil {
		t.Fatalf("WorksList default: %v", err)
	}
	got, err := json.Marshal(page)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	// olang carries the real row value since the o_lang column-tag fix (the
	// list scan struct previously never bound the selected `olang` column and
	// the field shipped as "" from W1 — a value correction, not a shape change).
	want := `{"items":[{"id":` + itoa(id) + `,"medium":"galgame","display_name":"Rich Brief",` +
		`"content_rating":"all_ages","olang":"ja","release_date":"2021-06-04","claimed_by":null,` +
		`"cover":"` + testCDNBase + `/aa/11/` + hash64("aa11") + `.webp",` +
		`"updated":"2026-01-02T03:04:05Z"}],"next_cursor":null}`
	if string(got) != want {
		t.Fatalf("default works-list response drifted from the frozen contract\n got: %s\nwant: %s", got, want)
	}
}

// TestWorksListIncludeNamesAndIntros pins the D7 four-key pivot: catalog
// BCP-47 tags land on ja-jp / zh-cn / zh-tw / en-us, a language outside the
// four is dropped, search_hint titles never surface, and a machine-translated
// intro carries its flag.
func TestWorksListIncludeNamesAndIntros(t *testing.T) {
	cleanTables(t)
	svc := newPublicSvcCDN()

	w := createWorkX(t, galgameMediumID, model.ContentRatingAllAges, model.WorkStatusLive, "Pivot")
	addWorkTitle(t, w.ID, "ja", "ぴぼっと", model.WorkTitleKindOfficial)
	// An alias in the same language must lose to the official row (lowest kind).
	addWorkTitle(t, w.ID, "ja", "ピボット別名", model.WorkTitleKindAlias)
	// search_hint must NEVER surface, even when it is the only row for a key.
	addWorkTitle(t, w.ID, "en", "pivot-searchhint", model.WorkTitleKindSearchHint)
	addWorkTitle(t, w.ID, "zh-Hans", "枢轴", model.WorkTitleKindOfficial)
	addWorkTitle(t, w.ID, "zh-Hant", "樞軸", model.WorkTitleKindOfficial)
	// Outside the four product keys → dropped.
	addWorkTitle(t, w.ID, "ko", "피벗", model.WorkTitleKindOfficial)

	// ja has a source row; zh-Hans has ONLY a machine row (so it surfaces with
	// machine=true); ko is dropped by the pivot.
	addWorkIntro(t, w.ID, "ja", "原文", srcVNDB, 0)
	addWorkIntro(t, w.ID, "zh-Hans", "机翻", srcVNDB, 1)
	addWorkIntro(t, w.ID, "ko", "한국어", srcVNDB, 0)

	page, err := svc.WorksList(t.Context(),
		WorksListFilter{Sort: "id", Include: ParseWorksListInclude("names,intros,nonsense")}, "", 50)
	if err != nil {
		t.Fatalf("WorksList include: %v", err)
	}
	if len(page.Items) != 1 {
		t.Fatalf("page = %d items, want 1", len(page.Items))
	}
	it := page.Items[0]

	if it.Names == nil {
		t.Fatal("names block missing")
	}
	if it.Names.JaJP != "ぴぼっと" {
		t.Fatalf("names.ja-jp = %q, want the official title (alias must lose)", it.Names.JaJP)
	}
	if it.Names.ZhCN != "枢轴" || it.Names.ZhTW != "樞軸" {
		t.Fatalf("names zh pivot = %q / %q, want 枢轴 / 樞軸", it.Names.ZhCN, it.Names.ZhTW)
	}
	if it.Names.EnUS != "" {
		t.Fatalf("names.en-us = %q, want empty (its only row is a search_hint)", it.Names.EnUS)
	}

	if it.Intros == nil || it.Intros.JaJP == nil || it.Intros.ZhCN == nil {
		t.Fatalf("intros block = %+v, want ja-jp + zh-cn filled", it.Intros)
	}
	if it.Intros.JaJP.Intro != "原文" || it.Intros.JaJP.Machine {
		t.Fatalf("intros.ja-jp = %+v, want the source row unflagged", it.Intros.JaJP)
	}
	if it.Intros.ZhCN.Intro != "机翻" || !it.Intros.ZhCN.Machine {
		t.Fatalf("intros.zh-cn = %+v, want the machine row flagged", it.Intros.ZhCN)
	}
	if it.Intros.ZhCN.Source != "vndb" {
		t.Fatalf("intros.zh-cn source = %q, want vndb", it.Intros.ZhCN.Source)
	}
	if it.Intros.EnUS != nil || it.Intros.ZhTW != nil {
		t.Fatalf("intros = %+v, want en-us / zh-tw absent", it.Intros)
	}
	// Blocks not asked for stay absent.
	if it.Labels != nil || it.Ratings != nil || it.Covers != nil {
		t.Fatalf("unrequested blocks leaked: labels=%v ratings=%v covers=%v", it.Labels, it.Ratings, it.Covers)
	}
}

// TestWorksListIncludeLabelsAndRatings pins the two flat blocks against the
// detail face's shapes (same projection, source-native rating scales).
func TestWorksListIncludeLabelsAndRatings(t *testing.T) {
	cleanTables(t)
	svc := newPublicSvcCDN()

	w := createWorkX(t, galgameMediumID, model.ContentRatingAllAges, model.WorkStatusLive, "Flat")
	labelID := addWorkLabel(t, w.ID, "Brand X", model.LabelKindGameBrand, model.WorkLabelKindBrand)
	addWorkRating(t, w.ID, srcVNDB, 8.4, 900)
	addWorkRating(t, w.ID, srcErogamespace, 78, 51)
	// A second work with nothing attached: its blocks stay absent, never [].
	bare := createWorkX(t, galgameMediumID, model.ContentRatingAllAges, model.WorkStatusLive, "Bare")

	page, err := svc.WorksList(t.Context(),
		WorksListFilter{Sort: "id", Include: ParseWorksListInclude("labels,ratings")}, "", 50)
	if err != nil {
		t.Fatalf("WorksList include: %v", err)
	}
	if len(page.Items) != 2 {
		t.Fatalf("page = %d items, want 2", len(page.Items))
	}
	rich, empty := page.Items[0], page.Items[1]
	if rich.ID != w.ID || empty.ID != bare.ID {
		t.Fatalf("unexpected order: %d, %d", rich.ID, empty.ID)
	}

	if len(rich.Labels) != 1 || rich.Labels[0].ID != labelID ||
		rich.Labels[0].DisplayName != "Brand X" ||
		rich.Labels[0].LabelKind != "game_brand" || rich.Labels[0].Kind != "brand" {
		t.Fatalf("labels = %+v", rich.Labels)
	}
	if len(rich.Ratings) != 2 {
		t.Fatalf("ratings = %+v, want 2 source-native rows", rich.Ratings)
	}
	if rich.Ratings[0].Source != "vndb" || rich.Ratings[0].Score != 8.4 || rich.Ratings[0].VoteCount != 900 {
		t.Fatalf("ratings[0] = %+v", rich.Ratings[0])
	}
	// erogamescape keeps its own 0-100 scale — never normalized against vndb's.
	if rich.Ratings[1].Source != "erogamescape" || rich.Ratings[1].Score != 78 {
		t.Fatalf("ratings[1] = %+v", rich.Ratings[1])
	}
	if empty.Labels != nil || empty.Ratings != nil {
		t.Fatalf("bare work must omit both blocks, got labels=%v ratings=%v", empty.Labels, empty.Ratings)
	}
}

// TestWorksListIncludeCoverSlots pins the two-slot picker: the pinned portrait
// wins its slot, the banner slot takes the landscape cover, both carry the
// image_service metadata, and an sfw caller never sees a sexual-flagged cover.
func TestWorksListIncludeCoverSlots(t *testing.T) {
	cleanTables(t)
	svc := newPublicSvcCDN()

	portraitHash, bannerHash, sexualHash := hash64("aabb"), hash64("ccdd"), hash64("eeff")
	svc.WithImageMeta(stubMeta(map[string]ImageMeta{
		portraitHash: {Width: 800, Height: 1200, Thumbhash: "TH-P"},
		bannerHash:   {Width: 1920, Height: 1080, Thumbhash: "TH-B"},
		sexualHash:   {Width: 1600, Height: 900, Thumbhash: "TH-S"},
	}))

	w := createWorkX(t, galgameMediumID, model.ContentRatingAllAges, model.WorkStatusLive, "Slots")
	// The sexual cover sorts FIRST, so an sfw caller skipping it is visible.
	addWorkCover(t, w.ID, sexualHash, 0, "main", false, 2, srcVNDB)
	addWorkCover(t, w.ID, bannerHash, 1, "main", false, 0, srcVNDB)
	addWorkCover(t, w.ID, portraitHash, 2, "main", true, 0, srcVNDB)

	inc := ParseWorksListInclude("covers")
	page, err := svc.WorksList(t.Context(), WorksListFilter{Sort: "id", Include: inc}, "", 50)
	if err != nil {
		t.Fatalf("WorksList covers: %v", err)
	}
	cov := page.Items[0].Covers
	if cov == nil || cov.Portrait == nil || cov.Banner == nil {
		t.Fatalf("covers block = %+v, want both slots filled", cov)
	}
	if cov.Portrait.URL != testCDNBase+"/aa/bb/"+portraitHash+".webp" {
		t.Fatalf("portrait url = %q", cov.Portrait.URL)
	}
	if cov.Portrait.Width != 800 || cov.Portrait.Height != 1200 || cov.Portrait.Thumbhash != "TH-P" {
		t.Fatalf("portrait meta = %+v", cov.Portrait)
	}
	if cov.Banner.URL != testCDNBase+"/cc/dd/"+bannerHash+".webp" {
		t.Fatalf("banner url = %q, want the landscape cover (the sexual one is skipped for sfw)", cov.Banner.URL)
	}
	if cov.Banner.Width != 1920 || cov.Banner.Height != 1080 || cov.Banner.Thumbhash != "TH-B" {
		t.Fatalf("banner meta = %+v", cov.Banner)
	}
	if cov.Portrait.Sexual != 0 || cov.Banner.Sexual != 0 {
		t.Fatalf("sfw caller received a sexual-flagged cover: %+v / %+v", cov.Portrait, cov.Banner)
	}

	// nsfw=1: the sexual landscape cover sorts first, so it now takes the banner.
	page, err = svc.WorksList(t.Context(), WorksListFilter{Sort: "id", NSFW: true, Include: inc}, "", 50)
	if err != nil {
		t.Fatalf("WorksList covers nsfw: %v", err)
	}
	cov = page.Items[0].Covers
	if cov.Banner == nil || cov.Banner.URL != testCDNBase+"/ee/ff/"+sexualHash+".webp" || cov.Banner.Sexual != 2 {
		t.Fatalf("nsfw banner = %+v, want the sexual landscape cover", cov.Banner)
	}
	// The pinned portrait is unaffected by the gate.
	if cov.Portrait == nil || cov.Portrait.URL != testCDNBase+"/aa/bb/"+portraitHash+".webp" {
		t.Fatalf("nsfw portrait = %+v", cov.Portrait)
	}
}

// TestWorksListCoverSlotsWithoutImageMeta pins the graceful degradation: with
// no image_service lookup the portrait slot still falls back to the lowest
// sort-order cover (a card always has key art), while the banner slot — whose
// only evidence is the dimensions — stays null rather than guessing.
func TestWorksListCoverSlotsWithoutImageMeta(t *testing.T) {
	cleanTables(t)
	svc := newPublicSvcCDN() // no WithImageMeta

	w := createWorkX(t, galgameMediumID, model.ContentRatingAllAges, model.WorkStatusLive, "NoMeta")
	addWorkCover(t, w.ID, hash64("1234"), 0, "main", false, 0, srcVNDB)

	page, err := svc.WorksList(t.Context(),
		WorksListFilter{Sort: "id", Include: ParseWorksListInclude("covers")}, "", 50)
	if err != nil {
		t.Fatalf("WorksList: %v", err)
	}
	cov := page.Items[0].Covers
	if cov == nil || cov.Portrait == nil {
		t.Fatalf("covers = %+v, want a portrait fallback", cov)
	}
	if cov.Portrait.Width != 0 || cov.Portrait.Thumbhash != "" {
		t.Fatalf("portrait carries metadata without a lookup: %+v", cov.Portrait)
	}
	if cov.Banner != nil {
		t.Fatalf("banner = %+v, want null without orientation evidence", cov.Banner)
	}
}

// TestWorkDetailCoversCarryImageMeta pins deliverable 2's detail-face half:
// the frozen covers[] block gains width/height/thumbhash through the same
// helper, and omits them for a hash image_service does not know.
func TestWorkDetailCoversCarryImageMeta(t *testing.T) {
	cleanTables(t)
	svc := newPublicSvcCDN()

	known, unknown := hash64("beef"), hash64("dead")
	svc.WithImageMeta(stubMeta(map[string]ImageMeta{
		known: {Width: 1280, Height: 720, Thumbhash: "TH-K"},
	}))

	w := createWorkX(t, galgameMediumID, model.ContentRatingAllAges, model.WorkStatusLive, "Detail")
	addWorkCover(t, w.ID, known, 0, "main", false, 0, srcVNDB)
	addWorkCover(t, w.ID, unknown, 1, "main", false, 0, srcVNDB)

	rec, found, err := svc.WorkDetail(t.Context(), w.ID, PublicInclude{}, false)
	if err != nil || !found {
		t.Fatalf("WorkDetail: found=%v err=%v", found, err)
	}
	if len(rec.Covers) != 2 {
		t.Fatalf("covers = %d, want 2", len(rec.Covers))
	}
	byHash := map[string]int{known: -1, unknown: -1}
	for i, c := range rec.Covers {
		for h := range byHash {
			if c.URL == testCDNBase+"/"+h[:2]+"/"+h[2:4]+"/"+h+".webp" {
				byHash[h] = i
			}
		}
	}
	kc := rec.Covers[byHash[known]]
	if kc.Width != 1280 || kc.Height != 720 || kc.Thumbhash != "TH-K" {
		t.Fatalf("known cover meta = %+v", kc)
	}
	uc := rec.Covers[byHash[unknown]]
	if uc.Width != 0 || uc.Height != 0 || uc.Thumbhash != "" {
		t.Fatalf("unknown cover must omit metadata, got %+v", uc)
	}
}

// TestParseWorksListIncludeIgnoresUnknown pins the §3.5 clause-2 posture: an
// unknown token is silently ignored, never a 400.
func TestParseWorksListIncludeIgnoresUnknown(t *testing.T) {
	inc := ParseWorksListInclude(" names , covers ,relations,,garbage")
	if !inc.Names || !inc.Covers {
		t.Fatalf("known tokens lost: %+v", inc)
	}
	if inc.Intros || inc.Labels || inc.Ratings {
		t.Fatalf("unknown tokens leaked into the selector: %+v", inc)
	}
	if !inc.any() {
		t.Fatal("any() must be true when a token matched")
	}
	if ParseWorksListInclude("").any() {
		t.Fatal("empty include must select nothing")
	}
}

// itoa keeps the golden string above readable without pulling strconv into
// the test's import block for a single call.
func itoa(v int64) string {
	if v == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	return string(buf[i:])
}
