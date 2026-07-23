package worktags

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
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// Integration test against a real Postgres: the catalog Gold schema
// (migrate.Run + registry seeds) and the src_bangumi Silver schema co-located
// in ONE database — the exact single-DSN shape the backfill runs against. Run
// drives the DSN itself, so we capture it (not just the handle) to exercise
// the real entry point.
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
	testDB = db
	os.Exit(m.Run())
}

func clean(t *testing.T) {
	t.Helper()
	for _, table := range []string{
		"catalog_work_tag", "catalog_external_ref", "catalog_work", "src_bangumi.subject",
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

func mkAnchor(t *testing.T, workID int64, externalID string, source, kind int16, matchedBy string) {
	t.Helper()
	require.NoError(t, testDB.Create(&model.CatalogExternalRef{
		EntityType: model.EntityTypeWork, EntityID: workID, SourceID: source,
		ExternalID: externalID, LinkKind: kind, MatchedBy: matchedBy,
	}).Error)
}

// mkSubject inserts a src_bangumi.subject fixture; tags == "" leaves the
// column NULL (datatypes.JSON zero value inserts NULL).
func mkSubject(t *testing.T, id int64, tags string) {
	t.Helper()
	sub := srcb.Subject{
		ID: id, Type: 4, Name: fmt.Sprintf("subject-%d", id), NameCN: "",
		InfoboxRaw: "", ParseError: "", Summary: "", Date: "",
		ParserVersion: srcb.ParserVersion, IngestedAt: time.Now(),
	}
	if tags != "" {
		sub.Tags = []byte(tags)
	}
	require.NoError(t, testDB.Create(&sub).Error)
}

func tagCount(t *testing.T, where string, args ...any) int64 {
	t.Helper()
	var n int64
	require.NoError(t, testDB.Raw("SELECT count(*) FROM catalog_work_tag "+where, args...).Scan(&n).Error)
	return n
}

// TestBackfillWorkTags exercises the whole pipeline through the real Run entry
// point: candidate selection (exact anchors only, bodyless only), the
// defensive parse branches (NULL / empty / non-array tags, blank or missing
// names, in-subject duplicate collapse to the highest count), verbatim
// store-all (count=1 included, whitespace-trimmed names), the top-frequency
// digest, dry-run zero-write, apply value fidelity, and second-pass
// idempotency.
func TestBackfillWorkTags(t *testing.T) {
	clean(t)
	ctx := context.Background()
	reg, err := resolveRegistry(ctx, testDB)
	require.NoError(t, err)

	claimed := "galgame_wiki"

	// wGood: the defensive-parse showcase — a duplicate 百合 (30 beats 24), an
	// untrimmed "  PC  ", a count=1 tag (store-all), a whitespace-only name and
	// a missing-name element (both skipped).
	wGood := mkWork(t, reg.galgameMedium, "tags-good", nil)
	mkSubject(t, 201, `[{"name":"百合","count":24},{"name":"拔作","count":1},{"name":"  PC  ","count":5},{"name":"百合","count":30},{"name":"   ","count":3},{"count":9}]`)
	mkAnchor(t, wGood, "201", reg.bangumiSource, model.LinkKindExact, ruleTitleYear)

	wNull := mkWork(t, reg.galgameMedium, "tags-null", nil) // tags NULL → no_tags
	mkSubject(t, 202, "")
	mkAnchor(t, wNull, "202", reg.bangumiSource, model.LinkKindExact, ruleTitleYear)

	wEmpty := mkWork(t, reg.galgameMedium, "tags-empty", nil) // tags [] → no_tags
	mkSubject(t, 203, `[]`)
	mkAnchor(t, wEmpty, "203", reg.bangumiSource, model.LinkKindExact, ruleTitleYear)

	wObject := mkWork(t, reg.galgameMedium, "tags-object", nil) // tags {} → not_array
	mkSubject(t, 204, `{"name":"百合","count":9}`)
	mkAnchor(t, wObject, "204", reg.bangumiSource, model.LinkKindExact, ruleTitleYear)

	wClaimed := mkWork(t, reg.galgameMedium, "tags-claimed", &claimed) // claimed → ADMITTED (T2)
	mkSubject(t, 205, `[{"name":"百合","count":7}]`)
	mkAnchor(t, wClaimed, "205", reg.bangumiSource, model.LinkKindExact, ruleTitleYear)

	wProbable := mkWork(t, reg.galgameMedium, "tags-probable", nil) // probable tier → excluded by SQL
	mkSubject(t, 206, `[{"name":"百合","count":7}]`)
	mkAnchor(t, wProbable, "206", reg.bangumiSource, model.LinkKindProbable, "rule:bgm-title-only")

	wGood2 := mkWork(t, reg.galgameMedium, "tags-good2", nil) // 百合 on a second work → top name
	mkSubject(t, 207, `[{"name":"百合","count":2}]`)
	mkAnchor(t, wGood2, "207", reg.bangumiSource, model.LinkKindExact, ruleTitleYear)

	// --- dry run: decides, writes nothing.
	st, err := Run(ctx, Opts{DSN: testDSN})
	require.NoError(t, err)
	assert.Equal(t, 6, st.Candidates, "probable excluded; claimed admitted (T2)")
	assert.Equal(t, 2, st.NoTags, "NULL + empty array")
	assert.Equal(t, 1, st.NotArray)
	assert.Equal(t, 2, st.NameBlank, "whitespace-only + missing name")
	assert.Equal(t, 1, st.DupCollapsed, "the second 百合 collapsed")
	assert.Equal(t, 5, st.Planned, "wGood 3 + wGood2 1 + wClaimed 1 (T2 admits claimed)")
	assert.Equal(t, 3, st.DistinctNames, "百合 / PC / 拔作")
	assert.Zero(t, st.Written+st.Conflict+st.Errors)
	assert.EqualValues(t, 0, tagCount(t, ""), "dry run writes nothing")
	// Per-subject decided order is (count DESC, name): 百合 30 → PC 5 → 拔作 1.
	require.GreaterOrEqual(t, len(st.Samples), 3)
	assert.Equal(t, Sample{WorkID: wGood, SubjectID: 201, Name: "百合", Count: 30}, st.Samples[0],
		"duplicate collapsed to the HIGHEST count")
	assert.Equal(t, "PC", st.Samples[1].Name, "name whitespace-trimmed")
	assert.Equal(t, Sample{WorkID: wGood, SubjectID: 201, Name: "拔作", Count: 1}, st.Samples[2],
		"count=1 stored too (store-all, no threshold)")
	// Top-frequency digest: 百合 is planned on three works (wGood, wGood2, wClaimed).
	require.NotEmpty(t, st.TopNames)
	assert.Equal(t, NameFreq{Name: "百合", Works: 3}, st.TopNames[0])

	// --- apply: writes the decided plan exactly.
	st, err = Run(ctx, Opts{DSN: testDSN, Apply: true})
	require.NoError(t, err)
	assert.Equal(t, 5, st.Written)
	assert.Zero(t, st.Conflict+st.Errors)
	assert.EqualValues(t, 5, tagCount(t, ""))

	// Value fidelity: verbatim names (trimmed only), source-attributed counts.
	var rows []model.CatalogWorkTag
	require.NoError(t, testDB.Where("work_id = ?", wGood).Order("count DESC, name").Find(&rows).Error)
	require.Len(t, rows, 3)
	assert.Equal(t, "百合", rows[0].Name)
	assert.Equal(t, 30, rows[0].Count)
	assert.Equal(t, reg.bangumiSource, rows[0].SourceID)
	assert.Equal(t, "PC", rows[1].Name)
	assert.Equal(t, 5, rows[1].Count)
	assert.Equal(t, "拔作", rows[2].Name)
	assert.Equal(t, 1, rows[2].Count)
	assert.EqualValues(t, 1, tagCount(t, "WHERE work_id = ?", wGood2))
	assert.EqualValues(t, 1, tagCount(t, "WHERE work_id = ?", wClaimed),
		"T2: the claimed work's bgm folksonomy row now materialises")
	assert.EqualValues(t, 0, tagCount(t, "WHERE work_id = ?", wProbable),
		"probable anchors never materialise")

	// --- second apply: idempotent — zero writes, every planned row conflicts.
	st, err = Run(ctx, Opts{DSN: testDSN, Apply: true})
	require.NoError(t, err)
	assert.Zero(t, st.Written+st.Errors, "second pass writes zero")
	assert.Equal(t, 5, st.Conflict)
	assert.EqualValues(t, 5, tagCount(t, ""), "row count unchanged")
}

// TestNonTitleYearExactAnchorEntersCandidates pins the step-79 fix: an EXACT
// Bangumi work anchor whose matched_by is NOT rule:bgm-title-year (here the
// wave-78 rule:bgm-type4-gated tier) now enters the candidate set. Before the
// fix the hard-coded matched_by filter left the 11,465 new anchors invisible.
// The exact gate is unchanged: a probable anchor still stays out.
func TestNonTitleYearExactAnchorEntersCandidates(t *testing.T) {
	clean(t)
	ctx := context.Background()
	reg, err := resolveRegistry(ctx, testDB)
	require.NoError(t, err)

	wGated := mkWork(t, reg.galgameMedium, "tags-gated", nil)
	mkSubject(t, 501, `[{"name":"純愛","count":12}]`)
	mkAnchor(t, wGated, "501", reg.bangumiSource, model.LinkKindExact, "rule:bgm-type4-gated")

	wProbable := mkWork(t, reg.galgameMedium, "tags-probable", nil)
	mkSubject(t, 502, `[{"name":"純愛","count":9}]`)
	mkAnchor(t, wProbable, "502", reg.bangumiSource, model.LinkKindProbable, "rule:bgm-title-only")

	st, err := Run(ctx, Opts{DSN: testDSN, Apply: true})
	require.NoError(t, err)
	assert.Equal(t, 1, st.Candidates, "the gated-rule exact anchor is now a candidate; probable stays out")
	assert.EqualValues(t, 1, tagCount(t, "WHERE work_id = ?", wGated))
	assert.EqualValues(t, 0, tagCount(t, "WHERE work_id = ?", wProbable))
}

// TestClaimedMaterialisesAndDSNRequired pins the T2 change: the write-time XOR
// guard is gone, so a CLAIMED work's bgm folksonomy row materialises exactly
// like a bodyless one (the (facet, source) XOR keeps only bridge-not-copy, and
// that is enforced at the read face, not here). Also covers the ON CONFLICT
// idempotency backstop and the refuse-to-guess DSN discipline.
func TestClaimedMaterialisesAndDSNRequired(t *testing.T) {
	clean(t)
	ctx := context.Background()
	reg, err := resolveRegistry(ctx, testDB)
	require.NoError(t, err)

	claimed := "galgame_wiki"
	wClaimed := mkWork(t, reg.galgameMedium, "claimed-direct", &claimed)

	// T2: a claimed row now WRITES; a retry conflicts (the UNIQUE backstop)
	// instead of duplicating; a different name on the same work is a NEW row (the
	// unique key is (work_id, name, source_id), not one-per-source).
	w := &writer{db: testDB, stats: &Stats{}}
	w.write(ctx, plannedRow{WorkID: wClaimed, SourceID: reg.bangumiSource, Name: "百合", Count: 3}, true)
	assert.Equal(t, 1, w.stats.Written, "T2: the claimed work materialises")
	w.write(ctx, plannedRow{WorkID: wClaimed, SourceID: reg.bangumiSource, Name: "百合", Count: 3}, true)
	assert.Equal(t, 1, w.stats.Conflict, "ON CONFLICT refuses the duplicate")
	w.write(ctx, plannedRow{WorkID: wClaimed, SourceID: reg.bangumiSource, Name: "拔作", Count: 1}, true)
	assert.Equal(t, 2, w.stats.Written, "same work+source, different name → distinct row")
	assert.EqualValues(t, 2, tagCount(t, ""))

	// DSN discipline: required, never guessed.
	_, err = Run(ctx, Opts{})
	require.Error(t, err)
}
