package entityintros

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"api/internal/platform/catalog/migrate"
	"api/internal/platform/catalog/model"
	"api/internal/platform/catalog/seed"
	srcb "api/internal/platform/catalog/srcbangumi"
	srcv "api/internal/platform/catalog/srcvndb"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// Integration test against a real Postgres: the catalog Gold schema
// (migrate.Run + registry seeds) and the src_bangumi / src_vndb Silver schemas
// co-located in ONE database, exactly as production lays them out (the
// single-DSN premise of this job). Run drives the DSN itself, so we capture it
// (not just the handle) to exercise the real entry point.
var (
	testDB  *gorm.DB
	testDSN string
)

func TestMain(m *testing.M) {
	testDSN = os.Getenv("TEST_DATABASE_DSN")
	if testDSN == "" {
		testDSN = "host=localhost port=5432 user=postgres password=postgres dbname=kun_catalog_test sslmode=disable"
	}
	db, err := gorm.Open(postgres.Open(testDSN), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		fmt.Fprintf(os.Stderr, "SKIP: cannot connect to test database: %v\n", err)
		os.Exit(0)
	}
	if err := migrate.Run(db); err != nil {
		fmt.Fprintf(os.Stderr, "SKIP: catalog migrate failed: %v\n", err)
		os.Exit(0)
	}
	if err := seed.Run(db); err != nil {
		fmt.Fprintf(os.Stderr, "SKIP: catalog seed failed: %v\n", err)
		os.Exit(0)
	}
	if err := srcb.EnsureSchema(db); err != nil {
		fmt.Fprintf(os.Stderr, "SKIP: src_bangumi schema failed: %v\n", err)
		os.Exit(0)
	}
	if err := srcv.EnsureSchema(db); err != nil {
		fmt.Fprintf(os.Stderr, "SKIP: src_vndb schema failed: %v\n", err)
		os.Exit(0)
	}
	testDB = db
	os.Exit(m.Run())
}

func clean(t *testing.T) {
	t.Helper()
	for _, table := range []string{
		"catalog_character_intro", "catalog_person_intro", "catalog_external_ref",
		"catalog_character", "catalog_person",
		"src_bangumi.character", "src_bangumi.person", "src_vndb.chars",
	} {
		require.NoError(t, testDB.Exec("TRUNCATE "+table+" RESTART IDENTITY CASCADE").Error)
	}
}

func mkCharacter(t *testing.T, name string) int64 {
	t.Helper()
	c := model.CatalogCharacter{DisplayName: name}
	require.NoError(t, testDB.Create(&c).Error)
	return c.ID
}

func mkPerson(t *testing.T, name string) int64 {
	t.Helper()
	p := model.CatalogPerson{DisplayName: name}
	require.NoError(t, testDB.Create(&p).Error)
	return p.ID
}

func mkAnchor(t *testing.T, entityType int16, entityID int64, source int16, externalID string, kind int16) {
	t.Helper()
	require.NoError(t, testDB.Create(&model.CatalogExternalRef{
		EntityType: entityType, EntityID: entityID, SourceID: source,
		ExternalID: externalID, LinkKind: kind, MatchedBy: "rule:test",
	}).Error)
}

func mkSrcCharacter(t *testing.T, id int64, summary string) {
	t.Helper()
	require.NoError(t, testDB.Create(&srcb.Character{
		ID: id, Role: 1, Name: fmt.Sprintf("char-%d", id), InfoboxRaw: "", ParseError: "",
		Summary: summary, ParserVersion: srcb.ParserVersion, IngestedAt: time.Now(),
	}).Error)
}

func mkSrcPerson(t *testing.T, id int64, summary string) {
	t.Helper()
	require.NoError(t, testDB.Create(&srcb.Person{
		ID: id, Name: fmt.Sprintf("person-%d", id), Type: 1, InfoboxRaw: "", ParseError: "",
		Summary: summary, ParserVersion: srcb.ParserVersion, IngestedAt: time.Now(),
	}).Error)
}

