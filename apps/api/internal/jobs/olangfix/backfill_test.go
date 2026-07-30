package olangfix

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"api/internal/platform/catalog/migrate"
	"api/internal/platform/catalog/model"
	"api/internal/platform/catalog/seed"
	"api/internal/platform/catalog/srcvndb"
	gmodel "api/internal/platform/galgame/model"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// Integration test against a real Postgres holding the exact single-database
// shape this backfill runs against in production: the catalog Gold schema
// (migrate.Run + registry seeds), the src_vndb mirror schema, and the wiki
// galgame table. Run drives the DSN itself, so we capture it (not just the
// handle) to exercise the real entry point.
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
	if err := srcvndb.EnsureSchema(db); err != nil {
		fmt.Fprintf(os.Stderr, "SKIP: src_vndb schema failed: %v\n", err)
		os.Exit(0)
	}
	if err := db.AutoMigrate(&gmodel.Galgame{}); err != nil {
		fmt.Fprintf(os.Stderr, "SKIP: galgame automigrate failed: %v\n", err)
		os.Exit(0)
	}
	testDB = db
	os.Exit(m.Run())
}

func clean(t *testing.T) {
	t.Helper()
	for _, table := range []string{
		"catalog_external_ref", "catalog_work", "galgame", "src_vndb.vn",
	} {
		require.NoError(t, testDB.Exec("TRUNCATE "+table+" CASCADE").Error)
	}
}

// mkWork inserts a registry row with the flat 'ja' every lane used to stamp —
// the exact state wave 144 has to undo.
func mkWork(t *testing.T, medium int16, name string, site *string, productWorkID *int64) int64 {
	t.Helper()
	w := model.CatalogWork{
		MediumID: medium, OLang: model.OLangDefault, DisplayName: name,
		Site: site, ProductWorkID: productWorkID,
	}
	require.NoError(t, testDB.Create(&w).Error)
	return w.ID
}

func mkAnchor(t *testing.T, workID int64, externalID string, source, kind int16) {
	t.Helper()
	require.NoError(t, testDB.Create(&model.CatalogExternalRef{
		EntityType: model.EntityTypeWork, EntityID: workID, SourceID: source,
		ExternalID: externalID, LinkKind: kind, MatchedBy: "test",
	}).Error)
}

func mkVN(t *testing.T, id, olang string) {
	t.Helper()
	require.NoError(t, testDB.Create(&srcvndb.VN{ID: id, OLang: olang, IngestedAt: time.Now()}).Error)
}

// mkGame inserts a wiki galgame row. original_language is ALWAYS stated: the
// column carries a GORM `default:'ja-jp'` tag, so an unset field would silently
// come back as ja-jp (the default-tag zero-value trap).
func mkGame(t *testing.T, name, originalLanguage string) int64 {
	t.Helper()
	g := gmodel.Galgame{NameJaJP: name, OriginalLanguage: originalLanguage, UserID: 1}
	require.NoError(t, testDB.Create(&g).Error)
	return int64(g.ID)
}

func olangOf(t *testing.T, workID int64) string {
	t.Helper()
	var v string
	require.NoError(t, testDB.Raw(`SELECT olang FROM catalog_work WHERE id = ?`, workID).Scan(&v).Error)
	return v
}

func ptr[T any](v T) *T { return &v }

// fixture is the whole population one pass sees, so every test can name the row
// it cares about.
type fixture struct {
	reg registry
	// lane V
	vnEN, vnJA, vnMissing, vnBlank, vnMulti int64
	vnProbable, vnASMR, vnDeleted           int64
	// lane W
	wikiEN, wikiJunk, wikiGone, wikiPT, wikiAnchored int64
}

