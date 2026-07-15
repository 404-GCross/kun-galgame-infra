package service

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"api/internal/platform/galgame/dto"
	"api/internal/platform/galgame/model"
	"api/internal/platform/galgame/repository"
)

// ── pure projection tests (no DB): the frozen v1 aggregate-record contract ──

func strptr(s string) *string { return &s }
func intptr(i int) *int       { return &i }
func i64ptr(i int64) *int64   { return &i }

func sampleGalgame() *model.Galgame {
	rd := model.Date(time.Date(2016, 11, 25, 0, 0, 0, 0, time.UTC))
	return &model.Galgame{
		ID:               1,
		NameJaJP:         "枯れない世界と終わる花",
		NameZhCN:         "永不枯萎的世界与终结之花",
		NameEnUS:         "Karenai Sekai to Owaru Hana",
		NameZhTW:         "", // → null
		VNDBID:           "v19658",
		BangumiID:        intptr(186675),
		OriginalLanguage: "ja-jp",
		AgeLimit:         "r18",
		ContentLimit:     "sfw",
		Status:           0,
		ReleaseDate:      &rd,
		ReleaseDateTBA:   false,
		IntroZhCN:        "中文简介",
		SeriesID:         intptr(3),
		CatalogWorkID:    i64ptr(3853),
		Updated:          model.Timestamp(time.Date(2026, 7, 14, 8, 0, 0, 0, time.UTC)),
		Alias:            []model.GalgameAlias{{Name: "alt-name"}},
		Cover: []model.GalgameCover{
			{ImageHash: "bannerhash", SortOrder: 0, Width: 1920, Height: 1080, Thumbhash: "tbB", Kind: "main"},
			{ImageHash: "portraithash", PortraitPinned: true, SortOrder: 1, Width: 720, Height: 1080, Thumbhash: "tbP"},
			{ImageHash: "nsfwhash", SortOrder: 2, Width: 800, Height: 600, Thumbhash: "tbN", Kind: "pkgfront", Sexual: 2},
		},
		EffectiveBannerHash:   strptr("bannerhash"),
		EffectivePortraitHash: strptr("portraithash"),
		Tag:                   []model.GalgameTagRelation{{Tag: &model.GalgameTag{ID: 5, Name: "tag-a"}}},
		Official:              []model.GalgameOfficialRelation{{Official: &model.GalgameOfficial{ID: 7, Name: "maker-x"}}},
		Engine:                []model.GalgameEngineRelation{{Engine: &model.GalgameEngine{ID: 9, Name: "engine-y"}}},
	}
}

func sampleScoreMeta() repository.ScoreMeta {
	rating := 76.0
	return repository.ScoreMeta{
		VNDBID: "v19658",
		VNDB:   &model.GalgameVNDBMeta{GalgameID: 1, VNDBID: "v19658", Rating: &rating, VoteCount: 615, SyncedAt: time.Now()},
		EG:     &model.GalgameEGMeta{GalgameID: 1, EGGameID: 23956, VoteCount: 42, SyncedAt: time.Now()},
	}
}

// toMap marshals the record and re-parses it so we assert on the exact wire.
func toMap(t *testing.T, v any) map[string]any {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return m
}

