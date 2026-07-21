package dlsitegenres

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

// Integration test against a real Postgres: the catalog Gold schema
// (migrate.Run + registry seeds) and a minimal DLsite mirror fixture
// co-located in ONE database. The mirror fixture lives in its OWN schema
// (dlsitegenres_dl) reached via a search_path DSN — the shared test DB's
// public tables belong to other suites and must not be clobbered (the
// workratings discipline). Run drives the DSNs itself, so we capture them
// (not just the handle) to exercise the real entry point.
var (
	testDB    *gorm.DB
	testDSN   string
	dlTestDSN string
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
	// Minimal mirror shape (only the columns the loaders touch) in a DEDICATED
	// schema; the search_path DSN resolves the unqualified table names there.
	for _, ddl := range []string{
		`CREATE SCHEMA IF NOT EXISTS dlsitegenres_dl`,
		`CREATE TABLE IF NOT EXISTS dlsitegenres_dl.works (workno text PRIMARY KEY, product_json jsonb)`,
		`CREATE TABLE IF NOT EXISTS dlsitegenres_dl.genre_taxonomy (
			genre_id int NOT NULL, locale text NOT NULL, name text NOT NULL,
			PRIMARY KEY (genre_id, locale))`,
	} {
		if err := db.Exec(ddl).Error; err != nil {
			fmt.Fprintf(os.Stderr, "SKIP: mirror fixture failed: %v\n", err)
			os.Exit(0)
		}
	}
	dlTestDSN = testDSN + " options='-csearch_path=dlsitegenres_dl'"
	testDB = db
	os.Exit(m.Run())
}

func clean(t *testing.T) {
	t.Helper()
	for _, table := range []string{
		"catalog_work_tag", "catalog_external_ref", "catalog_release", "catalog_work",
		"dlsitegenres_dl.works", "dlsitegenres_dl.genre_taxonomy",
	} {
		require.NoError(t, testDB.Exec("TRUNCATE "+table+" RESTART IDENTITY CASCADE").Error)
	}
}

func mkWork(t *testing.T, medium int16, name string, site *string) int64 {
	t.Helper()
	w := model.CatalogWork{MediumID: medium, OLang: "ja", DisplayName: name, Site: site}
	require.NoError(t, testDB.Create(&w).Error)
	return w.ID
}

// mkReleaseAnchor attaches a RELEASE-level external ref (the DLsite anchor
// shape — worknos are SKU-natured and hang off catalog_release).
func mkReleaseAnchor(t *testing.T, workID int64, externalID string, source, kind int16) {
	t.Helper()
	rel := model.CatalogRelease{WorkID: workID, Kind: model.ReleaseKindDigital}
	require.NoError(t, testDB.Create(&rel).Error)
	require.NoError(t, testDB.Create(&model.CatalogExternalRef{
		EntityType: model.EntityTypeRelease, EntityID: rel.ID, SourceID: source,
		ExternalID: externalID, LinkKind: kind, MatchedBy: "rule:test",
	}).Error)
}

// mkMirrorWork writes one DLsite mirror row; genres == "" leaves product_json
// without a genres key (the real mirror's NULL shape).
func mkMirrorWork(t *testing.T, workno, genres string) {
	t.Helper()
	pj := `{}`
	if genres != "" {
		pj = `{"genres": ` + genres + `}`
	}
	require.NoError(t, testDB.Exec(
		`INSERT INTO dlsitegenres_dl.works (workno, product_json) VALUES (?, ?::jsonb)`,
		workno, pj).Error)
}

func mkTaxonomy(t *testing.T, genreID int, locale, name string) {
	t.Helper()
	require.NoError(t, testDB.Exec(
		`INSERT INTO dlsitegenres_dl.genre_taxonomy (genre_id, locale, name) VALUES (?, ?, ?)`,
		genreID, locale, name).Error)
}

func tagCount(t *testing.T, where string, args ...any) int64 {
	t.Helper()
	var n int64
	require.NoError(t, testDB.Raw("SELECT count(*) FROM catalog_work_tag "+where, args...).Scan(&n).Error)
	return n
}

// runOpts returns the two-DSN Opts for a test run.
func runOpts(apply bool) Opts {
	return Opts{DSN: testDSN, DlsiteDSN: dlTestDSN, Apply: apply}
}

