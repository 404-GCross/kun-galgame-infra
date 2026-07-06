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
	testDB = db
	os.Exit(m.Run())
}

func clean(t *testing.T) {
	t.Helper()
	for _, table := range []string{
		"galgame_bangumi_meta", "galgame_alias", "galgame_revision", "galgame",
		"src_bangumi.subject",
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

func seedGalgame(t *testing.T, id int, bid int, nameZhCN, introZhCN string) {
	t.Helper()
	require.NoError(t, testDB.Exec(`
		INSERT INTO galgame (id, vndb_id, bid, name_en_us, name_ja_jp, name_zh_cn, name_zh_tw,
		                     intro_en_us, intro_ja_jp, intro_zh_cn, intro_zh_tw, status, user_id)
		VALUES (?, '', ?, 'EN name', 'JP name', ?, '', '', '', ?, '', 0, 1)
	`, id, bid, nameZhCN, introZhCN).Error)
	// A latest revision snapshot so the Approach-B patch has a target.
	require.NoError(t, testDB.Exec(`
		INSERT INTO galgame_revision (galgame_id, revision, action, user_id, snapshot, changed_fields, is_minor)
		VALUES (?, 1, 'created', 1, ?::jsonb, '[]'::jsonb, false)
	`, id, fmt.Sprintf(`{"name_zh_cn": %q, "intro_zh_cn": %q, "aliases": []}`, nameZhCN, introZhCN)).Error)
}

func TestEnrichScenarios(t *testing.T) {
	clean(t)

	// #1 empty fields → filled; aliases appended; meta lands.
	seedSubject(t, 100, 4, "ゲームA", "游戏A", "简介A", []string{"别名一", "别名二"}, 8.5, 42)
	seedGalgame(t, 1, 100, "", "")
	// #2 non-empty values (user-curated) → untouched; alias exists → deduped.
	seedSubject(t, 200, 4, "ゲームB", "游戏B", "简介B", []string{"已有别名"}, 7.0, 100)
	seedGalgame(t, 2, 200, "用户已填的名字", "用户已填的简介")
	require.NoError(t, testDB.Exec(
		`INSERT INTO galgame_alias (galgame_id, name, created, updated) VALUES (2, '已有别名', now(), now())`).Error)
	// #3 wrong-type subject → fully gated (no fill, no meta).
	seedSubject(t, 300, 2, "アニメC", "动画C", "动画简介", nil, 9.9, 1)
	seedGalgame(t, 3, 300, "", "")
	// #4 bid missing from the dump.
	seedGalgame(t, 4, 999999, "", "")

	stats, err := Run(testDB, testDB, Options{})
	require.NoError(t, err)
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
	stats2, err := Run(testDB, testDB, Options{})
	require.NoError(t, err)
	assert.Zero(t, stats2.NameFilled)
	assert.Zero(t, stats2.IntroFilled)
	assert.Zero(t, stats2.AliasesAppended)
	assert.Zero(t, stats2.MetaUpserted)
	assert.Equal(t, 2, stats2.Unchanged)

	// Dry run reports would-be writes without writing.
	require.NoError(t, testDB.Exec(`UPDATE galgame SET name_zh_cn = '' WHERE id = 1`).Error)
	dry, err := Run(testDB, testDB, Options{DryRun: true})
	require.NoError(t, err)
	assert.Equal(t, 1, dry.NameFilled)
	var still string
	require.NoError(t, testDB.Raw(`SELECT name_zh_cn FROM galgame WHERE id = 1`).Scan(&still).Error)
	assert.Equal(t, "", still, "dry run must not write")
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