func TestProjectDetailFrozenShape(t *testing.T) {
	svc := &GalgameService{cdnBase: "https://cdn.example.com/img"}
	rec := svc.projectDetail(sampleGalgame(), sampleScoreMeta(),
		PublicInclude{Intro: true, Scores: true, Covers: true, Taxonomy: true}, "sfw")
	m := toMap(t, rec)

	// names: keys always present, BCP-47, empty→null.
	names := m["names"].(map[string]any)
	for _, k := range []string{"ja-jp", "zh-cn", "zh-tw", "en-us"} {
		if _, ok := names[k]; !ok {
			t.Errorf("names missing key %q", k)
		}
	}
	if names["ja-jp"] != "枯れない世界と終わる花" {
		t.Errorf("names.ja-jp = %v", names["ja-jp"])
	}
	if names["zh-tw"] != nil {
		t.Errorf("empty name must be null, got %v", names["zh-tw"])
	}

	// aliases.
	if al := m["aliases"].([]any); len(al) != 1 || al[0] != "alt-name" {
		t.Errorf("aliases = %v", m["aliases"])
	}

	// refs: keys present, id-as-string.
	refs := m["refs"].(map[string]any)
	if refs["vndb"] != "v19658" || refs["bangumi"] != "186675" || refs["erogamescape"] != "23956" {
		t.Errorf("refs = %v", refs)
	}

	// attribution: constant templates + id.
	att := m["attribution"].(map[string]any)
	if att["vndb"] != "https://vndb.org/v19658" {
		t.Errorf("attribution.vndb = %v", att["vndb"])
	}
	if att["bangumi"] != "https://bgm.tv/subject/186675" {
		t.Errorf("attribution.bangumi = %v", att["bangumi"])
	}

	// images: full CDN URLs (never a bare hash); NSFW cover dropped from covers[].
	imgs := m["images"].(map[string]any)
	banner := imgs["banner"].(map[string]any)
	if url := banner["url"].(string); !strings.HasPrefix(url, "https://cdn.example.com/img/") || !strings.HasSuffix(url, "bannerhash.webp") {
		t.Errorf("banner.url = %v", url)
	}
	if banner["width"].(float64) != 1920 {
		t.Errorf("banner.width = %v", banner["width"])
	}
	if url := imgs["portrait"].(map[string]any)["url"].(string); !strings.HasSuffix(url, "portraithash.webp") {
		t.Errorf("portrait.url = %v", url)
	}
	covers := imgs["covers"].([]any)
	if len(covers) != 2 { // banner + portrait; the sexual=2 cover is dropped
		t.Fatalf("covers len = %d (want 2, NSFW dropped), %v", len(covers), covers)
	}
	for _, c := range covers {
		if url := c.(map[string]any)["url"].(string); strings.Contains(url, "nsfwhash") {
			t.Errorf("NSFW-rated cover leaked into covers[]")
		}
	}

	// taxonomy.
	tax := m["taxonomy"].(map[string]any)
	if tags := tax["tags"].([]any); len(tags) != 1 || tags[0] != "tag-a" {
		t.Errorf("taxonomy.tags = %v", tax["tags"])
	}
	off := tax["officials"].([]any)[0].(map[string]any)
	if off["id"].(float64) != 7 || off["name"] != "maker-x" {
		t.Errorf("taxonomy.officials = %v", tax["officials"])
	}
	if tax["series_id"].(float64) != 3 {
		t.Errorf("taxonomy.series_id = %v", tax["series_id"])
	}

	// always-present scalars.
	if m["catalog_work_id"].(float64) != 3853 {
		t.Errorf("catalog_work_id = %v", m["catalog_work_id"])
	}
	if m["updated"] != "2026-07-14T08:00:00Z" {
		t.Errorf("updated = %v", m["updated"])
	}
	if m["release_date"] != "2016-11-25" {
		t.Errorf("release_date = %v", m["release_date"])
	}
	if m["age_limit"] != "r18" {
		t.Errorf("age_limit = %v", m["age_limit"])
	}
	if _, ok := m["scores"]; !ok {
		t.Errorf("scores block missing under include=scores")
	}
	if _, ok := m["intro"]; !ok {
		t.Errorf("intro block missing under include=intro")
	}
}

func TestProjectDetailIncludeGating(t *testing.T) {
	svc := &GalgameService{cdnBase: "https://cdn.example.com/img"}
	rec := svc.projectDetail(sampleGalgame(), sampleScoreMeta(), PublicInclude{}, "sfw")
	m := toMap(t, rec)

	for _, k := range []string{"intro", "scores", "taxonomy"} {
		if _, ok := m[k]; ok {
			t.Errorf("%q must be omitted without include", k)
		}
	}
	// covers omitted without include=covers, but banner/portrait always present.
	imgs := m["images"].(map[string]any)
	if _, ok := imgs["covers"]; ok {
		t.Errorf("images.covers must be omitted without include=covers")
	}
	if imgs["banner"] == nil || imgs["portrait"] == nil {
		t.Errorf("banner/portrait must always be present")
	}
	// refs / attribution / catalog_work_id / updated always present.
	for _, k := range []string{"refs", "attribution", "catalog_work_id", "updated", "names", "aliases"} {
		if _, ok := m[k]; !ok {
			t.Errorf("always-present key %q missing", k)
		}
	}
}

