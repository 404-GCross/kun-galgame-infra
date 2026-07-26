package workplatforms

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

// Integration test: catalog Gold schema + src_bangumi Silver schema + a
// minimal dlsite mirror fixture in its OWN schema (workplatforms_dl) via a
// search_path DSN (the workaliases pattern).
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
	if err := srcb.EnsureSchema(db); err != nil {
		fmt.Fprintf(os.Stderr, "SKIP: src_bangumi schema failed: %v\n", err)
		os.Exit(0)
	}
	for _, ddl := range []string{
		`CREATE SCHEMA IF NOT EXISTS workplatforms_dl`,
		`CREATE TABLE IF NOT EXISTS workplatforms_dl.works (workno text PRIMARY KEY, product_json jsonb)`,
	} {
		if err := db.Exec(ddl).Error; err != nil {
			fmt.Fprintf(os.Stderr, "SKIP: mirror fixture failed: %v\n", err)
			os.Exit(0)
		}
	}
	dlTestDSN = testDSN + " options='-csearch_path=workplatforms_dl'"
	testDB = db
	os.Exit(m.Run())
}

func clean(t *testing.T) {
	t.Helper()
	// catalog_platform (seeded registry) is deliberately NOT truncated.
	for _, table := range []string{
		"catalog_work_platform", "catalog_external_ref", "catalog_release", "catalog_work",
		"src_bangumi.subject", "workplatforms_dl.works",
	} {
		require.NoError(t, testDB.Exec("TRUNCATE "+table+" RESTART IDENTITY CASCADE").Error)
	}
}

func mediumID(t *testing.T, key string) int16 {
	t.Helper()
	var id int16
	require.NoError(t, testDB.Raw(`SELECT id FROM catalog_medium WHERE key = ?`, key).Scan(&id).Error)
	require.NotZero(t, id, "medium %s", key)
	return id
}

func otherMediumID(t *testing.T) int16 {
	t.Helper()
	var id int16
	require.NoError(t, testDB.Raw(`SELECT id FROM catalog_medium WHERE key <> 'galgame' ORDER BY id LIMIT 1`).Scan(&id).Error)
	require.NotZero(t, id)
	return id
}

func mkWork(t *testing.T, medium int16, name string, site *string) int64 {
	t.Helper()
	w := model.CatalogWork{MediumID: medium, OLang: "ja", DisplayName: name, Site: site}
	require.NoError(t, testDB.Create(&w).Error)
	return w.ID
}

func mkDlsiteRelAnchor(t *testing.T, workID int64, workno string, platform *string) int64 {
	t.Helper()
	rel := model.CatalogRelease{WorkID: workID, Kind: model.ReleaseKindDigital, Platform: platform}
	require.NoError(t, testDB.Create(&rel).Error)
	require.NoError(t, testDB.Create(&model.CatalogExternalRef{
		EntityType: model.EntityTypeRelease, EntityID: rel.ID, SourceID: 4,
		ExternalID: workno, LinkKind: model.LinkKindExact, MatchedBy: "rule:test",
	}).Error)
	return rel.ID
}

func mkBgmAnchor(t *testing.T, workID int64, subjectID string) {
	t.Helper()
	require.NoError(t, testDB.Create(&model.CatalogExternalRef{
		EntityType: model.EntityTypeWork, EntityID: workID, SourceID: 3,
		ExternalID: subjectID, LinkKind: model.LinkKindExact, MatchedBy: "rule:test",
	}).Error)
}

func mkSubject(t *testing.T, id int64, infobox string) {
	t.Helper()
	sub := srcb.Subject{
		ID: id, Type: 4, Name: fmt.Sprintf("subject-%d", id), NameCN: "",
		InfoboxRaw: "", ParseError: "", Summary: "", Date: "",
		ParserVersion: srcb.ParserVersion, IngestedAt: time.Now(),
	}
	if infobox != "" {
		sub.InfoboxParsed = []byte(infobox)
	}
	require.NoError(t, testDB.Create(&sub).Error)
}

func strPtr(s string) *string { return &s }

