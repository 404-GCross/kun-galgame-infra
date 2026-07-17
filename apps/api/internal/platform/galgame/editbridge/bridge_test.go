package editbridge

// Real-DB tests over the shared test database. This suite TRUNCATEs shared
// tables — like editing/editspec it must run under -p 1 with sibling suites.
//
// The fixture is a miniature of the surveyed production corpus (03 号地面事实):
// contiguous revision numbers, legacy-NULL changed_fields, an empty-[]
// claimed row, a merged PR with revision link, a reverted row, an old-era
// snapshot missing the omitempty keys, and pending/declined PRs that D2 drops.

import (
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"testing"
	"time"

	catalogMigrate "api/internal/platform/catalog/migrate"
	"api/internal/platform/editing"
	"api/internal/platform/galgame/model"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

var (
	testDB     *gorm.DB
	testBridge *Bridge
)

func TestMain(m *testing.M) {
	dsn := os.Getenv("TEST_DATABASE_DSN")
	if dsn == "" {
		dsn = "host=localhost port=5432 user=postgres password=191007 dbname=kun_galgame_wiki_test sslmode=disable"
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		fmt.Fprintf(os.Stderr, "SKIP: cannot connect to test database: %v\n", err)
		os.Exit(0)
	}
	// Old-machine tables + the engine tables + the legacy bookkeeping columns
	// (exact production DDL via catalog/migrate.EditLegacyColumns).
	if err := db.AutoMigrate(
		&model.GalgameSeries{}, &model.Galgame{},
		&model.GalgamePR{}, &model.GalgameRevision{},
	); err != nil {
		fmt.Fprintf(os.Stderr, "SKIP: galgame migration failed: %v\n", err)
		os.Exit(0)
	}
	if err := editing.AutoMigrate(db); err != nil {
		fmt.Fprintf(os.Stderr, "SKIP: editing migration failed: %v\n", err)
		os.Exit(0)
	}
	if err := catalogMigrate.EditLegacyColumns(db); err != nil {
		fmt.Fprintf(os.Stderr, "SKIP: legacy columns failed: %v\n", err)
		os.Exit(0)
	}
	testDB = db
	testBridge = New(db, editing.NewEngine(db, editing.NewRegistry()))
	os.Exit(m.Run())
}

func cleanTables(t *testing.T) {
	t.Helper()
	for _, table := range []string{
		"edit_proposal_amendment", "edit_revision", "edit_proposal",
		"galgame_revision", "galgame_pr", "galgame", "galgame_series",
	} {
		if err := testDB.Exec("TRUNCATE " + table + " RESTART IDENTITY CASCADE").Error; err != nil {
			t.Fatalf("truncate %s: %v", table, err)
		}
	}
}

// ---- fixture -----------------------------------------------------------------

const (
	gidA = 61
	gidB = 62

	revIDBase = 1000 // legacy revision ids sit ABOVE the engine identity range
	prIDBase  = 500
)

func snapA(nameZh, introZh string, tagIDs []int) *model.Snapshot {
	bid := 4321
	date := "2020-06-01"
	return &model.Snapshot{
		VNDBID: "v6101", BangumiID: &bid,
		ReleaseDate: &date, ReleasePrecision: "day",
		NameEnUS: "Alpha", NameJaJP: "アルファ", NameZhCN: nameZh,
		Banner:    "https://legacy.example/a.webp",
		IntroZhCN: introZh, ContentLimit: "sfw", OriginalLanguage: "ja-jp", AgeLimit: "r18",
		Aliases: []string{"alias-1"}, TagIDs: tagIDs, OfficialIDs: []int{9}, EngineIDs: []int{},
		Links:  []model.SnapshotLink{{Name: "VNDB", Link: "https://vndb.org/v6101", Source: "vndb", SourceKey: "vndb"}},
		Covers: []model.SnapshotCover{{ImageHash: "hashA", SortOrder: 0, Kind: "digital"}},
		Screenshots: []model.SnapshotScreenshot{
			{ImageHash: "shot1", SortOrder: 1, Caption: "cap"},
		},
	}
}

func mustJSON(t *testing.T, s *model.Snapshot) []byte {
	t.Helper()
	b, err := s.ToJSON()
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func insertRevision(t *testing.T, r *model.GalgameRevision) *model.GalgameRevision {
	t.Helper()
	if err := testDB.Create(r).Error; err != nil {
		t.Fatalf("insert revision gid=%d rev=%d: %v", r.GalgameID, r.Revision, err)
	}
	return r
}

// seedFixture builds the miniature old machine and returns the merged PR id.
func seedFixture(t *testing.T) {
	t.Helper()
	cleanTables(t)
	for _, g := range []model.Galgame{
		{ID: gidA, VNDBID: "v6101", UserID: 7},
		{ID: gidB, VNDBID: "v6202", UserID: 8},
	} {
		if err := testDB.Create(&g).Error; err != nil {
			t.Fatalf("insert galgame: %v", err)
		}
	}

	s1 := snapA("初名", "简介一", []int{1, 2})
	s2 := snapA("初名", "简介二", []int{1, 2})
	s4 := snapA("新名", "简介二", []int{1, 2, 3})

	// rev1: created, legacy-NULL changed_fields, migration note.
	insertRevision(t, &model.GalgameRevision{
		ID: revIDBase + 1, GalgameID: gidA, Revision: 1, UserID: 7,
		Action: "created", Note: "初始版本（从历史数据迁移）", Snapshot: mustJSON(t, s1),
	})
	// rev2: updated + recorded changed_fields + minor.
	r2 := &model.GalgameRevision{
		ID: revIDBase + 2, GalgameID: gidA, Revision: 2, UserID: 9,
		Action: "updated", Snapshot: mustJSON(t, s2), IsMinor: true,
	}
	r2.SetChangedFields([]string{"intro_zh_cn"})
	insertRevision(t, r2)
	// rev3: claimed, recorded-empty changed_fields.
	r3 := &model.GalgameRevision{
		ID: revIDBase + 3, GalgameID: gidA, Revision: 3, UserID: 7,
		Action: "claimed", Snapshot: mustJSON(t, s2),
	}
	r3.SetChangedFields([]string{})
	insertRevision(t, r3)
	// rev4: merged (the PR's landing).
	r4 := &model.GalgameRevision{
		ID: revIDBase + 4, GalgameID: gidA, Revision: 4, UserID: 77,
		Action: "merged", Note: "", Snapshot: mustJSON(t, s4),
	}
	r4.SetChangedFields([]string{"name_zh_cn", "tag_ids"})
	insertRevision(t, r4)
	// rev5: reverted back to rev2.
	revertedTo := 2
	r5 := &model.GalgameRevision{
		ID: revIDBase + 5, GalgameID: gidA, Revision: 5, UserID: 7,
		Action: "reverted", Note: "回滚到版本 2", Snapshot: mustJSON(t, s2), RevertedTo: &revertedTo,
	}
	r5.SetChangedFields([]string{"name_zh_cn", "tag_ids"})
	insertRevision(t, r5)

	// gid B: created only, OLD-ERA snapshot (no bid, no release_precision —
	// both omitempty, so a nil/empty struct reproduces the old key set).
	sB := snapA("乙", "乙简介", []int{1})
	sB.BangumiID = nil
	sB.ReleasePrecision = ""
	insertRevision(t, &model.GalgameRevision{
		ID: revIDBase + 11, GalgameID: gidB, Revision: 1, UserID: 8,
		Action: "created", Note: "初始版本（从 moyu 迁移）", Snapshot: mustJSON(t, sB),
	})

	// PRs: one merged (migrates), one pending + one declined (dropped, D2).
	completedBy := 88
	completedAt := model.Timestamp(time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC))
	revID := revIDBase + 4
	for _, pr := range []model.GalgamePR{
		{ID: prIDBase + 1, GalgameID: gidA, UserID: 77, Status: 1,
			BaseRevision: 3, Snapshot: mustJSON(t, s4),
			CompletedBy: &completedBy, CompletedTime: &completedAt, RevisionID: &revID},
		{ID: prIDBase + 2, GalgameID: gidA, UserID: 10, Status: 0,
			Title: "改名", Message: "理由", BaseRevision: 4, Snapshot: mustJSON(t, s2)},
		{ID: prIDBase + 3, GalgameID: gidA, UserID: 11, Status: 2,
			BaseRevision: 4, Snapshot: mustJSON(t, s2), CompletedBy: &completedBy, CompletedTime: &completedAt},
	} {
		if err := testDB.Create(&pr).Error; err != nil {
			t.Fatalf("insert pr: %v", err)
		}
	}
}

func runTransform(t *testing.T) *TransformSummary {
	t.Helper()
	sum, err := Transform(testDB, testDB, TransformOpts{Batch: 3}) // tiny batch: exercise keyset paging
	if err != nil {
		t.Fatalf("transform: %v", err)
	}
	return sum
}

// asJSONValue normalizes any marshalable value for deep comparison (object
// key order erased, numbers unified to float64) — the replay comparator in
// miniature.
func asJSONValue(t *testing.T, v any) any {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var out any
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func requireWireEqual(t *testing.T, label string, oldRow, wire any) {
	t.Helper()
	oldV, wireV := asJSONValue(t, oldRow), asJSONValue(t, wire)
	if !reflect.DeepEqual(oldV, wireV) {
		ob, _ := json.MarshalIndent(oldV, "", " ")
		wb, _ := json.MarshalIndent(wireV, "", " ")
		t.Fatalf("%s wire mismatch\n--- old ---\n%s\n--- bridge ---\n%s", label, ob, wb)
	}
}

// ---- tests -------------------------------------------------------------------

func TestNoteCodec(t *testing.T) {
	cases := []struct{ title, message string }{
		{"标题", "多段\n\n说明"},
		{"标题", ""},
		{"", "只有说明\n\n第二段"},
		{"", ""},
	}
	for _, c := range cases {
		gotT, gotM := DecodeNote(EncodeNote(c.title, c.message))
		if gotT != c.title || gotM != c.message {
			t.Fatalf("codec round-trip (%q,%q) → (%q,%q)", c.title, c.message, gotT, gotM)
		}
	}
	// Plain single-string notes (revert/admin/link paths) are identities
	// through the wire-note recomposition.
	for _, note := range []string{"回滚到版本 2", "理由：重复\n\n补充说明", ""} {
		if got := wireNoteFromProposal(note); got != note {
			t.Fatalf("wire note identity broken: %q → %q", note, got)
		}
	}
}

func TestTransformWireFidelity(t *testing.T) {
	seedFixture(t)
	runTransform(t)
	if _, err := Verify(testDB, testDB, 0); err != nil {
		t.Fatalf("verify gates: %v", err)
	}

	// Every old revision row must reconstruct byte-faithfully (deep-JSON) —
	// reload from DB first so both sides carry jsonb-normalized snapshots.
	var oldRows []model.GalgameRevision
	if err := testDB.Order("id ASC").Find(&oldRows).Error; err != nil {
		t.Fatal(err)
	}
	if len(oldRows) != 6 {
		t.Fatalf("fixture rows = %d", len(oldRows))
	}
	for i := range oldRows {
		old := &oldRows[i]
		wire, err := testBridge.GetRevision(t.Context(), old.GalgameID, old.Revision)
		if err != nil {
			t.Fatalf("GetRevision(%d,%d): %v", old.GalgameID, old.Revision, err)
		}
		requireWireEqual(t, fmt.Sprintf("revision %d/%d", old.GalgameID, old.Revision), old, wire)
	}

	// List: order (seq DESC), totals, include_minor filter.
	list, total, err := testBridge.ListRevisions(t.Context(), gidA, 1, 50, true)
	if err != nil {
		t.Fatal(err)
	}
	if total != 5 || len(list) != 5 || list[0].Revision != 5 || list[4].Revision != 1 {
		t.Fatalf("list total=%d len=%d order=%v", total, len(list), list)
	}
	_, totalNoMinor, err := testBridge.ListRevisions(t.Context(), gidA, 1, 50, false)
	if err != nil {
		t.Fatal(err)
	}
	if totalNoMinor != 4 {
		t.Fatalf("include_minor=false total = %d, want 4", totalNoMinor)
	}
	// GetRevision(0) = latest (the old SubmitPR base lookup contract).
	latest, err := testBridge.GetRevision(t.Context(), gidA, 0)
	if err != nil || latest.Revision != 5 {
		t.Fatalf("latest = %v, %v", latest, err)
	}
}

func TestTransformPRWire(t *testing.T) {
	seedFixture(t)
	runTransform(t)

	var oldMerged model.GalgamePR
	if err := testDB.First(&oldMerged, prIDBase+1).Error; err != nil {
		t.Fatal(err)
	}
	wire, err := testBridge.GetPR(t.Context(), gidA, prIDBase+1)
	if err != nil {
		t.Fatal(err)
	}
	requireWireEqual(t, "merged PR", &oldMerged, wire)

	// D2: pending + declined are dropped — absent from list and detail.
	list, total, err := testBridge.ListPRs(t.Context(), gidA, 1, 50)
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || len(list) != 1 || list[0].ID != prIDBase+1 {
		t.Fatalf("pr list total=%d list=%v", total, list)
	}
	for _, dropped := range []int{prIDBase + 2, prIDBase + 3} {
		if _, err := testBridge.GetPR(t.Context(), gidA, dropped); err != gorm.ErrRecordNotFound {
			t.Fatalf("dropped PR %d: err=%v, want ErrRecordNotFound", dropped, err)
		}
	}

	// The engine proposal id resolves for the write paths.
	engID, err := testBridge.ResolveProposalID(t.Context(), gidA, prIDBase+1)
	if err != nil || engID == 0 {
		t.Fatalf("resolve proposal id: %d, %v", engID, err)
	}
}

func TestFeedCursor(t *testing.T) {
	seedFixture(t)
	runTransform(t)

	feed, err := testBridge.Feed(t.Context(), 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	// Only old action='merged' rows feed downstream ('updated' is excluded
	// even though it maps to the same engine action — legacy_action decides).
	if len(feed) != 1 || feed[0].ID != revIDBase+4 || feed[0].Action != "merged" ||
		feed[0].GalgameID != gidA || feed[0].Revision != 4 || feed[0].UserID != 77 {
		t.Fatalf("feed = %+v", feed)
	}
	after, err := testBridge.Feed(t.Context(), int64(revIDBase+4), 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != 0 {
		t.Fatalf("cursor past end returned %d rows", len(after))
	}
}
