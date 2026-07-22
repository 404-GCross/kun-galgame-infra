package tagcanon

import (
	"context"
	"fmt"
	"os"
	"testing"

	"api/internal/platform/catalog/migrate"
	"api/internal/platform/catalog/model"
	"api/internal/platform/catalog/seed"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// Integration test against a real Postgres: the catalog Gold schema (migrate.Run
// + seeds) plus the galgame tag layer the vndb lane joins (galgame_tag ⋈
// galgame_tag_relation), which in prod is co-located with catalog. This
// exercises Run end-to-end across ALL THREE lanes (vndb via the join,
// bangumi/dlsite via catalog_work_tag): extraction, junk prefilter, ≥2-source
// grouping with the canonical-name pick + meta stamping, dry-run zero-write,
// apply fidelity, and second-pass idempotence. Run drives the DSN itself, so we
// capture it to exercise the real entry point.
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
	ensureGalgameTagLayer(db)
	testDB = db
	os.Exit(m.Run())
}

// ensureGalgameTagLayer provisions the galgame tag tables the vndb lane joins,
// mirroring the handler-test stubs (real shapes). CREATE ... IF NOT EXISTS is a
// no-op where the real tables already exist; on a bare catalog test DB it makes
// minimal stubs (galgame needs only id + user_id). galgame is created so the
// relation's galgame FK — present on the real table — is always satisfiable via
// a parent row.
func ensureGalgameTagLayer(db *gorm.DB) {
	db.Exec(`CREATE TABLE IF NOT EXISTS galgame (id bigint PRIMARY KEY, user_id bigint NOT NULL DEFAULT 0)`)
	db.Exec(`CREATE TABLE IF NOT EXISTS galgame_tag (
		id bigint PRIMARY KEY, name text NOT NULL UNIQUE, category text NOT NULL DEFAULT '', description text DEFAULT '')`)
	db.Exec(`CREATE TABLE IF NOT EXISTS galgame_tag_relation (
		galgame_id bigint NOT NULL, tag_id bigint NOT NULL, spoiler_level bigint DEFAULT 0,
		source varchar(16) DEFAULT '', PRIMARY KEY (galgame_id, tag_id))`)
}

func cleanTagcanon(t *testing.T) {
	t.Helper()
	for _, table := range []string{
		"catalog_tag_source_map", "catalog_tag", "catalog_work_tag",
		"galgame_tag_relation", "galgame_tag",
	} {
		require.NoError(t, testDB.Exec("TRUNCATE "+table+" RESTART IDENTITY CASCADE").Error)
	}
	// galgame is not truncated (it may hold other data / be large); drop just the
	// fixture parent rows (74100-block) so a re-run's mkGalgame does not collide.
	require.NoError(t, testDB.Exec(`DELETE FROM galgame WHERE id BETWEEN 74100 AND 74199`).Error)
}

func srcID(t *testing.T, key string) int16 {
	t.Helper()
	var id int16
	require.NoError(t, testDB.Raw(`SELECT id FROM catalog_source WHERE key = ?`, key).Scan(&id).Error)
	require.NotZero(t, id, "source %s seeded", key)
	return id
}

func mkWorkTag(t *testing.T, workID int64, name string, count int, source int16) {
	t.Helper()
	require.NoError(t, testDB.Create(&model.CatalogWorkTag{WorkID: workID, Name: name, Count: count, SourceID: source}).Error)
}

func mkGalgame(t *testing.T, id int64) {
	t.Helper()
	require.NoError(t, testDB.Exec(`INSERT INTO galgame (id, user_id) VALUES (?, 0)`, id).Error)
}

func mkGalgameTag(t *testing.T, id int64, name string) {
	t.Helper()
	require.NoError(t, testDB.Exec(`INSERT INTO galgame_tag (id, name, category) VALUES (?, ?, 'content')`, id, name).Error)
}

func mkGalgameRel(t *testing.T, galgameID, tagID int64) {
	t.Helper()
	require.NoError(t, testDB.Exec(`INSERT INTO galgame_tag_relation (galgame_id, tag_id, spoiler_level, source)
		VALUES (?, ?, 0, 'vndb')`, galgameID, tagID).Error)
}

func mkBodylessWork(t *testing.T, medium int16) int64 {
	t.Helper()
	w := model.CatalogWork{MediumID: medium, OLang: "ja", DisplayName: "t"}
	require.NoError(t, testDB.Create(&w).Error)
	return w.ID
}