func TestPublicImageURLAndNSFWDrop(t *testing.T) {
	// No CDN base → no image URL is ever emitted (never leak a bare hash).
	noCDN := &GalgameService{}
	if got := noCDN.publicImage("h", ImageMeta{}, ""); got != nil {
		t.Errorf("no cdnBase must yield nil image, got %v", got)
	}
	// Banner pin that is itself NSFW-rated is dropped on the sfw face.
	g := sampleGalgame()
	g.Cover[0].Sexual = 3 // the banner cover is now NSFW-rated
	svc := &GalgameService{cdnBase: "https://cdn.example.com/img"}
	imgs := svc.detailImages(g, false, true)
	if imgs.Banner != nil {
		t.Errorf("NSFW-rated banner must be dropped on the sfw face, got %v", imgs.Banner)
	}
}

// ── cursor round-trips ──

func TestIDCursorRoundtrip(t *testing.T) {
	c := encodeIDCursor(42)
	if got := decodeIDCursor(c); got != 42 {
		t.Errorf("id cursor roundtrip = %d", got)
	}
	if decodeIDCursor("") != 0 || decodeIDCursor("!!not-base64!!") != 0 {
		t.Errorf("invalid id cursor must decode to 0")
	}
}

func TestReleaseCursorRoundtrip(t *testing.T) {
	d := model.Date(time.Date(2020, 3, 15, 0, 0, 0, 0, time.UTC))
	c := encodeReleaseCursor(&d, 7)
	has, dt, dNull, id := decodeReleaseCursor(c)
	if !has || dNull || id != 7 || dt.Format("2006-01-02") != "2020-03-15" {
		t.Errorf("release cursor roundtrip = (has %v, %v, null %v, id %d)", has, dt, dNull, id)
	}
	// undated boundary.
	cn := encodeReleaseCursor(nil, 9)
	has, _, dNull, id = decodeReleaseCursor(cn)
	if !has || !dNull || id != 9 {
		t.Errorf("undated release cursor = (has %v, null %v, id %d)", has, dNull, id)
	}
}

func TestChangesCursorMicroPrecision(t *testing.T) {
	// A timestamp with sub-millisecond micros must round-trip losslessly so the
	// keyset boundary is neither duplicated nor skipped.
	ts := time.Unix(1_700_000_000, 123_456_000).UTC() // 123456 micros
	c := encodeChangesCursor(ts, 11)
	has, boundary, id := decodeChangesCursor(c)
	if !has || id != 11 {
		t.Fatalf("changes cursor = (has %v, id %d)", has, id)
	}
	if boundary.UnixMicro() != ts.UnixMicro() {
		t.Errorf("micro precision lost: got %d want %d", boundary.UnixMicro(), ts.UnixMicro())
	}
}

// ── DB-backed keyset / changes / NSFW (wiki test DB) ──

func migratePublicMeta(t *testing.T) {
	t.Helper()
	if err := testDB.AutoMigrate(&model.GalgameVNDBMeta{}, &model.GalgameBangumiMeta{}, &model.GalgameEGMeta{}); err != nil {
		t.Fatalf("migrate score meta: %v", err)
	}
}

func insertPublicGalgame(t *testing.T, id int, name, contentLimit string, updated time.Time) {
	t.Helper()
	g := model.Galgame{ID: id, NameJaJP: name, UserID: 1, Status: 0, ContentLimit: contentLimit, OriginalLanguage: "ja-jp", AgeLimit: "all"}
	if err := testDB.Create(&g).Error; err != nil {
		t.Fatalf("insert galgame %d: %v", id, err)
	}
	// Override the autoUpdateTime value for a deterministic changes ordering.
	if err := testDB.Model(&model.Galgame{}).Where("id = ?", id).UpdateColumn("updated", updated).Error; err != nil {
		t.Fatalf("set updated %d: %v", id, err)
	}
}

func TestPublicListCursorTwoPages(t *testing.T) {
	cleanTables(t)
	migratePublicMeta(t)
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := 1; i <= 5; i++ {
		insertPublicGalgame(t, i, "g", "sfw", base.Add(time.Duration(i)*time.Hour))
	}
	ctx := context.Background()

	page1, err := testSvc.PublicList(ctx, "id", "", 2, "sfw")
	if err != nil {
		t.Fatalf("page1: %v", err)
	}
	if ids := itemIDs(page1.Items); len(ids) != 2 || ids[0] != 1 || ids[1] != 2 {
		t.Fatalf("page1 ids = %v (want [1 2])", ids)
	}
	if page1.NextCursor == nil {
		t.Fatalf("page1 next_cursor must be set")
	}

	page2, err := testSvc.PublicList(ctx, "id", *page1.NextCursor, 2, "sfw")
	if err != nil {
		t.Fatalf("page2: %v", err)
	}
	if ids := itemIDs(page2.Items); len(ids) != 2 || ids[0] != 3 || ids[1] != 4 {
		t.Fatalf("page2 ids = %v (want [3 4])", ids)
	}

	page3, err := testSvc.PublicList(ctx, "id", *page2.NextCursor, 2, "sfw")
	if err != nil {
		t.Fatalf("page3: %v", err)
	}
	if ids := itemIDs(page3.Items); len(ids) != 1 || ids[0] != 5 {
		t.Fatalf("page3 ids = %v (want [5])", ids)
	}
	if page3.NextCursor != nil {
		t.Errorf("last page next_cursor must be nil, got %v", *page3.NextCursor)
	}
}