func mkSrcVNDBChar(t *testing.T, id, description string) {
	t.Helper()
	require.NoError(t, testDB.Create(&srcv.Char{
		ID: id, Image: "", Sex: "", Gender: "", Main: "", MainSpoil: 0,
		Description: description, IngestedAt: time.Now(),
	}).Error)
}

func charIntroCount(t *testing.T, where string, args ...any) int64 {
	t.Helper()
	var n int64
	require.NoError(t, testDB.Raw("SELECT count(*) FROM catalog_character_intro "+where, args...).Scan(&n).Error)
	return n
}

// TestFillMissingAllLanes exercises the whole pipeline through the real Run
// entry point: per-lane candidate selection (exact anchors, live entities),
// fill-missing-language decisions, spoiler-span removal, CRLF normalization,
// dry-run zero-write, apply, and second-pass idempotency.
func TestFillMissingAllLanes(t *testing.T) {
	clean(t)
	ctx := context.Background()
	reg, err := resolveRegistry(ctx, testDB)
	require.NoError(t, err)
	var userSrc int16
	require.NoError(t, testDB.Raw(`SELECT id FROM catalog_source WHERE key = 'user'`).Scan(&userSrc).Error)
	require.NotZero(t, userSrc)

	// Characters.
	chZh := mkCharacter(t, "bgm-zh")            // bgm zh summary → zh-Hans new
	chJaDup := mkCharacter(t, "bgm-ja-dup")     // bgm ja summary, ja intro exists → skip
	chJaCRLF := mkCharacter(t, "bgm-ja-crlf")   // bgm ja CRLF summary → ja new, LF-normalized
	chBlank := mkCharacter(t, "bgm-blank")      // whitespace summary → no_text
	chSpoiler := mkCharacter(t, "vndb-spoiler") // vndb description with spoiler span → en new, span gone
	chAllSpoil := mkCharacter(t, "vndb-all-spoiler")
	chBoth := mkCharacter(t, "both-sources") // bgm ja + vndb en → two rows, two langs
	chProbable := mkCharacter(t, "vndb-probable")
	chDeleted := mkCharacter(t, "deleted")

	mkSrcCharacter(t, 101, "少女是学园的学生会长。")
	mkSrcCharacter(t, 102, "ヒロインのひとり。")
	mkSrcCharacter(t, 103, "一行目です。\r\n二行目です。")
	mkSrcCharacter(t, 104, " \r\n ")
	mkSrcCharacter(t, 105, "主人公の幼馴染。")
	mkSrcVNDBChar(t, "c201", "A cheerful student.\n\n[spoiler]She is the final boss.[/spoiler]\n\nLoves cats.")
	mkSrcVNDBChar(t, "c202", "[spoiler]entirely spoiler[/spoiler]")
	mkSrcVNDBChar(t, "c203", "The protagonist's childhood friend.")
	mkSrcVNDBChar(t, "c204", "Probable-tier description.")
	mkSrcVNDBChar(t, "c205", "Deleted character description.")

	mkAnchor(t, model.EntityTypeCharacter, chZh, reg.bangumiSource, "101", model.LinkKindExact)
	mkAnchor(t, model.EntityTypeCharacter, chJaDup, reg.bangumiSource, "102", model.LinkKindExact)
	mkAnchor(t, model.EntityTypeCharacter, chJaCRLF, reg.bangumiSource, "103", model.LinkKindExact)
	mkAnchor(t, model.EntityTypeCharacter, chBlank, reg.bangumiSource, "104", model.LinkKindExact)
	mkAnchor(t, model.EntityTypeCharacter, chSpoiler, reg.vndbSource, "c201", model.LinkKindExact)
	mkAnchor(t, model.EntityTypeCharacter, chAllSpoil, reg.vndbSource, "c202", model.LinkKindExact)
	mkAnchor(t, model.EntityTypeCharacter, chBoth, reg.bangumiSource, "105", model.LinkKindExact)
	mkAnchor(t, model.EntityTypeCharacter, chBoth, reg.vndbSource, "c203", model.LinkKindExact)
	mkAnchor(t, model.EntityTypeCharacter, chProbable, reg.vndbSource, "c204", model.LinkKindProbable)
	mkAnchor(t, model.EntityTypeCharacter, chDeleted, reg.vndbSource, "c205", model.LinkKindExact)
	require.NoError(t, testDB.Delete(&model.CatalogCharacter{ID: chDeleted}).Error)

	// The pre-existing ja intro that makes chJaDup a fill-missing skip.
	require.NoError(t, testDB.Create(&model.CatalogCharacterIntro{
		CharacterID: chJaDup, Lang: "ja", Intro: "既にある日本語紹介。", SourceID: userSrc,
	}).Error)

	// Persons: anchors are empty in prod today, but the lane must work
	// end-to-end for when identity resolution lands them.
	pZh := mkPerson(t, "person-zh")
	pJaDup := mkPerson(t, "person-ja-dup")
	mkSrcPerson(t, 301, "中国出身的插画家。")
	mkSrcPerson(t, 302, "日本のイラストレーター。")
	mkAnchor(t, model.EntityTypePerson, pZh, reg.bangumiSource, "301", model.LinkKindExact)
	mkAnchor(t, model.EntityTypePerson, pJaDup, reg.bangumiSource, "302", model.LinkKindExact)
	require.NoError(t, testDB.Create(&model.CatalogPersonIntro{
		PersonID: pJaDup, Lang: "ja", Intro: "既存の人物紹介。", SourceID: userSrc,
	}).Error)

	// --- dry run: decides, writes nothing.
	st, err := Run(ctx, Opts{DSN: testDSN})
	require.NoError(t, err)
	assert.Equal(t, 5, st.CharBangumi.Candidates, "chZh chJaDup chJaCRLF chBlank chBoth")
	assert.Equal(t, 1, st.CharBangumi.ZhNew, "chZh")
	assert.Equal(t, 2, st.CharBangumi.JaNew, "chJaCRLF + chBoth")
	assert.Equal(t, 1, st.CharBangumi.SkipDupLang, "chJaDup")
	assert.Equal(t, 1, st.CharBangumi.NoText, "chBlank")

	assert.Equal(t, 3, st.CharVNDB.Candidates, "chSpoiler chAllSpoil chBoth; probable + deleted excluded in SQL")
	assert.Equal(t, 2, st.CharVNDB.EnNew, "chSpoiler + chBoth")
	assert.Equal(t, 1, st.CharVNDB.NoText, "chAllSpoil emptied by the spoiler strip")
	assert.Equal(t, 2, st.CharVNDB.SpoilerStripped, "chSpoiler + chAllSpoil had spans removed")

	assert.Equal(t, 2, st.PersonBangumi.Candidates)
	assert.Equal(t, 1, st.PersonBangumi.ZhNew, "pZh")
	assert.Equal(t, 1, st.PersonBangumi.SkipDupLang, "pJaDup")

	assert.Zero(t, st.CharBangumi.JaWritten+st.CharBangumi.ZhWritten+st.CharVNDB.EnWritten+
		st.PersonBangumi.JaWritten+st.PersonBangumi.ZhWritten, "dry run writes nothing")
	assert.EqualValues(t, 1, charIntroCount(t, ""), "only the fixture row exists")

	// --- apply.
	st, err = Run(ctx, Opts{DSN: testDSN, Apply: true})
	require.NoError(t, err)
	assert.Equal(t, 1, st.CharBangumi.ZhWritten)
	assert.Equal(t, 2, st.CharBangumi.JaWritten)
	assert.Equal(t, 2, st.CharVNDB.EnWritten)
	assert.Equal(t, 1, st.PersonBangumi.ZhWritten)
	assert.Zero(t, st.CharBangumi.Errors+st.CharVNDB.Errors+st.PersonBangumi.Errors)
	assert.Zero(t, st.CharBangumi.Conflict+st.CharVNDB.Conflict+st.PersonBangumi.Conflict)

	// Spoiler-strip proof: the span content never lands; surrounding text does.
	var spoilRow model.CatalogCharacterIntro
	require.NoError(t, testDB.Where("character_id = ? AND source_id = ?", chSpoiler, reg.vndbSource).First(&spoilRow).Error)
	assert.Equal(t, "en", spoilRow.Lang)
	assert.Equal(t, "A cheerful student.\n\nLoves cats.", spoilRow.Intro)
	assert.NotContains(t, spoilRow.Intro, "final boss")

	// CRLF → LF, otherwise verbatim.
	var crlfRow model.CatalogCharacterIntro
	require.NoError(t, testDB.Where("character_id = ?", chJaCRLF).First(&crlfRow).Error)
	assert.Equal(t, "ja", crlfRow.Lang)
	assert.Equal(t, "一行目です。\n二行目です。", crlfRow.Intro)

	// Two sources on one character coexist under different langs.
	assert.EqualValues(t, 2, charIntroCount(t, "WHERE character_id = ?", chBoth), "ja(bangumi) + en(vndb)")
	assert.EqualValues(t, 0, charIntroCount(t, "WHERE character_id = ?", chDeleted), "soft-deleted never materialises")
	assert.EqualValues(t, 0, charIntroCount(t, "WHERE character_id = ?", chProbable), "probable tier never materialises")

	var pRow model.CatalogPersonIntro
	require.NoError(t, testDB.Where("person_id = ?", pZh).First(&pRow).Error)
	assert.Equal(t, "zh-Hans", pRow.Lang)

	// --- second apply: fill-missing is idempotent — zero writes everywhere.
	st, err = Run(ctx, Opts{DSN: testDSN, Apply: true})
	require.NoError(t, err)
	assert.Zero(t, st.CharBangumi.JaWritten+st.CharBangumi.ZhWritten+st.CharVNDB.EnWritten+
		st.PersonBangumi.JaWritten+st.PersonBangumi.ZhWritten+
		st.CharBangumi.Errors+st.CharVNDB.Errors+st.PersonBangumi.Errors, "second pass writes zero")
	assert.Equal(t, 4, st.CharBangumi.SkipDupLang, "the three written langs now skip; chJaDup still dup")
	assert.EqualValues(t, 6, charIntroCount(t, ""), "row count unchanged: 1 fixture + 5 writes")
}

