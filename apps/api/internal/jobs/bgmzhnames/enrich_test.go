package bgmzhnames

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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/datatypes"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// Integration test against a real Postgres: the catalog Gold schema
// (migrate.Run + registry seeds) and the src_bangumi Silver schema co-located in
// ONE database, exactly as production lays them out (the single-DSN premise of
// this job). Run drives the DSN itself, so we capture it (not just the handle)
// to exercise the real entry point.
var (
	testDB  *gorm.DB
	testDSN string
)

// backdated is stamped on freshly created works so any TouchWorks bump is
// unambiguous (GORM writes now() on create).
var backdated = time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)

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
	testDB = db
	os.Exit(m.Run())
}

func clean(t *testing.T) {
	t.Helper()
	for _, table := range []string{
		"catalog_character_alias", "catalog_external_ref",
		"catalog_work_character", "catalog_work", "catalog_character",
		"src_bangumi.character",
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

func mkAnchor(t *testing.T, characterID int64, source int16, externalID string, kind int16) {
	t.Helper()
	require.NoError(t, testDB.Create(&model.CatalogExternalRef{
		EntityType: model.EntityTypeCharacter, EntityID: characterID,
		SourceID: source, ExternalID: externalID, LinkKind: kind, MatchedBy: "test",
	}).Error)
}

func mkBGMCharacter(t *testing.T, id int64, name, infobox string) {
	t.Helper()
	require.NoError(t, testDB.Create(&srcb.Character{
		ID: id, Role: 1, Name: name, InfoboxRaw: "", InfoboxParsed: datatypes.JSON(infobox),
		ParserVersion: "test", IngestedAt: time.Now(),
	}).Error)
}

func mkWork(t *testing.T, name string) int64 {
	t.Helper()
	w := model.CatalogWork{MediumID: 1, OLang: "ja", DisplayName: name}
	require.NoError(t, testDB.Create(&w).Error)
	require.NoError(t, testDB.Exec(`UPDATE catalog_work SET updated_at = ? WHERE id = ?`, backdated, w.ID).Error)
	return w.ID
}

func mkRosterEdge(t *testing.T, workID, characterID int64) {
	t.Helper()
	require.NoError(t, testDB.Create(&model.CatalogWorkCharacter{
		WorkID: workID, CharacterID: characterID,
		Kind: model.WorkCharacterKindMain, Spoiler: 0, MatchedBy: "import:test",
	}).Error)
}

func workUpdatedAt(t *testing.T, id int64) time.Time {
	t.Helper()
	var ts time.Time
	require.NoError(t, testDB.Raw(`SELECT updated_at FROM catalog_work WHERE id = ?`, id).Scan(&ts).Error)
	return ts
}

func aliases(t *testing.T, characterID int64) []model.CatalogCharacterAlias {
	t.Helper()
	var rows []model.CatalogCharacterAlias
	require.NoError(t, testDB.Where("character_id = ?", characterID).Order("id").Find(&rows).Error)
	return rows
}

func aliasCount(t *testing.T, where string, args ...any) int64 {
	t.Helper()
	var n int64
	q := testDB.Model(&model.CatalogCharacterAlias{})
	if where != "" {
		q = q.Where(where, args...)
	}
	require.NoError(t, q.Count(&n).Error)
	return n
}

func bangumiSource(t *testing.T) int16 {
	t.Helper()
	id, err := resolveBangumiSource(context.Background(), testDB)
	require.NoError(t, err)
	return id
}

// TestRun drives the whole wave end to end: the infobox guard, the Chinese
// sorting, the uq absorption of a name the character already has, the
// never-steal-an-existing-primary rule, the non-anchored/probable exclusions,
// the host-work touch, and second-pass idempotency.
func TestRun(t *testing.T) {
	clean(t)
	ctx := context.Background()
	src := bangumiSource(t)

	chLelouch := mkCharacter(t, "ルルーシュ・ランペルージ")
	chGuard := mkCharacter(t, "scalar-fields")
	chNoSupply := mkCharacter(t, "ja-only")
	chDup := mkCharacter(t, "already-named")
	chHasPrimary := mkCharacter(t, "human-primary")
	chProbable := mkCharacter(t, "probable-anchor")
	chUnanchored := mkCharacter(t, "no-anchor")

	for ch, bgm := range map[int64]int64{
		chLelouch: 1, chGuard: 2, chNoSupply: 3, chDup: 4, chHasPrimary: 5, chProbable: 6,
	} {
		mkBGMCharacter(t, bgm, fmt.Sprintf("bgm-%d", bgm), `{"Fields":[{"Key":"简体中文名","Value":"占位","Items":null}]}`)
		kind := model.LinkKindExact
		if ch == chProbable {
			kind = model.LinkKindProbable
		}
		mkAnchor(t, ch, src, fmt.Sprintf("%d", bgm), kind)
	}
	// chUnanchored deliberately gets no external_ref at all.
	_ = chUnanchored

	// The real Lelouch infobox: main name + one Chinese-declaring alias item,
	// surrounded by English/Japanese/untagged siblings that must not be taken.
	require.NoError(t, testDB.Exec(`UPDATE src_bangumi.character SET infobox_parsed = ?::jsonb WHERE id = 1`,
		`{"Type":"Crt","Fields":[
			{"Key":"简体中文名","Value":"鲁路修·兰佩路基","Items":null},
			{"Key":"别名","Value":"","Items":[
				{"Key":"","Value":"鲁鲁修"},
				{"Key":"英文名","Value":"Lelouch Lamperouge"},
				{"Key":"第二中文名","Value":"鲁路修·冯·布里塔尼亚"},
				{"Key":"日文名","Value":"ルルーシュ・ヴィ・ブリタニア"}]}]}`).Error)
	// A SCALAR Fields — the dirty value the guard exists for.
	require.NoError(t, testDB.Exec(`UPDATE src_bangumi.character SET infobox_parsed = ?::jsonb WHERE id = 2`,
		`{"Fields":"简体中文名"}`).Error)
	// Japanese-only names: parseable, but nothing this wave may take.
	require.NoError(t, testDB.Exec(`UPDATE src_bangumi.character SET infobox_parsed = ?::jsonb WHERE id = 3`,
		`{"Fields":[{"Key":"别名","Value":"","Items":[{"Key":"日文名","Value":"涼宮ハルヒ"}]}]}`).Error)
	require.NoError(t, testDB.Exec(`UPDATE src_bangumi.character SET infobox_parsed = ?::jsonb WHERE id = 4`,
		`{"Fields":[{"Key":"简体中文名","Value":"零","Items":null},
			{"Key":"别名","Value":"","Items":[{"Key":"第二中文名","Value":"零号机"}]}]}`).Error)
	require.NoError(t, testDB.Exec(`UPDATE src_bangumi.character SET infobox_parsed = ?::jsonb WHERE id = 5`,
		`{"Fields":[{"Key":"简体中文名","Value":"绫波丽","Items":null}]}`).Error)

	// chDup already carries the main name (uq absorption), chHasPrimary already
	// has a HUMAN zh-Hans primary under a different name (never stolen).
	require.NoError(t, testDB.Create(&model.CatalogCharacterAlias{
		CharacterID: chDup, Name: "零", Lang: LangZhHans, Kind: model.AliasKindTranslation,
	}).Error)
	require.NoError(t, testDB.Create(&model.CatalogCharacterAlias{
		CharacterID: chHasPrimary, Name: "凌波丽", Lang: LangZhHans,
		Kind: model.AliasKindTranslation, IsPrimaryForLocale: true,
	}).Error)
	// A ja alias with the SAME text the wave will write: different language, so
	// it must not absorb the zh-Hans row.
	require.NoError(t, testDB.Create(&model.CatalogCharacterAlias{
		CharacterID: chLelouch, Name: "鲁路修·兰佩路基", Lang: "ja", Kind: model.AliasKindTranslation,
	}).Error)

	hostA := mkWork(t, "host-a")
	hostB := mkWork(t, "host-b")
	hostSkipped := mkWork(t, "host-skipped")
	mkRosterEdge(t, hostA, chLelouch)
	mkRosterEdge(t, hostB, chLelouch)
	mkRosterEdge(t, hostSkipped, chNoSupply)

	fixtures := aliasCount(t, "")
	require.EqualValues(t, 3, fixtures)

	// --- dry run: decides, writes nothing, touches nothing.
	st, err := Run(ctx, Opts{DSN: testDSN})
	require.NoError(t, err)
	assert.Equal(t, 5, st.Anchored, "the probable anchor and the unanchored character are out of the universe")
	assert.Equal(t, 1, st.SkippedGuard, "the scalar Fields row")
	assert.Equal(t, 1, st.NoSupply, "the Japanese-only character")
	assert.Equal(t, 3, st.Candidates, "lelouch + dup + has-primary")
	assert.Equal(t, 5, st.Names, "2 lelouch + 2 dup + 1 has-primary")
	assert.Equal(t, 4, st.WouldInsert)
	assert.Equal(t, 1, st.SkippedDup, "chDup already carries 零")
	assert.Zero(t, st.Inserted)
	assert.Zero(t, st.Touched)
	assert.EqualValues(t, fixtures, aliasCount(t, ""), "a dry run writes nothing")
	assert.Equal(t, backdated.UTC(), workUpdatedAt(t, hostA).UTC(), "a dry run moves no watermark")

	// --- apply.
	st, err = Run(ctx, Opts{DSN: testDSN, Apply: true})
	require.NoError(t, err)
	assert.Equal(t, 4, st.Inserted)
	assert.Equal(t, 2, st.PrimarySet, "chLelouch + chDup; chHasPrimary already had one and keeps it")
	assert.Equal(t, 2, st.Touched, "hostA + hostB; hostSkipped's character gained nothing")
	assert.Zero(t, st.Errors+st.Conflict)

	zh := func(rows []model.CatalogCharacterAlias) []model.CatalogCharacterAlias {
		var out []model.CatalogCharacterAlias
		for _, r := range rows {
			if r.Lang == LangZhHans {
				out = append(out, r)
			}
		}
		return out
	}
	lel := zh(aliases(t, chLelouch))
	require.Len(t, lel, 2)
	assert.Equal(t, "鲁路修·兰佩路基", lel[0].Name)
	assert.True(t, lel[0].IsPrimaryForLocale, "the main 简体中文名 claims the free primary")
	assert.Equal(t, model.AliasKindTranslation, lel[0].Kind)
	assert.Nil(t, lel[0].Latin, "latin is never written by this wave")
	assert.Equal(t, "鲁路修·冯·布里塔尼亚", lel[1].Name)
	assert.False(t, lel[1].IsPrimaryForLocale, "only one row per character claims the primary")
	assert.EqualValues(t, 1, aliasCount(t, "character_id = ? AND lang = ?", chLelouch, "ja"),
		"the same text in another language is a separate row, untouched")

	dup := zh(aliases(t, chDup))
	require.Len(t, dup, 2, "零 was absorbed by the unique key; only 零号机 is new")
	assert.False(t, dup[0].IsPrimaryForLocale, "the pre-existing row is not rewritten")
	assert.Equal(t, "零号机", dup[1].Name)
	assert.True(t, dup[1].IsPrimaryForLocale, "chDup had no primary, so its one new row claims it")

	hp := zh(aliases(t, chHasPrimary))
	require.Len(t, hp, 2)
	assert.Equal(t, "凌波丽", hp[0].Name)
	assert.True(t, hp[0].IsPrimaryForLocale, "the human primary survives")
	assert.Equal(t, "绫波丽", hp[1].Name)
	assert.False(t, hp[1].IsPrimaryForLocale, "a new name never steals an existing primary")

	assert.EqualValues(t, 0, aliasCount(t, "character_id IN ?", []int64{chProbable, chUnanchored, chNoSupply, chGuard}),
		"non-exact-anchored and unsupplied characters get zero writes")
	assert.EqualValues(t, 0, aliasCount(t, "lang <> ? AND lang <> ?", LangZhHans, "ja"),
		"every written row is zh-Hans")
	assert.EqualValues(t, 0, aliasCount(t, "lang = ? AND latin IS NOT NULL", LangZhHans))

	bumpedA, bumpedB := workUpdatedAt(t, hostA), workUpdatedAt(t, hostB)
	assert.True(t, bumpedA.After(backdated))
	assert.True(t, bumpedB.After(backdated))
	assert.Equal(t, backdated.UTC(), workUpdatedAt(t, hostSkipped).UTC(), "a character that gained nothing bumps nothing")

	// --- second apply: zero writes, zero touches, watermarks frozen.
	st, err = Run(ctx, Opts{DSN: testDSN, Apply: true})
	require.NoError(t, err)
	assert.Zero(t, st.Inserted, "second pass writes zero")
	assert.Zero(t, st.WouldInsert, "and decides zero, so the skip is the preload, not the backstop")
	assert.Zero(t, st.Touched, "second pass touches zero")
	assert.Zero(t, st.Errors+st.Conflict)
	assert.Equal(t, 5, st.SkippedDup, "every projected name is now present")
	assert.EqualValues(t, fixtures+4, aliasCount(t, ""))
	assert.Equal(t, bumpedA.UTC(), workUpdatedAt(t, hostA).UTC(), "watermark does not drift on a re-run")
	assert.Equal(t, bumpedB.UTC(), workUpdatedAt(t, hostB).UTC())
}

// TestRunRequiresDSN pins the "never guess a DSN" discipline.
func TestRunRequiresDSN(t *testing.T) {
	_, err := Run(context.Background(), Opts{})
	require.Error(t, err)
}