// TestImportWorkPlatforms pins both lanes end to end: the viewer-flag skip
// (smartphone/play), the win-first ordering, the empty-platform guard, the
// medium gate, the bgm normalization (alias map + direct registry hit +
// unmapped counting), the claimed exclusion, and second-apply idempotence.
func TestImportWorkPlatforms(t *testing.T) {
	clean(t)
	gal := mediumID(t, "galgame")
	other := otherMediumID(t)

	// dlsite lane candidates.
	wPC := mkWork(t, gal, "PC限定", nil)
	relPC := mkDlsiteRelAnchor(t, wPC, "RJ100001", nil) // pc + viewer flags → win only
	wPorts := mkWork(t, gal, "移植持ち", nil)
	relPorts := mkDlsiteRelAnchor(t, wPorts, "RJ100002", nil) // pc+android+ios → win,and,ios
	wNoMirror := mkWork(t, gal, "鏡像穴", nil)
	mkDlsiteRelAnchor(t, wNoMirror, "RJ100003", nil) // mirror has no platform array
	wFilled := mkWork(t, gal, "既充填", nil)
	relFilled := mkDlsiteRelAnchor(t, wFilled, "RJ100004", strPtr("win")) // not a candidate
	wOther := mkWork(t, other, "非galgame", nil)
	mkDlsiteRelAnchor(t, wOther, "RJ100005", nil) // medium-gated out
	require.NoError(t, testDB.Exec(`INSERT INTO workplatforms_dl.works (workno, product_json) VALUES
		('RJ100001', '{"platform": ["pc", "smartphone", "play"]}'),
		('RJ100002', '{"platform": ["pc", "android", "ios"]}'),
		('RJ100003', '{}'),
		('RJ100004', '{"platform": ["pc"]}'),
		('RJ100005', '{"platform": ["pc"]}')`).Error)

	// bgm lane candidates.
	wArr := mkWork(t, gal, "数组形", nil)
	mkBgmAnchor(t, wArr, "2001")
	mkSubject(t, 2001, `{"Fields":[{"Key":"平台","Array":true,"Value":"","Items":[{"Value":"PC"},{"Value":"Nintendo Switch"},{"Value":"Steam"}]}]}`)
	wScalar := mkWork(t, gal, "标量形", nil)
	mkBgmAnchor(t, wScalar, "2002")
	mkSubject(t, 2002, `{"Fields":[{"Key":"平台","Array":false,"Value":"PS4","Items":null}]}`)
	wDirect := mkWork(t, gal, "直击码", nil)
	mkBgmAnchor(t, wDirect, "2003")
	mkSubject(t, 2003, `{"Fields":[{"Key":"平台","Array":false,"Value":"psv","Items":null}]}`)
	wClaimed := mkWork(t, gal, "认领作品", strPtr("galgame_wiki"))
	mkBgmAnchor(t, wClaimed, "2004")
	mkSubject(t, 2004, `{"Fields":[{"Key":"平台","Array":false,"Value":"PC","Items":null}]}`)

	ctx := context.Background()
	opts := Opts{DSN: testDSN, DlsiteDSN: dlTestDSN, Source: "all"}

	// Dry: plan only.
	st, err := Run(ctx, opts)
	require.NoError(t, err)
	assert.Equal(t, 3, st.DlCandidates, "empty-platform galgame anchors only (filled + non-galgame gated out)")
	assert.Equal(t, 1, st.DlNoMirror)
	assert.Equal(t, 2, st.DlPlanned)
	assert.Equal(t, 3, st.BgmWorks, "claimed excluded")
	assert.Equal(t, 4, st.BgmPlanned, "win+swi / ps4 / psv")
	assert.Equal(t, 1, st.Unmapped["Steam"], "a store, not a platform")
	var n int64
	require.NoError(t, testDB.Table("catalog_work_platform").Count(&n).Error)
	assert.Zero(t, n, "dry run must not write")
	require.NoError(t, testDB.Table("catalog_release").Where("platform IS NOT NULL").Count(&n).Error)
	assert.Equal(t, int64(1), n, "dry run must not write (only the pre-filled row)")

	// Apply.
	opts.Apply = true
	st, err = Run(ctx, opts)
	require.NoError(t, err)
	assert.Equal(t, 2, st.DlWritten)
	assert.Equal(t, 4, st.BgmWritten)
	assert.Zero(t, st.Errors)

	var rel model.CatalogRelease
	require.NoError(t, testDB.First(&rel, relPC).Error)
	require.NotNil(t, rel.Platform)
	assert.Equal(t, "win", *rel.Platform)
	assert.JSONEq(t, `{"platforms":["win"]}`, string(rel.Extra), "viewer flags skipped")
	rel = model.CatalogRelease{}
	require.NoError(t, testDB.First(&rel, relPorts).Error)
	require.NotNil(t, rel.Platform)
	assert.Equal(t, "win", *rel.Platform, "win-first primary")
	assert.JSONEq(t, `{"platforms":["win","and","ios"]}`, string(rel.Extra))
	rel = model.CatalogRelease{}
	require.NoError(t, testDB.First(&rel, relFilled).Error)
	assert.Equal(t, "{}", string(rel.Extra), "pre-filled row untouched")

	var rows []model.CatalogWorkPlatform
	require.NoError(t, testDB.Where("work_id = ?", wArr).Order("platform").Find(&rows).Error)
	require.Len(t, rows, 2)
	assert.Equal(t, "swi", rows[0].Platform)
	assert.Equal(t, "win", rows[1].Platform)
	assert.Equal(t, int16(3), rows[0].SourceID)
	require.NoError(t, testDB.Where("work_id = ?", wScalar).Find(&rows).Error)
	require.Len(t, rows, 1)
	assert.Equal(t, "ps4", rows[0].Platform)
	require.NoError(t, testDB.Where("work_id = ?", wDirect).Find(&rows).Error)
	require.Len(t, rows, 1)
	assert.Equal(t, "psv", rows[0].Platform, "direct registry-code hit")
	require.NoError(t, testDB.Table("catalog_work_platform").Where("work_id = ?", wClaimed).Count(&n).Error)
	assert.Zero(t, n, "claimed work got nothing")

	// Second apply: dlsite guard finds no candidates, bgm rows all conflict.
	st, err = Run(ctx, opts)
	require.NoError(t, err)
	assert.Equal(t, 1, st.DlCandidates, "only the no-mirror hole stays a candidate (filled rows guard-removed)")
	assert.Equal(t, 1, st.DlNoMirror)
	assert.Zero(t, st.DlWritten+st.BgmWritten, "idempotent re-run")
	assert.Equal(t, 4, st.BgmConflict)
}