func seedFixture(t *testing.T) fixture {
	t.Helper()
	clean(t)
	reg, err := resolveRegistry(context.Background(), testDB)
	require.NoError(t, err)
	f := fixture{reg: reg}
	wiki := siteGalgameWiki

	// ── lane V: the VNDB authority ───────────────────────────────────────────
	f.vnEN = mkWork(t, reg.galgameMedium, "vn-en", nil, nil)
	mkAnchor(t, f.vnEN, "v100", reg.vndbSource, model.LinkKindExact)
	mkVN(t, "v100", "en")

	f.vnJA = mkWork(t, reg.galgameMedium, "vn-ja", nil, nil) // already correct
	mkAnchor(t, f.vnJA, "v101", reg.vndbSource, model.LinkKindExact)
	mkVN(t, "v101", "ja")

	f.vnMissing = mkWork(t, reg.galgameMedium, "vn-missing", nil, nil) // no mirror row
	mkAnchor(t, f.vnMissing, "v102", reg.vndbSource, model.LinkKindExact)

	f.vnBlank = mkWork(t, reg.galgameMedium, "vn-blank", nil, nil) // mirror row, blank olang
	mkAnchor(t, f.vnBlank, "v103", reg.vndbSource, model.LinkKindExact)
	mkVN(t, "v103", "")

	// Two exact anchors on one work: defensive only. The LOWEST external_id wins.
	f.vnMulti = mkWork(t, reg.galgameMedium, "vn-multi", nil, nil)
	mkAnchor(t, f.vnMulti, "v105", reg.vndbSource, model.LinkKindExact)
	mkAnchor(t, f.vnMulti, "v104", reg.vndbSource, model.LinkKindExact)
	mkVN(t, "v104", "ru")
	mkVN(t, "v105", "ko")

	f.vnProbable = mkWork(t, reg.galgameMedium, "vn-probable", nil, nil) // probable ≠ identity
	mkAnchor(t, f.vnProbable, "v106", reg.vndbSource, model.LinkKindProbable)
	mkVN(t, "v106", "en")

	asmr := int16(0)
	require.NoError(t, testDB.Raw(`SELECT id FROM catalog_medium WHERE key = 'asmr'`).Scan(&asmr).Error)
	require.NotZero(t, asmr)
	f.vnASMR = mkWork(t, asmr, "vn-asmr", nil, nil) // another medium, out of the galgame gate
	mkAnchor(t, f.vnASMR, "v107", reg.vndbSource, model.LinkKindExact)
	mkVN(t, "v107", "en")

	f.vnDeleted = mkWork(t, reg.galgameMedium, "vn-deleted", nil, nil)
	mkAnchor(t, f.vnDeleted, "v108", reg.vndbSource, model.LinkKindExact)
	mkVN(t, "v108", "en")
	require.NoError(t, testDB.Exec(`UPDATE catalog_work SET deleted_at = now() WHERE id = ?`, f.vnDeleted).Error)

	// ── lane W: the wiki remainder ───────────────────────────────────────────
	f.wikiEN = mkWork(t, reg.galgameMedium, "wiki-en", &wiki, ptr(mkGame(t, "wiki-en", "en-us")))
	f.wikiJunk = mkWork(t, reg.galgameMedium, "wiki-junk", &wiki, ptr(mkGame(t, "wiki-junk", "others")))
	f.wikiGone = mkWork(t, reg.galgameMedium, "wiki-gone", &wiki, ptr(int64(9_000_001))) // galgame row absent
	f.wikiPT = mkWork(t, reg.galgameMedium, "wiki-pt", &wiki, ptr(mkGame(t, "wiki-pt", "pt-pt")))

	// Claimed AND vndb-anchored: lane V owns it, lane W must not see it twice.
	f.wikiAnchored = mkWork(t, reg.galgameMedium, "wiki-anchored", &wiki, ptr(mkGame(t, "wiki-anchored", "zh-cn")))
	mkAnchor(t, f.wikiAnchored, "v109", reg.vndbSource, model.LinkKindExact)
	mkVN(t, "v109", "zh-Hant") // VNDB disagrees with the wiki → VNDB wins

	return f
}

