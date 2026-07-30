package editspec_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"

	"api/internal/platform/authz"
	"api/internal/platform/editing"
	"api/internal/platform/galgame/editspec"
	"api/internal/platform/galgame/model"
	"api/internal/platform/galgame/perm"
	"api/internal/platform/galgame/repository"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// Real-DB tests for the galgame.game registration (E2a): both "pools" are the
// one test database — engine tables and galgame tables side by side, exactly
// the dev-single-host posture. NOTE: this suite TRUNCATEs the edit_* tables,
// so it collides with internal/platform/editing and catalog/editspec — always
// run the module with -p 1.

var (
	testDB  *gorm.DB
	testCtx = context.Background()
)

func TestMain(m *testing.M) {
	dsn := os.Getenv("TEST_DATABASE_DSN")
	if dsn == "" {
		dsn = "host=localhost port=5432 user=postgres password=191007 dbname=kun_galgame_wiki_test sslmode=disable"
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "SKIP: cannot connect to test database: %v\n", err)
		os.Exit(0)
	}
	if err := db.AutoMigrate(
		&model.GalgameSeries{},
		&model.GalgameTag{},
		&model.GalgameTagAlias{},
		&model.GalgameOfficial{},
		&model.GalgameOfficialAlias{},
		&model.GalgameEngine{},
		&model.Galgame{},
		&model.GalgameAlias{},
		&model.GalgameTagRelation{},
		&model.GalgameOfficialRelation{},
		&model.GalgameEngineRelation{},
		&model.GalgameLink{},
		&model.GalgameCover{},
		&model.GalgameScreenshot{},
		&model.GalgamePR{},
		&model.GalgameRevision{},
		&model.GalgameContributor{},
	); err != nil {
		fmt.Fprintf(os.Stderr, "SKIP: galgame migration failed: %v\n", err)
		os.Exit(0)
	}
	if err := editing.AutoMigrate(db); err != nil {
		fmt.Fprintf(os.Stderr, "SKIP: editing migration failed: %v\n", err)
		os.Exit(0)
	}
	// The partial unique indexes migrate-catalog creates in production —
	// the pinned-cover one is load-bearing for the covers Apply test.
	_ = db.Exec(`DROP INDEX IF EXISTS idx_galgame_vndb_id`).Error
	_ = db.Exec(`DROP INDEX IF EXISTS uni_galgame_vndb_id`).Error
	_ = db.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS uq_galgame_vndb_id_nonempty
		ON galgame(vndb_id) WHERE vndb_id <> ''`).Error
	_ = db.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS idx_galgame_cover_pinned
		ON galgame_cover(galgame_id) WHERE sort_order = 0`).Error
	_ = db.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS idx_galgame_cover_portrait_pinned
		ON galgame_cover(galgame_id) WHERE portrait_pinned`).Error
	testDB = db
	os.Exit(m.Run())
}

func newGameEngine(t *testing.T) *editing.Engine {
	return newGameEngineHooks(t, nil, nil)
}

// newGameEngineHooks truncates and registers galgame.game with injected OnMerge
// spies (nil = that write-through is unwired; contributor recording still runs)
// so every side effect of the single write path can be asserted.
func newGameEngineHooks(t *testing.T, reindex func(entityID int), claim func(context.Context, int64)) *editing.Engine {
	t.Helper()
	for _, table := range []string{
		"edit_proposal_amendment", "edit_proposal", "edit_revision",
		"galgame_link", "galgame_alias", "galgame_cover", "galgame_screenshot",
		"galgame_tag_relation", "galgame_official_relation", "galgame_engine_relation",
		"galgame_contributor", "galgame_revision", "galgame_pr",
		"galgame", "galgame_tag", "galgame_official", "galgame_engine", "galgame_series",
	} {
		if err := testDB.Exec("TRUNCATE " + table + " RESTART IDENTITY CASCADE").Error; err != nil {
			t.Fatalf("truncate %s: %v", table, err)
		}
	}
	reg := editing.NewRegistry()
	if err := editspec.RegisterGame(reg, testDB, reindex, claim); err != nil {
		t.Fatalf("register galgame.game: %v", err)
	}
	return editing.NewEngine(testDB, reg)
}

// contributorUIDs returns the (sorted) user ids credited as contributors of gid.
func contributorUIDs(t *testing.T, gid int) []int {
	t.Helper()
	var uids []int
	if err := testDB.Model(&model.GalgameContributor{}).
		Where("galgame_id = ?", gid).Order("user_id ASC").Pluck("user_id", &uids).Error; err != nil {
		t.Fatalf("load contributors: %v", err)
	}
	return uids
}

// TestOnMergeSideEffects pins the E3b-tail parity fix: the single write path's
// OnMerge hook records the merge's contributor(s) — proposer AND amender — and
// fires the search reindex, on every merge path (direct edit, reviewer merge,
// revert), so the kungal BFF / a future /v1 writer can never drop them.
func TestOnMergeSideEffects(t *testing.T) {
	var reindexed []int
	e := newGameEngineHooks(t, func(id int) { reindexed = append(reindexed, id) }, nil)
	g := createGame(t, nil)

	// 1) Direct edit (automerge) by a trusted proposer → actor credited + reindex.
	proposer := gameActor(101, editing.TrustedTier)
	if _, _, err := e.CreateProposal(testCtx, editing.CreateProposalInput{
		EntityType: editspec.TypeGame, EntityID: int64(g.ID),
		Patch: map[string]any{editspec.FieldNameEnUS: "Direct Edit"}, Actor: proposer,
	}); err != nil {
		t.Fatalf("direct edit: %v", err)
	}
	if got := contributorUIDs(t, g.ID); len(got) != 1 || got[0] != 101 {
		t.Fatalf("after direct edit want contributors [101], got %v", got)
	}

	// 2) Open proposal by an untrusted proposer, amended then merged by a
	// reviewer → BOTH proposer and amender credited (the double signature).
	untrusted := gameActor(202, 0)
	prop, rev, err := e.CreateProposal(testCtx, editing.CreateProposalInput{
		EntityType: editspec.TypeGame, EntityID: int64(g.ID),
		Patch: map[string]any{editspec.FieldNameJaJP: "提案タイトル"}, Actor: untrusted,
	})
	if err != nil {
		t.Fatalf("open proposal: %v", err)
	}
	if rev != nil {
		t.Fatalf("untrusted proposal should stay open, got revision %d", rev.Seq)
	}
	reviewer := gameActor(303, editing.TrustedTier, "ren")
	if _, err := e.AmendProposal(testCtx, prop.ID, editing.AmendInput{
		Set: map[string]any{editspec.FieldNameJaJP: "修正タイトル"}, Actor: reviewer,
	}); err != nil {
		t.Fatalf("amend: %v", err)
	}
	if _, err := e.MergeProposal(testCtx, prop.ID, reviewer, ""); err != nil {
		t.Fatalf("merge: %v", err)
	}
	// proposer 202 (actor) and reviewer 303 (amender) join 101 from step 1.
	if got := contributorUIDs(t, g.ID); len(got) != 3 || got[0] != 101 || got[1] != 202 || got[2] != 303 {
		t.Fatalf("after amended merge want contributors [101 202 303], got %v", got)
	}

	// 3) Idempotent: re-crediting an existing contributor adds no duplicate row.
	if _, _, err := e.CreateProposal(testCtx, editing.CreateProposalInput{
		EntityType: editspec.TypeGame, EntityID: int64(g.ID),
		Patch: map[string]any{editspec.FieldNameEnUS: "Direct Edit 2"}, Actor: proposer,
	}); err != nil {
		t.Fatalf("second direct edit: %v", err)
	}
	if got := contributorUIDs(t, g.ID); len(got) != 3 {
		t.Fatalf("idempotent re-credit changed the set: %v", got)
	}

	// Reindex fired once per landed merge (steps 1, 2, 3) — never on the open
	// proposal in step 2.
	if len(reindexed) != 3 {
		t.Fatalf("want 3 reindex calls (each landed merge), got %d: %v", len(reindexed), reindexed)
	}
	for _, id := range reindexed {
		if id != g.ID {
			t.Fatalf("reindex called for %d, want %d", id, g.ID)
		}
	}
}

// TestOnMergeClaimsCatalogIdentity pins wave 146's half of the same seam: every
// status transition on this family lands as an engine merge, so the catalog
// claim hangs off OnMerge and no publication path can forget it — approve, VNDB
// draft claim and unban all reach it, and it fires on the merge, never on an
// open proposal.
func TestOnMergeClaimsCatalogIdentity(t *testing.T) {
	var claimed []int64
	e := newGameEngineHooks(t, nil, func(_ context.Context, id int64) { claimed = append(claimed, id) })
	g := createGame(t, nil)
	staff := gameActor(11, editing.TrustedTier, "admin")

	// A status transition to published — the transition the 404 was about.
	if _, rev, err := e.CreateProposal(testCtx, editing.CreateProposalInput{
		EntityType: editspec.TypeGame, EntityID: int64(g.ID),
		Patch: map[string]any{editspec.FieldStatus: float64(model.GalgameStatusBanned)}, Actor: staff,
	}); err != nil || rev == nil {
		t.Fatalf("ban: rev=%v err=%v", rev, err)
	}
	if _, rev, err := e.CreateProposal(testCtx, editing.CreateProposalInput{
		EntityType: editspec.TypeGame, EntityID: int64(g.ID),
		Patch: map[string]any{editspec.FieldStatus: float64(model.GalgameStatusPublished)}, Actor: staff,
	}); err != nil || rev == nil {
		t.Fatalf("unban: rev=%v err=%v", rev, err)
	}

	// An open proposal (untrusted, default-policy field) must NOT claim: nothing
	// landed, so there is nothing to register.
	if _, rev, err := e.CreateProposal(testCtx, editing.CreateProposalInput{
		EntityType: editspec.TypeGame, EntityID: int64(g.ID),
		Patch: map[string]any{editspec.FieldNameEnUS: "Pending"}, Actor: gameActor(22, 0),
	}); err != nil || rev != nil {
		t.Fatalf("open proposal: rev=%v err=%v", rev, err)
	}

	if len(claimed) != 2 {
		t.Fatalf("want 2 claim calls (each landed status merge), got %d: %v", len(claimed), claimed)
	}
	for _, id := range claimed {
		if id != int64(g.ID) {
			t.Fatalf("claim called for %d, want %d", id, g.ID)
		}
	}
}

// gameActor resolves permissions through the REAL galgame perm bundles.
func gameActor(uid int64, tier int16, roles ...string) editing.PolicyContext {
	return editing.PolicyContext{
		UserID: uid, Site: editspec.SiteGalgameWiki, TrustTier: tier,
		HasPerm: func(key string) bool {
			return perm.Resolver.Can(roles, authz.Permission(key))
		},
	}
}

// createGame writes a fixture galgame through the historical single write
// path (bare insert + ApplySnapshot), the same way Create/Submit do.
func createGame(t *testing.T, tagIDs []int) *model.Galgame {
	t.Helper()
	bid := 4242
	g := &model.Galgame{VNDBID: "v99999", UserID: 7, Status: 0, BangumiID: &bid}
	if err := testDB.Create(g).Error; err != nil {
		t.Fatalf("create galgame: %v", err)
	}
	date := "2020-01-15"
	snap := &model.Snapshot{
		VNDBID: "v99999", BangumiID: &bid,
		ReleaseDate: &date, ReleasePrecision: "day",
		NameJaJP: "初期タイトル", NameZhCN: "初始标题",
		IntroZhCN: "初始简介", ContentLimit: "sfw",
		OriginalLanguage: "ja-jp", AgeLimit: "r18",
		Aliases: []string{"alias-a"}, TagIDs: tagIDs,
		OfficialIDs: []int{}, EngineIDs: []int{},
		Links: []model.SnapshotLink{{Name: "VNDB", Link: "https://vndb.org/v99999", Source: "vndb", SourceKey: "vndb"}},
		Covers: []model.SnapshotCover{
			{ImageHash: "hash-pinned", SortOrder: 0},
			{ImageHash: "hash-alt", SortOrder: 1},
		},
		Screenshots: []model.SnapshotScreenshot{{ImageHash: "hash-shot", SortOrder: 1, Caption: "cg"}},
	}
	if err := testDB.Transaction(func(tx *gorm.DB) error {
		return repository.ApplySnapshot(tx, g.ID, 7, snap)
	}); err != nil {
		t.Fatalf("apply fixture snapshot: %v", err)
	}
	return g
}

func createTag(t *testing.T, name string) int {
	t.Helper()
	tag := &model.GalgameTag{Name: name, Category: "content"}
	if err := testDB.Create(tag).Error; err != nil {
		t.Fatalf("create tag: %v", err)
	}
	return tag.ID
}

func TestGameLoadSnapshot(t *testing.T) {
	e := newGameEngine(t)
	tagA := createTag(t, "校园")
	g := createGame(t, []int{tagA})

	spec, ok := e.Registry().Type(editspec.TypeGame)
	if !ok {
		t.Fatal("galgame.game not registered")
	}
	snap, err := spec.LoadSnapshot(testCtx, int64(g.ID))
	if err != nil {
		t.Fatalf("LoadSnapshot: %v", err)
	}
	// bid + release_precision are set on the fixture, so every historical key
	// plus status must be present: 25 old keys + status = 26.
	if len(snap) != 26 {
		keys := make([]string, 0, len(snap))
		for k := range snap {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		t.Fatalf("want 26 keys, got %d: %v", len(snap), keys)
	}
	if snap[editspec.FieldStatus] != float64(0) {
		t.Fatalf("status = %#v, want float64(0)", snap[editspec.FieldStatus])
	}
	if snap[editspec.FieldNameJaJP] != "初期タイトル" {
		t.Fatalf("name_ja_jp = %#v", snap[editspec.FieldNameJaJP])
	}
	if snap[editspec.FieldReleaseDate] != "2020-01-15" {
		t.Fatalf("release_date = %#v", snap[editspec.FieldReleaseDate])
	}
	// Missing entity → typed not-found.
	if _, err := spec.LoadSnapshot(testCtx, 999999); !errors.Is(err, editing.ErrEntityNotFound) {
		t.Fatalf("missing entity: %v", err)
	}
}

func TestGameDirectEditEndToEnd(t *testing.T) {
	e := newGameEngine(t)
	tagA := createTag(t, "校园")
	tagB := createTag(t, "治愈")
	g := createGame(t, []int{tagA})

	editor := gameActor(42, editing.TrustedTier) // trusted direct-edit route
	prop, rev, err := e.CreateProposal(testCtx, editing.CreateProposalInput{
		EntityType: editspec.TypeGame, EntityID: int64(g.ID),
		Patch: map[string]any{
			editspec.FieldNameZhCN: "新标题",
			editspec.FieldTagIDs:   []any{float64(tagB)},
			editspec.FieldCovers: []any{
				map[string]any{"image_hash": "hash-new", "sort_order": float64(0)},
				map[string]any{"image_hash": "hash-alt", "sort_order": float64(2)},
			},
			editspec.FieldIntroZhCN: "前文 ![img](https://x.example/i.png) 后文",
		},
		Note:  "direct edit",
		Actor: editor,
	})
	if err != nil {
		t.Fatalf("direct edit: %v", err)
	}
	if rev == nil || rev.Action != editing.ActionDirect {
		t.Fatalf("want automerged direct revision, got %+v", rev)
	}
	if prop.Status != editing.StatusMerged {
		t.Fatalf("proposal status = %d", prop.Status)
	}

	var after model.Galgame
	if err := testDB.First(&after, g.ID).Error; err != nil {
		t.Fatal(err)
	}
	if after.NameZhCN != "新标题" {
		t.Fatalf("name_zh_cn = %q", after.NameZhCN)
	}
	if strings.Contains(after.IntroZhCN, "![") || !strings.Contains(after.IntroZhCN, "前文") {
		t.Fatalf("intro not image-stripped: %q", after.IntroZhCN)
	}
	var tagIDs []int
	testDB.Model(&model.GalgameTagRelation{}).Where("galgame_id = ?", g.ID).Pluck("tag_id", &tagIDs)
	if len(tagIDs) != 1 || tagIDs[0] != tagB {
		t.Fatalf("tag relations = %v, want [%d]", tagIDs, tagB)
	}
	var covers []model.GalgameCover
	testDB.Where("galgame_id = ?", g.ID).Order("sort_order ASC").Find(&covers)
	// image_hash is a blank-padded char column — trim before comparing.
	if len(covers) != 2 || strings.TrimSpace(covers[0].ImageHash) != "hash-new" || covers[0].SortOrder != 0 {
		t.Fatalf("covers = %+v", covers)
	}

	var changed []string
	if err := json.Unmarshal(rev.ChangedFields, &changed); err != nil {
		t.Fatal(err)
	}
	want := []string{editspec.FieldCovers, editspec.FieldIntroZhCN, editspec.FieldNameZhCN, editspec.FieldTagIDs}
	sort.Strings(changed)
	if fmt.Sprint(changed) != fmt.Sprint(want) {
		t.Fatalf("changed_fields = %v, want %v", changed, want)
	}
	var snap map[string]any
	if err := json.Unmarshal(rev.Snapshot, &snap); err != nil {
		t.Fatal(err)
	}
	if _, ok := snap[editspec.FieldStatus]; !ok {
		t.Fatal("revision snapshot must carry galgame.game.status")
	}
}

func TestGameLockedAndPermFields(t *testing.T) {
	e := newGameEngine(t)
	g := createGame(t, nil)

	plain := gameActor(42, editing.TrustedTier)
	staff := gameActor(43, editing.TrustedTier, "moderator")

	// bid is locked for everyone.
	var lockedErr *editing.LockedFieldError
	if _, _, err := e.CreateProposal(testCtx, editing.CreateProposalInput{
		EntityType: editspec.TypeGame, EntityID: int64(g.ID),
		Patch: map[string]any{editspec.FieldBID: float64(1)}, Actor: staff,
	}); !errors.As(err, &lockedErr) {
		t.Fatalf("bid patch: %v", err)
	}

	// vndb_id is perm-gated: plain trusted user rejected, moderator lands it.
	var permErr *editing.PermissionError
	if _, _, err := e.CreateProposal(testCtx, editing.CreateProposalInput{
		EntityType: editspec.TypeGame, EntityID: int64(g.ID),
		Patch: map[string]any{editspec.FieldVNDBID: "v123"}, Actor: plain,
	}); !errors.As(err, &permErr) {
		t.Fatalf("vndb_id without perm: %v", err)
	}
	if _, rev, err := e.CreateProposal(testCtx, editing.CreateProposalInput{
		EntityType: editspec.TypeGame, EntityID: int64(g.ID),
		Patch: map[string]any{editspec.FieldVNDBID: "v123"}, Actor: staff,
	}); err != nil || rev == nil {
		t.Fatalf("vndb_id with perm: rev=%v err=%v", rev, err)
	}

	// status is the management direct-edit field.
	if _, _, err := e.CreateProposal(testCtx, editing.CreateProposalInput{
		EntityType: editspec.TypeGame, EntityID: int64(g.ID),
		Patch: map[string]any{editspec.FieldStatus: float64(1)}, Actor: plain,
	}); !errors.As(err, &permErr) {
		t.Fatalf("status without perm: %v", err)
	}
	if _, rev, err := e.CreateProposal(testCtx, editing.CreateProposalInput{
		EntityType: editspec.TypeGame, EntityID: int64(g.ID),
		Patch: map[string]any{editspec.FieldStatus: float64(1)}, Actor: staff,
	}); err != nil || rev == nil || rev.Action != editing.ActionDirect {
		t.Fatalf("status with perm: rev=%v err=%v", rev, err)
	}
	var after model.Galgame
	testDB.First(&after, g.ID)
	if after.Status != 1 || after.VNDBID != "v123" {
		t.Fatalf("status=%d vndb=%q after staff edits", after.Status, after.VNDBID)
	}
}

func TestGameValidators(t *testing.T) {
	e := newGameEngine(t)
	g := createGame(t, nil)
	editor := gameActor(42, editing.TrustedTier)

	bad := []struct {
		name  string
		patch map[string]any
	}{
		{"links unknown key", map[string]any{editspec.FieldLinks: []any{
			map[string]any{"name": "x", "link": "https://x", "bogus": "y"}}}},
		{"covers two pinned", map[string]any{editspec.FieldCovers: []any{
			map[string]any{"image_hash": "a", "sort_order": float64(0)},
			map[string]any{"image_hash": "b", "sort_order": float64(0)}}}},
		{"release_date bad format", map[string]any{editspec.FieldReleaseDate: "2026/01/01"}},
		{"tag id zero", map[string]any{editspec.FieldTagIDs: []any{float64(0)}}},
		{"status out of range", map[string]any{editspec.FieldStatus: float64(9)}},
		{"alias empty", map[string]any{editspec.FieldAliases: []any{""}}},
		// NOTE: a null list is NOT in this table — it decodes as the empty
		// list (old-snapshot fidelity: pre-2025 snapshots store null for list
		// fields and the old ApplySnapshot treated nil as "clear").
	}
	staff := gameActor(43, editing.TrustedTier, "moderator")
	for _, tc := range bad {
		t.Run(tc.name, func(t *testing.T) {
			var vErr *editing.ValidationError
			_, _, err := e.CreateProposal(testCtx, editing.CreateProposalInput{
				EntityType: editspec.TypeGame, EntityID: int64(g.ID),
				Patch: tc.patch, Actor: staff,
			})
			if !errors.As(err, &vErr) {
				t.Fatalf("want ValidationError, got %v", err)
			}
		})
	}

	// The pre-P3 empty release_precision is accepted and applied as "unknown"
	// (ResolveReleasePrecision's terminal fallback, mirrored per-field).
	if _, rev, err := e.CreateProposal(testCtx, editing.CreateProposalInput{
		EntityType: editspec.TypeGame, EntityID: int64(g.ID),
		Patch: map[string]any{editspec.FieldReleasePrecision: ""}, Actor: editor,
	}); err != nil || rev == nil {
		t.Fatalf("empty precision: rev=%v err=%v", rev, err)
	}
	var after model.Galgame
	testDB.First(&after, g.ID)
	if after.ReleasePrecision != "unknown" {
		t.Fatalf("release_precision = %q, want unknown", after.ReleasePrecision)
	}
}

func TestGameRecordCreated(t *testing.T) {
	e := newGameEngine(t)
	g := createGame(t, nil)
	actor := gameActor(7, editing.TrustedTier)

	rev, err := e.RecordCreated(testCtx, editspec.TypeGame, int64(g.ID), actor,
		[]string{editspec.FieldNameJaJP, editspec.FieldCovers})
	if err != nil {
		t.Fatalf("RecordCreated: %v", err)
	}
	if rev.Seq != 1 || rev.Action != editing.ActionCreated || rev.ProposalID != nil {
		t.Fatalf("birth revision = %+v", rev)
	}
	var snap map[string]any
	if err := json.Unmarshal(rev.Snapshot, &snap); err != nil {
		t.Fatal(err)
	}
	if snap[editspec.FieldNameJaJP] != "初期タイトル" {
		t.Fatalf("birth snapshot name = %#v", snap[editspec.FieldNameJaJP])
	}

	// Birth-only: a second record on the same entity must refuse.
	if _, err := e.RecordCreated(testCtx, editspec.TypeGame, int64(g.ID), actor, nil); err == nil {
		t.Fatal("second RecordCreated must fail")
	}
	// Unknown changed key fails loud.
	var unknownErr *editing.UnknownFieldError
	if _, err := e.RecordCreated(testCtx, editspec.TypeGame, 12345, actor,
		[]string{"galgame.game.nope"}); !errors.As(err, &unknownErr) {
		t.Fatalf("unknown key: %v", err)
	}
}