// TestBackfillDlsiteGenres exercises the whole pipeline through the real Run
// entry point: candidate selection (exact release anchors only, bodyless only,
// galgame medium only), the name-resolution buckets (zh_CN taxonomy hit — with
// the rename auto-correction and the locale pin — vs the retired-id embedded-ja
// fallback vs the blank skip), same-name in-work dedupe, the defensive parse
// branches, count=0 fill semantics, coexistence with pre-existing bgm
// folksonomy rows under the read-face (count DESC, name) order, dry-run
// zero-write, apply value fidelity, and second-pass idempotency.
func TestBackfillDlsiteGenres(t *testing.T) {
	clean(t)
	ctx := context.Background()
	reg, err := resolveRegistry(ctx, testDB)
	require.NoError(t, err)

	claimed := "galgame_wiki"

	// Vocabulary: 226 carries a ja_JP row too — the loader must pin zh_CN.
	// 113's zh row is the rename-correction proof (works embed the OLD ja name
	// レイプ; the id resolves to the CURRENT official zh name). 300/301 both
	// resolve to one zh name — the in-work dedupe case.
	mkTaxonomy(t, 226, "zh_CN", "女教师")
	mkTaxonomy(t, 226, "ja_JP", "女教師")
	mkTaxonomy(t, 113, "zh_CN", "强X")
	mkTaxonomy(t, 300, "zh_CN", "重复名")
	mkTaxonomy(t, 301, "zh_CN", "重复名")

	// wFull: two zh hits (113 embedding the outdated ja name — corrected by id)
	// + one retired id falling back to its embedded ja name.
	wFull := mkWork(t, reg.galgameMedium, "genres-full", nil)
	mkReleaseAnchor(t, wFull, "RJ200001", reg.dlsiteSource, model.LinkKindExact)
	mkMirrorWork(t, "RJ200001",
		`[{"id":226,"name":"女教師"},{"id":113,"name":"レイプ"},{"id":9999,"name":"退役ジャンル"}]`)

	wDup := mkWork(t, reg.galgameMedium, "genres-dup", nil) // 300+301 → one 重复名 row
	mkReleaseAnchor(t, wDup, "RJ200002", reg.dlsiteSource, model.LinkKindExact)
	mkMirrorWork(t, "RJ200002", `[{"id":300,"name":"旧名A"},{"id":301,"name":"旧名B"}]`)

	wBlank := mkWork(t, reg.galgameMedium, "genres-blank", nil) // retired ids with blank/missing names
	mkReleaseAnchor(t, wBlank, "RJ200003", reg.dlsiteSource, model.LinkKindExact)
	mkMirrorWork(t, "RJ200003", `[{"id":8888,"name":"   "},{"id":8887}]`)

	wEmpty := mkWork(t, reg.galgameMedium, "genres-empty", nil) // genres [] → no_genres
	mkReleaseAnchor(t, wEmpty, "RJ200004", reg.dlsiteSource, model.LinkKindExact)
	mkMirrorWork(t, "RJ200004", `[]`)

	wNull := mkWork(t, reg.galgameMedium, "genres-null", nil) // no genres key → no_genres
	mkReleaseAnchor(t, wNull, "RJ200005", reg.dlsiteSource, model.LinkKindExact)
	mkMirrorWork(t, "RJ200005", "")

	wObject := mkWork(t, reg.galgameMedium, "genres-object", nil) // genres {} → not_array
	mkReleaseAnchor(t, wObject, "RJ200006", reg.dlsiteSource, model.LinkKindExact)
	mkMirrorWork(t, "RJ200006", `{"id":226,"name":"女教師"}`)

	wMissing := mkWork(t, reg.galgameMedium, "genres-missing", nil) // absent from mirror
	mkReleaseAnchor(t, wMissing, "RJ200007", reg.dlsiteSource, model.LinkKindExact)

	wClaimed := mkWork(t, reg.galgameMedium, "genres-claimed", &claimed) // claimed → excluded by SQL
	mkReleaseAnchor(t, wClaimed, "RJ200008", reg.dlsiteSource, model.LinkKindExact)
	mkMirrorWork(t, "RJ200008", `[{"id":226,"name":"女教師"}]`)

	wAsmr := mkWork(t, 5 /* asmr medium */, "genres-asmr", nil) // wrong medium → excluded (game-domain ruling)
	mkReleaseAnchor(t, wAsmr, "RJ200009", reg.dlsiteSource, model.LinkKindExact)
	mkMirrorWork(t, "RJ200009", `[{"id":226,"name":"女教師"}]`)

	wProbable := mkWork(t, reg.galgameMedium, "genres-probable", nil) // probable tier → excluded
	mkReleaseAnchor(t, wProbable, "RJ200010", reg.dlsiteSource, model.LinkKindProbable)
	mkMirrorWork(t, "RJ200010", `[{"id":226,"name":"女教師"}]`)

	// --- dry run: decides, writes nothing.
	st, err := Run(ctx, runOpts(false))
	require.NoError(t, err)
	assert.Equal(t, 4, st.TaxonomyRows, "zh_CN rows only — the ja_JP 226 row is pinned out")
	assert.Equal(t, 7, st.Candidates, "claimed + asmr + probable excluded in SQL")
	assert.Equal(t, 1, st.MissingMirror)
	assert.Equal(t, 2, st.NoGenres, "empty array + missing key")
	assert.Equal(t, 1, st.NotArray)
	assert.Equal(t, 4, st.ZhHit, "226 + 113 + 300 + 301")
	assert.Equal(t, 1, st.JaFallback, "retired 9999")
	assert.Equal(t, 2, st.NameBlank, "whitespace-only + missing embedded name")
	assert.Equal(t, 1, st.DupCollapsed, "301's 重复名 collapsed into 300's")
	assert.Equal(t, 4, st.Planned, "wFull 3 + wDup 1")
	assert.Equal(t, 4, st.DistinctNames)
	assert.Equal(t, 0, st.Refused, "no claimed work reaches the write path")
	assert.Zero(t, st.Written+st.Conflict+st.Errors)
	assert.EqualValues(t, 0, tagCount(t, ""), "dry run writes nothing")
	// Planned order keeps the mirror's element order; names resolve by id.
	require.Len(t, st.Samples, 4)
	assert.Equal(t, Sample{WorkID: wFull, Workno: "RJ200001", GenreID: 226, Name: "女教师", FromTaxonomy: true},
		st.Samples[0], "zh_CN taxonomy name wins over the embedded ja name AND the ja_JP taxonomy row")
	assert.Equal(t, Sample{WorkID: wFull, Workno: "RJ200001", GenreID: 113, Name: "强X", FromTaxonomy: true},
		st.Samples[1], "outdated embedded レイプ auto-corrected to the CURRENT official name by id")
	assert.Equal(t, Sample{WorkID: wFull, Workno: "RJ200001", GenreID: 9999, Name: "退役ジャンル", FromTaxonomy: false},
		st.Samples[2], "retired id falls back to the embedded ja name")
	require.Len(t, st.FallbackSamples, 1)
	assert.Equal(t, 9999, st.FallbackSamples[0].GenreID)

	// A pre-existing bgm folksonomy row on wFull (inserted after the dry-run
	// zero-write assertion): the dlsite genres must coexist with it, never
	// clobber it — distinct (work_id, name, source_id) space.
	require.NoError(t, testDB.Create(&model.CatalogWorkTag{
		WorkID: wFull, Name: "百合", Count: 24, SourceID: 1, /* bangumi seed id */
	}).Error)

	// --- apply: writes the decided plan exactly.
	st, err = Run(ctx, runOpts(true))
	require.NoError(t, err)
	assert.Equal(t, 4, st.Written)
	assert.Zero(t, st.Conflict+st.Errors)
	assert.EqualValues(t, 5, tagCount(t, ""), "4 dlsite rows + the pre-existing bgm row")

	// Value fidelity + coexistence under the read-face order (count DESC, name):
	// the voted bgm folksonomy row sorts first; the count=0 dlsite genre rows
	// follow, name-ordered.
	var rows []model.CatalogWorkTag
	require.NoError(t, testDB.Where("work_id = ?", wFull).Order(`count DESC, name`).Find(&rows).Error)
	require.Len(t, rows, 4)
	assert.Equal(t, "百合", rows[0].Name)
	assert.Equal(t, 24, rows[0].Count)
	for i, want := range []string{"女教师", "强X", "退役ジャンル"} {
		assert.Equal(t, want, rows[i+1].Name)
		assert.Equal(t, 0, rows[i+1].Count, "dlsite genres carry no votes")
		assert.Equal(t, reg.dlsiteSource, rows[i+1].SourceID)
	}
	assert.EqualValues(t, 0, tagCount(t, "WHERE name = ?", "レイプ"),
		"the outdated embedded name never lands")
	assert.EqualValues(t, 1, tagCount(t, "WHERE work_id = ?", wDup), "重复名 deduped to one row")
	assert.EqualValues(t, 0, tagCount(t, "WHERE work_id IN (?, ?, ?)", wClaimed, wAsmr, wProbable),
		"claimed / off-domain / probable works never materialise")

	// --- second apply: idempotent — zero writes, every planned row conflicts.
	st, err = Run(ctx, runOpts(true))
	require.NoError(t, err)
	assert.Zero(t, st.Written+st.Errors, "second pass writes zero")
	assert.Equal(t, 4, st.Conflict)
	assert.EqualValues(t, 5, tagCount(t, ""), "row count unchanged")

	// --- fill semantics (NOT step 62's upsert): a mirror refresh adding a NEW
	// genre back-fills only the new row; existing rows are never touched.
	require.NoError(t, testDB.Exec(
		`UPDATE dlsitegenres_dl.works
		SET product_json = jsonb_set(product_json, '{genres}',
			(product_json->'genres') || '[{"id":226,"name":"女教師"}]'::jsonb)
		WHERE workno = 'RJ200002'`).Error)
	st, err = Run(ctx, runOpts(true))
	require.NoError(t, err)
	assert.Equal(t, 1, st.Written, "only the newly-appeared genre lands")
	assert.Equal(t, 4, st.Conflict)
	assert.EqualValues(t, 2, tagCount(t, "WHERE work_id = ?", wDup))
}