func TestPublicChangesKeysetAndEmptyPage(t *testing.T) {
	cleanTables(t)
	migratePublicMeta(t)
	base := time.Date(2026, 2, 1, 12, 0, 0, 0, time.UTC)
	insertPublicGalgame(t, 1, "a", "sfw", base.Add(1*time.Minute))
	insertPublicGalgame(t, 2, "b", "sfw", base.Add(2*time.Minute))
	insertPublicGalgame(t, 3, "c", "sfw", base.Add(3*time.Minute))
	ctx := context.Background()

	p1, err := testSvc.PublicChanges(ctx, "", 2, "sfw")
	if err != nil {
		t.Fatalf("changes p1: %v", err)
	}
	if ids := changeIDs(p1.Items); len(ids) != 2 || ids[0] != 1 || ids[1] != 2 {
		t.Fatalf("changes p1 = %v (want [1 2])", ids)
	}
	if p1.NextCursor == nil {
		t.Fatalf("changes p1 next_cursor must advance")
	}

	p2, err := testSvc.PublicChanges(ctx, *p1.NextCursor, 2, "sfw")
	if err != nil {
		t.Fatalf("changes p2: %v", err)
	}
	if ids := changeIDs(p2.Items); len(ids) != 1 || ids[0] != 3 {
		t.Fatalf("changes p2 = %v (want [3])", ids)
	}

	// Empty page: no new changes → next_cursor echoes the incoming cursor so a
	// consumer can keep polling the same position.
	p3, err := testSvc.PublicChanges(ctx, *p2.NextCursor, 2, "sfw")
	if err != nil {
		t.Fatalf("changes p3: %v", err)
	}
	if len(p3.Items) != 0 {
		t.Fatalf("changes p3 must be empty, got %v", changeIDs(p3.Items))
	}
	if p3.NextCursor == nil || *p3.NextCursor != *p2.NextCursor {
		t.Errorf("empty-page next_cursor must echo the incoming cursor")
	}
}

func TestPublicNSFWHiddenAndDetail404(t *testing.T) {
	cleanTables(t)
	migratePublicMeta(t)
	now := time.Now().UTC()
	insertPublicGalgame(t, 1, "sfw-game", "sfw", now)
	insertPublicGalgame(t, 2, "nsfw-game", "nsfw", now)
	ctx := context.Background()

	list, err := testSvc.PublicList(ctx, "id", "", 20, "sfw")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if ids := itemIDs(list.Items); len(ids) != 1 || ids[0] != 1 {
		t.Fatalf("sfw list must hide the nsfw entry, got %v", ids)
	}

	if _, found, _, err := testSvc.PublicDetail(ctx, 2, PublicInclude{}, "sfw"); err != nil || found {
		t.Errorf("nsfw detail on sfw face must be 404 (found=false), got found=%v err=%v", found, err)
	}
	if _, found, _, err := testSvc.PublicDetail(ctx, 1, PublicInclude{}, "sfw"); err != nil || !found {
		t.Errorf("sfw detail must be found, got found=%v err=%v", found, err)
	}
	if _, found, _, err := testSvc.PublicDetail(ctx, 999, PublicInclude{}, "sfw"); err != nil || found {
		t.Errorf("unknown id must be 404 (found=false), got found=%v err=%v", found, err)
	}
}

func itemIDs(items []dto.PublicGalgameItem) []int {
	out := make([]int, len(items))
	for i := range items {
		out[i] = items[i].ID
	}
	return out
}

func changeIDs(items []dto.PublicChangeItem) []int {
	out := make([]int, len(items))
	for i := range items {
		out[i] = items[i].ID
	}
	return out
}