// TestTagcanonRun drives the whole pipeline through the real Run entry point.
func TestTagcanonRun(t *testing.T) {
	cleanTagcanon(t)
	ctx := context.Background()
	vndb, bgm, dl := srcID(t, "vndb"), srcID(t, "bangumi"), srcID(t, "dlsite")
	var medium int16
	require.NoError(t, testDB.Raw(`SELECT id FROM catalog_medium WHERE key='galgame'`).Scan(&medium).Error)

	// vndb vocabulary via the galgame join: 奇幻 (usage 3) + 百合 (usage 1) +
	// 巨乳女主角 (usage 0, single-source → no group).
	for _, id := range []int64{74101, 74102, 74103} {
		mkGalgame(t, id)
	}
	mkGalgameTag(t, 74001, "奇幻")
	mkGalgameTag(t, 74002, "百合")
	mkGalgameTag(t, 74003, "巨乳女主角")
	mkGalgameRel(t, 74101, 74001)
	mkGalgameRel(t, 74102, 74001)
	mkGalgameRel(t, 74103, 74001)
	mkGalgameRel(t, 74101, 74002)

	// bangumi + dlsite vocabulary via catalog_work_tag.
	wB := mkBodylessWork(t, medium)
	wD := mkBodylessWork(t, medium)
	mkWorkTag(t, wB, "奇幻", 5, bgm)   // → 3-source group
	mkWorkTag(t, wB, "百合", 9, bgm)   // → 2-source group (vndb + bangumi)
	mkWorkTag(t, wB, "像素", 2, bgm)   // meta → 2-source group (bangumi + dlsite)
	mkWorkTag(t, wB, "2024", 1, bgm) // JUNK (number)
	mkWorkTag(t, wB, "抖m", 1, bgm)   // bangumi-only → no group
	mkWorkTag(t, wD, "奇幻", 40, dl)
	mkWorkTag(t, wD, "像素", 8, dl)
	mkWorkTag(t, wD, "手交", 3, dl) // dlsite-only → no group

	// --- dry run: decides, writes nothing.
	st, err := Run(ctx, Opts{DSN: testDSN})
	require.NoError(t, err)
	assert.Equal(t, 3, st.VndbNames, "奇幻 / 百合 / 巨乳女主角")
	assert.Equal(t, 1, st.BangumiJunk, "2024 filtered")
	require.NotEmpty(t, st.JunkSamples)
	assert.Equal(t, "number", st.JunkSamples[0].Reason)
	assert.Equal(t, 3, st.Groups, "奇幻 / 百合 / 像素")
	assert.Equal(t, 1, st.MetaGroups, "像素")
	assert.Equal(t, 1, st.TriSource, "奇幻 spans vndb+bangumi+dlsite")
	assert.Equal(t, 7, st.PlannedMaps, "奇幻 3 + 百合 2 + 像素 2")
	assert.Zero(t, st.TagsCreated+st.MapsCreated, "dry writes nothing")
	assert.EqualValues(t, 0, tagRowCount(t))

	// --- apply: writes the decided plan exactly.
	st, err = Run(ctx, Opts{DSN: testDSN, Apply: true})
	require.NoError(t, err)
	assert.Equal(t, 3, st.TagsCreated)
	assert.Equal(t, 7, st.MapsCreated)
	assert.Zero(t, st.TagsConflict+st.MapsConflict+st.Errors)
	assert.EqualValues(t, 3, tagRowCount(t))

	// Fidelity: 奇幻 = tier core, kind content, three map rows (vndb/bangumi/dlsite).
	var qf model.CatalogTag
	require.NoError(t, testDB.Where("name = ?", "奇幻").First(&qf).Error)
	assert.EqualValues(t, model.TagTierCore, qf.Tier)
	assert.EqualValues(t, model.TagKindContent, qf.Kind)
	var maps []model.CatalogTagSourceMap
	require.NoError(t, testDB.Where("tag_id = ?", qf.ID).Order("source_id").Find(&maps).Error)
	require.Len(t, maps, 3)
	assert.Equal(t, []int16{vndb, bgm, dl}, []int16{maps[0].SourceID, maps[1].SourceID, maps[2].SourceID})
	// 像素 stamped meta.
	var qm model.CatalogTag
	require.NoError(t, testDB.Where("name = ?", "像素").First(&qm).Error)
	assert.EqualValues(t, model.TagKindMeta, qm.Kind)

	// --- second apply: idempotent — zero writes, every row conflicts.
	st, err = Run(ctx, Opts{DSN: testDSN, Apply: true})
	require.NoError(t, err)
	assert.Zero(t, st.TagsCreated+st.MapsCreated+st.Errors, "second pass writes zero")
	assert.Equal(t, 3, st.TagsConflict)
	assert.Equal(t, 7, st.MapsConflict)
	assert.EqualValues(t, 3, tagRowCount(t), "row count unchanged")
}

// TestDSNRequired covers the refuse-to-guess DSN discipline.
func TestDSNRequired(t *testing.T) {
	_, err := Run(context.Background(), Opts{})
	require.Error(t, err)
}

func tagRowCount(t *testing.T) int64 {
	t.Helper()
	var n int64
	require.NoError(t, testDB.Raw("SELECT count(*) FROM catalog_tag").Scan(&n).Error)
	return n
}