// TestBackfillOLangPlanAndApply drives the whole pipeline through the real Run
// entry point: both lanes' candidate gates, every skip branch, the transition
// matrix, the dry-run zero-write, apply fidelity, and the dry → apply → dry
// rehearsal whose second dry MUST come back empty.
func TestBackfillOLangPlanAndApply(t *testing.T) {
	f := seedFixture(t)
	ctx := context.Background()

	// ── dry run: decides everything, writes nothing ──────────────────────────
	st, err := Run(ctx, Opts{DSN: testDSN})
	require.NoError(t, err)

	assert.Equal(t, 6, st.VNCandidates,
		"galgame-medium, non-deleted, exact vndb anchor: en/ja/missing/blank/multi/wiki-anchored")
	assert.Equal(t, 1, st.VNMultiAnchor, "the second anchor on vn-multi is counted, not silently dropped")
	assert.Equal(t, 1, st.VNMissingRow)
	assert.Equal(t, 1, st.VNBlankOLang)
	assert.Equal(t, 3, st.VNPlanned, "vn-en → en, vn-multi → ru, wiki-anchored → zh-Hant")
	assert.Equal(t, 1, st.VNUnchanged, "vn-ja is already correct")

	assert.Equal(t, 4, st.WikiCandidates,
		"wiki-anchored belongs to lane V, so lane W sees only the other four")
	assert.Equal(t, 1, st.WikiRowMissing)
	assert.Equal(t, 1, st.WikiJunkLang, "'others' → the deliberate default, counted")
	assert.Equal(t, 2, st.WikiPlanned, "wiki-en → en, wiki-pt → pt-pt")
	assert.Equal(t, 2, st.WikiUnchanged, "wiki-junk and wiki-gone both resolve to the ja they already hold")

	assert.Equal(t, 5, st.Planned)
	assert.Zero(t, st.Written+st.Errors, "a dry run writes nothing")
	assert.Equal(t, []string{"pt-pt"}, st.UnknownOLangs,
		"a pass-through value absent from the VNDB vocabulary warns; it never blocks")

	// The transition matrix counts CHANGES only.
	assert.Equal(t, 4, st.DistinctTransitions, "ja→en (×2), ja→ru, ja→zh-Hant, ja→pt-pt")
	require.NotEmpty(t, st.Transitions)
	assert.Equal(t, Transition{From: "ja", To: "en", Works: 2}, st.Transitions[0])

	for _, id := range []int64{f.vnEN, f.vnMulti, f.wikiEN, f.wikiPT, f.wikiAnchored} {
		assert.Equalf(t, "ja", olangOf(t, id), "dry run must not move work %d", id)
	}

	// ── apply: writes exactly the decided plan ───────────────────────────────
	st, err = Run(ctx, Opts{DSN: testDSN, Apply: true})
	require.NoError(t, err)
	assert.Equal(t, 5, st.Planned)
	assert.Equal(t, 5, st.Written)
	assert.Zero(t, st.Errors)

	assert.Equal(t, "en", olangOf(t, f.vnEN))
	assert.Equal(t, "ja", olangOf(t, f.vnJA))
	assert.Equal(t, "ru", olangOf(t, f.vnMulti), "the lowest external_id decided it")
	assert.Equal(t, "zh-Hant", olangOf(t, f.wikiAnchored), "an anchored claim takes VNDB, not the wiki locale")
	assert.Equal(t, "en", olangOf(t, f.wikiEN))
	assert.Equal(t, "pt-pt", olangOf(t, f.wikiPT))
	// Untouched populations: skipped lane-V rows and the works outside both gates.
	for _, id := range []int64{f.vnMissing, f.vnBlank, f.vnProbable, f.vnASMR, f.vnDeleted, f.wikiJunk, f.wikiGone} {
		assert.Equalf(t, "ja", olangOf(t, id), "work %d must be left alone", id)
	}

	// ── second dry: the rehearsal's pass condition ───────────────────────────
	st, err = Run(ctx, Opts{DSN: testDSN})
	require.NoError(t, err)
	assert.Zero(t, st.Planned, "a converged registry plans nothing")
	assert.Empty(t, st.Transitions, "the transition matrix must be all-zero on the second dry run")
	assert.Zero(t, st.VNPlanned+st.WikiPlanned)

	// ── second apply: idempotent ─────────────────────────────────────────────
	st, err = Run(ctx, Opts{DSN: testDSN, Apply: true})
	require.NoError(t, err)
	assert.Zero(t, st.Written+st.Errors, "a second apply writes zero")
}

// TestBackfillOLangDoesNotTouchUpdatedAt pins the 轨长 ruling of 2026-07-29:
// unlike every other facet backfill in this tree, this one must NOT move the
// host work's updated_at. olang is a population predicate, not work content, and
// bumping 82k watermarks would shove the whole registry through the
// /v1/catalog/changes keyset feed and invalidate every downstream ETag at once.
// The new values reach the read faces through a full reindex instead.
func TestBackfillOLangDoesNotTouchUpdatedAt(t *testing.T) {
	f := seedFixture(t)
	ctx := context.Background()

	stamp := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	require.NoError(t, testDB.Exec(`UPDATE catalog_work SET updated_at = ?`, stamp).Error)

	st, err := Run(ctx, Opts{DSN: testDSN, Apply: true})
	require.NoError(t, err)
	require.Equal(t, 5, st.Written)

	var moved int64
	require.NoError(t, testDB.Raw(
		`SELECT count(*) FROM catalog_work WHERE updated_at IS DISTINCT FROM ?`, stamp).Scan(&moved).Error)
	assert.Zero(t, moved, "no watermark may move, least of all the rewritten works")
	assert.Equal(t, "en", olangOf(t, f.vnEN), "…while the value itself did change")
}

// TestBackfillOLangWindowAndDSN covers the chunking window and the refuse-to-
// guess DSN discipline. The window walks the COMBINED candidate list — lane V
// first, then lane W — so an operator can resume a long run at an offset.
func TestBackfillOLangWindowAndDSN(t *testing.T) {
	f := seedFixture(t)
	ctx := context.Background()

	// The first candidate is vn-en (lowest work id in lane V).
	st, err := Run(ctx, Opts{DSN: testDSN, Apply: true, Limit: 1})
	require.NoError(t, err)
	assert.Equal(t, 1, st.Written)
	assert.Equal(t, "en", olangOf(t, f.vnEN))
	assert.Equal(t, "ja", olangOf(t, f.vnMulti), "outside the window")

	// Offset past the whole list decides nothing at all.
	st, err = Run(ctx, Opts{DSN: testDSN, Apply: true, Offset: 1000})
	require.NoError(t, err)
	assert.Zero(t, st.Planned+st.Written)

	// The rest of the plan still lands on a resumed run.
	st, err = Run(ctx, Opts{DSN: testDSN, Apply: true, Offset: 1})
	require.NoError(t, err)
	assert.Equal(t, 4, st.Written)
	assert.Equal(t, "ru", olangOf(t, f.vnMulti))

	// DSN discipline: required, never guessed.
	_, err = Run(ctx, Opts{})
	require.Error(t, err)
}