// TestOnlyLaneAndConflictBackstop covers --only lane selection, the invalid
// lane guard, and the ON CONFLICT backstop against a stale exist map.
func TestOnlyLaneAndConflictBackstop(t *testing.T) {
	clean(t)
	ctx := context.Background()
	reg, err := resolveRegistry(ctx, testDB)
	require.NoError(t, err)

	_, err = Run(ctx, Opts{DSN: testDSN, Only: "bogus"})
	require.Error(t, err, "unknown lane is refused")

	ch := mkCharacter(t, "only-vndb")
	mkSrcVNDBChar(t, "c900", "Only-lane description.")
	mkSrcCharacter(t, 901, "bgm 側の紹介。")
	mkAnchor(t, model.EntityTypeCharacter, ch, reg.vndbSource, "c900", model.LinkKindExact)
	mkAnchor(t, model.EntityTypeCharacter, ch, reg.bangumiSource, "901", model.LinkKindExact)

	st, err := Run(ctx, Opts{DSN: testDSN, Apply: true, Only: LaneCharVNDB})
	require.NoError(t, err)
	assert.Equal(t, 1, st.CharVNDB.EnWritten)
	assert.Zero(t, st.CharBangumi.Candidates, "bgm lane not run under --only char-vndb")
	assert.Zero(t, st.PersonBangumi.Candidates)
	assert.EqualValues(t, 1, charIntroCount(t, ""))

	// Backstop: a STALE (empty) exist map cannot duplicate a row — the
	// (character_id,lang,source_id) unique key refuses it.
	r := &laneRunner{db: testDB, sourceID: reg.vndbSource, exist: map[int64]map[string]bool{},
		stats: &LaneStats{}, vndb: true, insert: insertCharacterIntro}
	r.enrich(ctx, candidate{EntityID: ch, ExternalID: "c900", Text: "Only-lane description."}, true)
	assert.Equal(t, 1, r.stats.Conflict, "ON CONFLICT refuses the duplicate")
	assert.Equal(t, 0, r.stats.EnWritten)
	assert.EqualValues(t, 1, charIntroCount(t, ""), "still exactly one row")
}