// TestXORGuardAndDSNRequired covers the write-time XOR guard (the SQL filter
// excludes claimed works from candidates, so the guard is only reachable by
// driving the writer directly) and the refuse-to-guess DSN discipline.
func TestXORGuardAndDSNRequired(t *testing.T) {
	clean(t)
	ctx := context.Background()
	reg, err := resolveRegistry(ctx, testDB)
	require.NoError(t, err)

	claimed := "galgame_wiki"
	wClaimed := mkWork(t, reg.galgameMedium, "claimed-direct", &claimed)
	wBody := mkWork(t, reg.galgameMedium, "bodyless-direct", nil)

	// XOR: a claimed row is refused before any write.
	w := &writer{db: testDB, stats: &Stats{}}
	w.write(ctx, plannedRow{WorkID: wClaimed, Site: &claimed, SourceID: reg.dlsiteSource, Name: "女教师"}, true)
	assert.Equal(t, 1, w.stats.Refused)
	assert.Zero(t, w.stats.Written+w.stats.Conflict)
	assert.EqualValues(t, 0, tagCount(t, ""))

	// A bodyless row writes; a retry conflicts (the UNIQUE backstop) instead of
	// duplicating; the same name from ANOTHER source is a NEW row (the unique
	// key is (work_id, name, source_id) — sources coexist).
	w.write(ctx, plannedRow{WorkID: wBody, Site: nil, SourceID: reg.dlsiteSource, Name: "女教师"}, true)
	assert.Equal(t, 1, w.stats.Written)
	w.write(ctx, plannedRow{WorkID: wBody, Site: nil, SourceID: reg.dlsiteSource, Name: "女教师"}, true)
	assert.Equal(t, 1, w.stats.Conflict, "ON CONFLICT refuses the duplicate")
	w.write(ctx, plannedRow{WorkID: wBody, Site: nil, SourceID: 1 /* bangumi seed id */, Name: "女教师"}, true)
	assert.Equal(t, 2, w.stats.Written, "same work+name, different source → distinct row")
	assert.EqualValues(t, 2, tagCount(t, ""))

	// DSN discipline: both DSNs are required, never guessed.
	_, err = Run(ctx, Opts{DlsiteDSN: dlTestDSN})
	require.Error(t, err)
	_, err = Run(ctx, Opts{DSN: testDSN})
	require.Error(t, err)
}
