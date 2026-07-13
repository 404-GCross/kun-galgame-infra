package bangumienrich

import (
	"encoding/json"
	"fmt"
	"os"
	"testing"

	"api/internal/platform/catalog/bangumiwiki"
	srcb "api/internal/platform/catalog/srcbangumi"
	"api/internal/platform/galgame/model"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// Integration tests against a real Postgres: wiki tables and the src_bangumi
// Silver schema live in the SAME test database (both sides self-migrate, as
// in CI's shared job database).
var testDB *gorm.DB

func TestMain(m *testing.M) {
	dsn := os.Getenv("TEST_DATABASE_DSN")
	if dsn == "" {
		// Default: local test database
		dsn = "host=localhost port=5432 user=postgres password=postgres dbname=kun_catalog_test sslmode=disable"
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "SKIP: cannot connect to test database: %v\n", err)
		os.Exit(0)
	}
	if err := db.AutoMigrate(&model.Galgame{}, &model.GalgameAlias{}, &model.GalgameRevision{}, &model.GalgameBangumiMeta{}); err != nil {
		fmt.Fprintf(os.Stderr, "SKIP: wiki automigrate failed: %v\n", err)
		os.Exit(0)
	}
	// The alias dedup unique (created by cmd/dedup-galgame-alias in prod).
	if err := db.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS idx_galgame_alias_unique
		ON galgame_alias (galgame_id, name)`).Error; err != nil {
		fmt.Fprintf(os.Stderr, "SKIP: alias unique index failed: %v\n", err)
		os.Exit(0)
	}
	if err := srcb.EnsureSchema(db); err != nil {
		fmt.Fprintf(os.Stderr, "SKIP: src_bangumi schema failed: %v\n", err)
		os.Exit(0)
	}
	// Minimal catalog exact-anchor face (step 43 candidate source). The real
	// table is owned by cmd/migrate-catalog; only the columns the anchor query
	// reads are needed here (schema-isolation precedent, step 33). matched_by
	// is included NOT NULL to stay insert-compatible with the full schema when
	// this runs against a shared DB where migrate-catalog already created it.
	if err := db.Exec(`CREATE TABLE IF NOT EXISTS catalog_external_ref (
		entity_type smallint NOT NULL,
		entity_id   bigint   NOT NULL,
		source_id   smallint NOT NULL,
		external_id text     NOT NULL,
		link_kind   smallint NOT NULL,
		matched_by  text     NOT NULL,
		PRIMARY KEY (entity_type, entity_id, source_id, external_id)
	)`).Error; err != nil {
		fmt.Fprintf(os.Stderr, "SKIP: catalog_external_ref schema failed: %v\n", err)
		os.Exit(0)
	}
	testDB = db
	os.Exit(m.Run())
}

func clean(t *testing.T) {
	t.Helper()
	for _, table := range []string{
		"galgame_bangumi_meta", "galgame_alias", "galgame_revision", "galgame",
		"src_bangumi.subject", "catalog_external_ref",
	} {
		require.NoError(t, testDB.Exec("TRUNCATE "+table+" RESTART IDENTITY CASCADE").Error)
	}
}

func seedSubject(t *testing.T, id int64, typ int, name, nameCN, summary string, aliases []string, score float64, rank int) {
	t.Helper()
	infobox := "{{Infobox Game\n|中文名= " + nameCN + "\n|别名={\n"
	for _, a := range aliases {
		infobox += "[" + a + "]\n"
	}
	infobox += "}\n}}"
	parsed, perr := parseForTest(infobox)
	require.Empty(t, perr)
	require.NoError(t, testDB.Create(&srcb.Subject{
		ID: id, Type: typ, Name: name, NameCN: nameCN,
		InfoboxRaw: infobox, InfoboxParsed: parsed,
		Summary: summary, Score: score, Rank: rank,
		ScoreDetails:  []byte(`{"10": 7, "9": 3}`),
		ParserVersion: srcb.ParserVersion,
	}).Error)
}

// seedGalgame inserts a galgame with a nullable curated bid and a nullable
// catalog_work_id (the step-43 anchor pointer). Pass nil for either to leave
// the column NULL.
func seedGalgame(t *testing.T, id int, bid *int, catalogWorkID *int64, nameZhCN, introZhCN string) {
	t.Helper()
	require.NoError(t, testDB.Exec(`
		INSERT INTO galgame (id, vndb_id, bid, catalog_work_id, name_en_us, name_ja_jp, name_zh_cn, name_zh_tw,
		                     intro_en_us, intro_ja_jp, intro_zh_cn, intro_zh_tw, status, user_id)
		VALUES (?, '', ?, ?, 'EN name', 'JP name', ?, '', '', '', ?, '', 0, 1)
	`, id, bid, catalogWorkID, nameZhCN, introZhCN).Error)
	// A latest revision snapshot so the Approach-B patch has a target.
	require.NoError(t, testDB.Exec(`
		INSERT INTO galgame_revision (galgame_id, revision, action, user_id, snapshot, changed_fields, is_minor)
		VALUES (?, 1, 'created', 1, ?::jsonb, '[]'::jsonb, false)
	`, id, fmt.Sprintf(`{"name_zh_cn": %q, "intro_zh_cn": %q, "aliases": []}`, nameZhCN, introZhCN)).Error)
}

// seedAnchor inserts a catalog Bangumi EXACT anchor (source_id=3, entity_type=5,
// link_kind=0) linking catalog_work workID to Bangumi subject bid.
func seedAnchor(t *testing.T, workID int64, bid int) {
	t.Helper()
	require.NoError(t, testDB.Exec(`
		INSERT INTO catalog_external_ref (entity_type, entity_id, source_id, external_id, link_kind, matched_by)
		VALUES (5, ?, 3, ?, 0, 'import:test')
	`, workID, fmt.Sprintf("%d", bid)).Error)
}

func intPtr(v int) *int       { return &v }
func int64Ptr(v int64) *int64 { return &v }

func TestEnrichScenarios(t *testing.T) {
	clean(t)

	// #1 bid-only, empty fields → filled; aliases appended; meta lands.
	seedSubject(t, 100, 4, "ゲームA", "游戏A", "简介A", []string{"别名一", "别名二"}, 8.5, 42)
	seedGalgame(t, 1, intPtr(100), nil, "", "")
	// #2 only-fill-empty guard: non-empty (user-curated) → untouched; alias exists → deduped.
	seedSubject(t, 200, 4, "ゲームB", "游戏B", "简介B", []string{"已有别名"}, 7.0, 100)
	seedGalgame(t, 2, intPtr(200), nil, "用户已填的名字", "用户已填的简介")
	require.NoError(t, testDB.Exec(
		`INSERT INTO galgame_alias (galgame_id, name, created, updated) VALUES (2, '已有别名', now(), now())`).Error)
	// #3 wrong-type subject → fully gated (no fill, no meta).
	seedSubject(t, 300, 2, "アニメC", "动画C", "动画简介", nil, 9.9, 1)
	seedGalgame(t, 3, intPtr(300), nil, "", "")
	// #4 bid missing from the dump.
	seedGalgame(t, 4, intPtr(999999), nil, "", "")

	stats, conflicts, err := Run(testDB, testDB, Options{})
	require.NoError(t, err)
	// All four are bid-anchored, none carry a catalog anchor here.
	assert.Equal(t, 4, stats.CandidatesBID)
	assert.Zero(t, stats.CandidatesAnchorOnly)
	assert.Zero(t, stats.Conflicts)
	assert.Empty(t, conflicts)
	assert.Equal(t, 2, stats.Matched)
	assert.Equal(t, 1, stats.WrongType)
	assert.Equal(t, 1, stats.MissingInDump)
	assert.Equal(t, 1, stats.NameFilled)
	assert.Equal(t, 1, stats.IntroFilled)
	// #1 gets 别名一/别名二 (name_cn 游戏A was filled as the name, not an
	// alias); #2's 已有别名 deduped, but 游戏B lands as an alias (differs
	// from the main name and the user-curated fields).
	assert.Equal(t, 3, stats.AliasesAppended)
	assert.Equal(t, 2, stats.MetaUpserted)

	// Fill semantics.
	var g1 struct{ NameZhCN, IntroZhCN string }
	require.NoError(t, testDB.Raw(`SELECT name_zh_cn, intro_zh_cn FROM galgame WHERE id = 1`).Scan(&g1).Error)
	assert.Equal(t, "游戏A", g1.NameZhCN)
	assert.Equal(t, "简介A", g1.IntroZhCN)

	// User protection: non-empty values stay verbatim.
	var g2 struct{ NameZhCN, IntroZhCN string }
	require.NoError(t, testDB.Raw(`SELECT name_zh_cn, intro_zh_cn FROM galgame WHERE id = 2`).Scan(&g2).Error)
	assert.Equal(t, "用户已填的名字", g2.NameZhCN)
	assert.Equal(t, "用户已填的简介", g2.IntroZhCN)

	// Approach B: the latest revision snapshot matches the live row.
	var snapName string
	require.NoError(t, testDB.Raw(
		`SELECT snapshot->>'name_zh_cn' FROM galgame_revision WHERE galgame_id = 1 AND revision = 1`).Scan(&snapName).Error)
	assert.Equal(t, "游戏A", snapName)
	var snapAliases string
	require.NoError(t, testDB.Raw(
		`SELECT snapshot->'aliases' FROM galgame_revision WHERE galgame_id = 1`).Scan(&snapAliases).Error)
	assert.JSONEq(t, `["别名一","别名二"]`, snapAliases)

	// Wrong-type gate: nothing landed for #3.
	var g3Meta int64
	testDB.Table("galgame_bangumi_meta").Where("galgame_id = 3").Count(&g3Meta)
	assert.Zero(t, g3Meta)

	// Meta values.
	var meta model.GalgameBangumiMeta
	require.NoError(t, testDB.First(&meta, "galgame_id = ?", 1).Error)
	assert.Equal(t, 100, meta.BID)
	assert.Equal(t, 8.5, meta.Score)
	assert.Equal(t, 42, meta.Rank)
	assert.Equal(t, 10, meta.Total, "total = sum of score_details buckets")

	// Idempotency: a second run changes nothing.
	stats2, _, err := Run(testDB, testDB, Options{})
	require.NoError(t, err)
	assert.Zero(t, stats2.NameFilled)
	assert.Zero(t, stats2.IntroFilled)
	assert.Zero(t, stats2.AliasesAppended)
	assert.Zero(t, stats2.MetaUpserted)
	assert.Equal(t, 2, stats2.Unchanged)

	// Dry run reports would-be writes without writing.
	require.NoError(t, testDB.Exec(`UPDATE galgame SET name_zh_cn = '' WHERE id = 1`).Error)
	dry, _, err := Run(testDB, testDB, Options{DryRun: true})
	require.NoError(t, err)
	assert.Equal(t, 1, dry.NameFilled)
	var still string
	require.NoError(t, testDB.Raw(`SELECT name_zh_cn FROM galgame WHERE id = 1`).Scan(&still).Error)
	assert.Equal(t, "", still, "dry run must not write")
}

// TestCandidateSourceResolution exercises the widened candidate enumeration
// (step 43). Together with TestEnrichScenarios (bid-only #1 + only-fill-empty
// guard #2) it covers the five fixture cases: bid-only, anchor-only, both
// sources agree, conflict (zero action), and the fill guard.
func TestCandidateSourceResolution(t *testing.T) {
	clean(t)

	// anchor-only: no curated bid, but catalog_work 71 carries Bangumi bid 500.
	seedSubject(t, 500, 4, "ゲームAnchor", "锚游戏", "锚简介", []string{"锚别名"}, 8.0, 50)
	seedGalgame(t, 10, nil, int64Ptr(71), "", "")
	seedAnchor(t, 71, 500)

	// both sources agree: curated bid 600 AND anchor on work 72 = 600.
	seedSubject(t, 600, 4, "ゲームAgree", "一致游戏", "一致简介", nil, 7.5, 60)
	seedGalgame(t, 11, intPtr(600), int64Ptr(72), "", "")
	seedAnchor(t, 72, 600)

	// conflict: curated bid 700 but the anchor on work 73 says 999 → ZERO action.
	seedSubject(t, 700, 4, "ゲームCurated", "策展游戏", "策展简介", nil, 6.0, 70)
	seedSubject(t, 999, 4, "ゲームAnchorBad", "锚错游戏", "锚错简介", nil, 5.0, 80)
	seedGalgame(t, 12, intPtr(700), int64Ptr(73), "策展游戏名", "")
	seedAnchor(t, 73, 999)

	stats, conflicts, err := Run(testDB, testDB, Options{})
	require.NoError(t, err)

	// Provenance buckets: #11 bid wins, #10 anchor-only, #12 conflict.
	assert.Equal(t, 1, stats.CandidatesBID)
	assert.Equal(t, 1, stats.CandidatesAnchorOnly)
	assert.Equal(t, 1, stats.Conflicts)
	assert.Equal(t, 2, stats.Matched, "only the two non-conflict candidates are enriched")

	// Anchor-only game #10 is enriched via the catalog anchor bid (500).
	var g10 struct{ NameZhCN, IntroZhCN string }
	require.NoError(t, testDB.Raw(`SELECT name_zh_cn, intro_zh_cn FROM galgame WHERE id = 10`).Scan(&g10).Error)
	assert.Equal(t, "锚游戏", g10.NameZhCN)
	assert.Equal(t, "锚简介", g10.IntroZhCN)
	var meta10 model.GalgameBangumiMeta
	require.NoError(t, testDB.First(&meta10, "galgame_id = ?", 10).Error)
	assert.Equal(t, 500, meta10.BID, "anchor-only meta carries the anchor bid")

	// Both-agree game #11 is enriched via its curated bid (600).
	var name11 string
	require.NoError(t, testDB.Raw(`SELECT name_zh_cn FROM galgame WHERE id = 11`).Scan(&name11).Error)
	assert.Equal(t, "一致游戏", name11)

	// Conflict game #12: nothing touched — bid preserved, no fill, no meta.
	var bid12 int
	require.NoError(t, testDB.Raw(`SELECT bid FROM galgame WHERE id = 12`).Scan(&bid12).Error)
	assert.Equal(t, 700, bid12, "curated galgame.bid is never rewritten")
	var name12 string
	require.NoError(t, testDB.Raw(`SELECT name_zh_cn FROM galgame WHERE id = 12`).Scan(&name12).Error)
	assert.Equal(t, "策展游戏名", name12, "conflict game is left untouched")
	var meta12 int64
	testDB.Table("galgame_bangumi_meta").Where("galgame_id = 12").Count(&meta12)
	assert.Zero(t, meta12, "conflict game gets no meta row")

	// The conflict is exported for human adjudication (zero automatic action).
	require.Len(t, conflicts, 1)
	assert.Equal(t, 12, conflicts[0].GalgameID)
	assert.Equal(t, 700, conflicts[0].WikiBID)
	assert.Equal(t, 999, conflicts[0].AnchorBID)
	assert.Equal(t, "策展游戏名", conflicts[0].Name)
}

// parseForTest builds infobox_parsed exactly like the ingest transform
// (bangumiwiki.Parse + JSON marshal).
func parseForTest(infobox string) ([]byte, string) {
	box, err := bangumiwiki.Parse(infobox)
	if err != nil {
		return nil, err.Error()
	}
	b, merr := json.Marshal(box)
	if merr != nil {
		return nil, merr.Error()
	}
	return b, ""
}
