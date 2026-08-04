package getchuintros

import (
	"context"
	"fmt"
	"os"
	"testing"

	"api/internal/jobs/workpop"
	"api/internal/platform/catalog/migrate"
	"api/internal/platform/catalog/model"
	"api/internal/platform/catalog/seed"
	"api/internal/testsupport/dbtest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// Integration test against a real Postgres: the catalog Gold schema plus a
// stand-in for the crawler's staging `items` table. In production those are two
// databases; here one DSN plays both parts, which is faithful enough because
// the job never joins across them — it reads the staging side into a map.
var (
	testDB  *gorm.DB
	testDSN string
)

// TestMain gates the DB-backed tests PER TEST (dbtest.Skip) rather than exiting
// the package: pick_test.go holds pure functions, and a package-level exit
// would report them as `ok` while running none of them.
func TestMain(m *testing.M) {
	var ok bool
	testDSN, ok = dbtest.DSN()
	if !ok {
		fmt.Fprintln(os.Stderr, "SKIP: no TEST_DATABASE_DSN — DB-backed getchuintros tests will skip individually")
		os.Exit(m.Run())
	}
	db, err := gorm.Open(postgres.Open(testDSN), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		fmt.Fprintln(os.Stderr, "FAIL: cannot connect to the assigned test database")
		os.Exit(1)
	}
	if err := migrate.Run(db); err != nil {
		fmt.Fprintf(os.Stderr, "FAIL: catalog migrate failed: %v\n", err)
		os.Exit(1)
	}
	if err := seed.Run(db); err != nil {
		fmt.Fprintf(os.Stderr, "FAIL: catalog seed failed: %v\n", err)
		os.Exit(1)
	}
	// The staging stand-in. Only the two columns the job reads matter; the real
	// table (kun-getchu-api) has eighteen more.
	if err := db.Exec(`CREATE TABLE IF NOT EXISTS items (getchu_id text PRIMARY KEY, story text)`).Error; err != nil {
		fmt.Fprintf(os.Stderr, "FAIL: staging items table: %v\n", err)
		os.Exit(1)
	}
	testDB = db
	os.Exit(m.Run())
}

// --- fixtures -------------------------------------------------------------

const getchuSource = int16(17)

func clean(t *testing.T) {
	t.Helper()
	if testDB == nil {
		dbtest.Skip(t)
	}
	// catalog_external_ref carries a POLYMORPHIC entity_id, so it has no foreign
	// key to catalog_work and a CASCADE truncate of works leaves its rows
	// behind. It is cleaned explicitly, without RESTART IDENTITY — that sequence
	// is shared with every other package running against this database.
	for _, table := range []string{"catalog_external_ref", "catalog_release", "items"} {
		require.NoError(t, testDB.Exec("TRUNCATE "+table+" CASCADE").Error)
	}
	for _, table := range []string{"catalog_work_intro", "catalog_work"} {
		require.NoError(t, testDB.Exec("TRUNCATE "+table+" RESTART IDENTITY CASCADE").Error)
	}
}

var nextProductWorkID int64 = 970000

// mkWork builds a work in the requested population. Published needs BOTH a site
// and a product_work_id — model.ClaimStateKey will not call a row live without
// the second, and (site, product_work_id) is unique, so each gets a fresh id.
func mkWork(t *testing.T, name string, pop workpop.Population) int64 {
	t.Helper()
	w := model.CatalogWork{MediumID: 1, OLang: "ja", DisplayName: name}
	if pop == workpop.Claimed || pop == workpop.Published {
		site := "kungal"
		w.Site = &site
	}
	if pop == workpop.Published {
		nextProductWorkID++
		pid := nextProductWorkID
		w.ProductWorkID = &pid
	}
	require.NoError(t, testDB.Create(&w).Error)
	return w.ID
}

// mkAnchoredRelease gives a work a release carrying an EXACT getchu ref.
func mkAnchoredRelease(t *testing.T, workID int64, getchuID string) {
	t.Helper()
	rel := model.CatalogRelease{WorkID: workID, Kind: 0}
	require.NoError(t, testDB.Create(&rel).Error)
	require.NoError(t, testDB.Exec(`
		INSERT INTO catalog_external_ref (entity_type, entity_id, source_id, external_id, link_kind)
		VALUES (?,?,?,?,?)`,
		model.EntityTypeRelease, rel.ID, getchuSource, getchuID, model.LinkKindExact).Error)
}

func mkStagingItem(t *testing.T, getchuID, story string) {
	t.Helper()
	require.NoError(t, testDB.Exec(
		`INSERT INTO items (getchu_id, story) VALUES (?,?)`, getchuID, story).Error)
}

func mkIntro(t *testing.T, workID int64, lang string, source int16) {
	t.Helper()
	require.NoError(t, testDB.Create(&model.CatalogWorkIntro{
		WorkID: workID, Lang: lang, Intro: "already here", SourceID: source}).Error)
}

func run(t *testing.T, apply bool, pop workpop.Population) *Stats {
	t.Helper()
	st, err := Run(context.Background(), Opts{
		DSN: testDSN, GetchuDSN: testDSN, Apply: apply, Population: pop})
	require.NoError(t, err)
	return st
}

func introOf(t *testing.T, workID int64, lang string) (string, int16, bool) {
	t.Helper()
	var row struct {
		Intro    string `gorm:"column:intro"`
		SourceID int16  `gorm:"column:source_id"`
	}
	res := testDB.Raw(`SELECT intro, source_id FROM catalog_work_intro WHERE work_id = ? AND lang = ?`,
		workID, lang).Scan(&row)
	require.NoError(t, res.Error)
	return row.Intro, row.SourceID, res.RowsAffected > 0
}

// --- tests ----------------------------------------------------------------

func TestFillsMissingJapanese(t *testing.T) {
	clean(t)
	work := mkWork(t, "空の作品", workpop.Published)
	mkAnchoredRelease(t, work, "1001")
	mkStagingItem(t, "1001", "彼が目を覚ますと、そこは肌色の世界だった。")

	dry := run(t, false, workpop.Published)
	assert.Equal(t, 1, dry.Planned)
	assert.Equal(t, 0, dry.Written, "a dry run decides but writes nothing")
	_, _, found := introOf(t, work, "ja")
	assert.False(t, found)

	st := run(t, true, workpop.Published)
	assert.Equal(t, 1, st.Written)
	intro, source, found := introOf(t, work, "ja")
	require.True(t, found)
	assert.Equal(t, "彼が目を覚ますと、そこは肌色の世界だった。", intro)
	assert.Equal(t, getchuSource, source)
}

// Fill-missing is across ALL sources, not just this one: a work that already
// reads in Japanese from Bangumi or DLsite must not gain a second Japanese
// intro, because the read face would surface both.
func TestSkipsWorkThatAlreadyReadsInJapanese(t *testing.T) {
	clean(t)
	work := mkWork(t, "既に日本語あり", workpop.Published)
	mkAnchoredRelease(t, work, "1002")
	mkStagingItem(t, "1002", "getchu の紹介文")
	mkIntro(t, work, "ja", 3) // bangumi

	st := run(t, true, workpop.Published)
	assert.Equal(t, 1, st.SkipHasJa)
	assert.Equal(t, 0, st.Written)
	intro, source, _ := introOf(t, work, "ja")
	assert.Equal(t, "already here", intro, "the incumbent text is untouched")
	assert.Equal(t, int16(3), source)
}

// An existing intro in ANOTHER language is not a reason to skip — filling the
// Japanese gap is the point, and it is what feeds the nightly ja→zh lane.
func TestChineseIntroDoesNotBlockTheJapaneseFill(t *testing.T) {
	clean(t)
	work := mkWork(t, "中文あり", workpop.Published)
	mkAnchoredRelease(t, work, "1003")
	mkStagingItem(t, "1003", "日本語の紹介")
	mkIntro(t, work, "zh-Hans", 3)

	st := run(t, true, workpop.Published)
	assert.Equal(t, 1, st.Written)
	_, _, found := introOf(t, work, "ja")
	assert.True(t, found)
}

// A second --apply must write nothing. Fill-missing is self-idempotent (the row
// it wrote makes the language present), and the ON CONFLICT is only a backstop.
func TestSecondApplyIsAZeroWriteNoOp(t *testing.T) {
	clean(t)
	work := mkWork(t, "二度目", workpop.Published)
	mkAnchoredRelease(t, work, "1004")
	mkStagingItem(t, "1004", "本文")

	first := run(t, true, workpop.Published)
	require.Equal(t, 1, first.Written)

	second := run(t, true, workpop.Published)
	assert.Equal(t, 0, second.Written)
	assert.Equal(t, 0, second.Conflict, "the preload skips before the write is attempted")
	assert.Equal(t, 1, second.SkipHasJa)
}

// The published population is NARROWER than claimed, which is narrower than
// all. Getting this wrong would spend the lane on the draft sea.
func TestPopulationNarrows(t *testing.T) {
	clean(t)
	for i, pop := range []workpop.Population{workpop.Bodyless, workpop.Claimed, workpop.Published} {
		id := mkWork(t, fmt.Sprintf("w%d", i), pop)
		gid := fmt.Sprintf("200%d", i)
		mkAnchoredRelease(t, id, gid)
		mkStagingItem(t, gid, "本文")
	}

	assert.Equal(t, 1, run(t, false, workpop.Published).Planned)
	assert.Equal(t, 2, run(t, false, workpop.Claimed).Planned, "published works are claimed too")
	assert.Equal(t, 3, run(t, false, workpop.All).Planned)
}

// An anchored work whose Getchu page was never crawled, or carries no story
// block, is reported — not silently dropped from the denominator.
func TestUnreachableWorkIsCountedNotHidden(t *testing.T) {
	clean(t)
	work := mkWork(t, "story なし", workpop.Published)
	mkAnchoredRelease(t, work, "3001")
	mkStagingItem(t, "3001", "") // fetched, but Getchu had no story block

	st := run(t, true, workpop.Published)
	assert.Equal(t, 1, st.Works)
	assert.Equal(t, 1, st.NoStory)
	assert.Equal(t, 0, st.Written)
}

// Only EXACT anchors are read. A probable ref sits in the confirm bucket and
// asserting identity from it is exactly what this lane must not do.
func TestProbableAnchorIsNotRead(t *testing.T) {
	clean(t)
	work := mkWork(t, "probable のみ", workpop.Published)
	rel := model.CatalogRelease{WorkID: work, Kind: 0}
	require.NoError(t, testDB.Create(&rel).Error)
	require.NoError(t, testDB.Exec(`
		INSERT INTO catalog_external_ref (entity_type, entity_id, source_id, external_id, link_kind)
		VALUES (?,?,?,?,?)`,
		model.EntityTypeRelease, rel.ID, getchuSource, "4001", model.LinkKindProbable).Error)
	mkStagingItem(t, "4001", "本文")

	st := run(t, true, workpop.Published)
	assert.Equal(t, 0, st.Works)
	assert.Equal(t, 0, st.Written)
}

// Writing an intro bumps the work's updated_at so the public changes feed
// learns it is worth re-pulling; a skip must not.
func TestWrittenWorkIsTouchedAndSkippedWorkIsNot(t *testing.T) {
	clean(t)
	filled := mkWork(t, "書かれる", workpop.Published)
	mkAnchoredRelease(t, filled, "5001")
	mkStagingItem(t, "5001", "本文")

	skipped := mkWork(t, "飛ばされる", workpop.Published)
	mkAnchoredRelease(t, skipped, "5002")
	mkStagingItem(t, "5002", "本文")
	mkIntro(t, skipped, "ja", 3)

	var before []struct {
		ID        int64 `gorm:"column:id"`
		UpdatedAt any   `gorm:"column:updated_at"`
	}
	require.NoError(t, testDB.Raw(`SELECT id, updated_at FROM catalog_work ORDER BY id`).Scan(&before).Error)

	require.Equal(t, 1, run(t, true, workpop.Published).Written)

	var after []struct {
		ID        int64 `gorm:"column:id"`
		UpdatedAt any   `gorm:"column:updated_at"`
	}
	require.NoError(t, testDB.Raw(`SELECT id, updated_at FROM catalog_work ORDER BY id`).Scan(&after).Error)
	require.Len(t, after, 2)
	assert.NotEqual(t, before[0].UpdatedAt, after[0].UpdatedAt, "the filled work moved")
	assert.Equal(t, before[1].UpdatedAt, after[1].UpdatedAt, "the skipped work did not")
}

// Both DSNs are required. A bare invocation must not be able to guess either —
// this job reads a staging database and writes the live catalog.
func TestBothDSNsAreRequired(t *testing.T) {
	_, err := Run(context.Background(), Opts{GetchuDSN: "x"})
	require.Error(t, err)
	_, err = Run(context.Background(), Opts{DSN: "x"})
	require.Error(t, err)
}
