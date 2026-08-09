package importer

import (
	"testing"

	"api/internal/platform/catalog/model"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// cleanReleases truncates the releases-family staging tables (the shared clean()
// covers the chars/staff families but not these).
func cleanReleases(t *testing.T) {
	t.Helper()
	for _, tb := range []string{
		"src_vndb.releases", "src_vndb.releases_vn",
		"src_vndb.releases_titles", "src_vndb.releases_platforms",
	} {
		require.NoError(t, testDB.Exec("TRUNCATE "+tb+" RESTART IDENTITY CASCADE").Error)
	}
}

// seedVNDBWork (work + EXACT source-2 vndb work anchor) is shared with
// rostervndb_test.go.

func insRelease(t *testing.T, id, olang string, released int, minage *int16, patch, freeware, official bool) {
	t.Helper()
	require.NoError(t, testDB.Exec(`INSERT INTO src_vndb.releases
		(id, gtin, olang, released, voiced, reso_x, reso_y, minage, ani_story, ani_ero, has_ero, patch, freeware, official, catalog, notes, engine)
		VALUES (?,0,?,?,0,0,0,?,0,0,false,?,?,?,'','','')`,
		id, olang, released, minage, patch, freeware, official).Error)
}

func insReleaseVN(t *testing.T, id, vid, rtype string) {
	t.Helper()
	require.NoError(t, testDB.Exec(`INSERT INTO src_vndb.releases_vn (id, vid, rtype) VALUES (?,?,?)`, id, vid, rtype).Error)
}

func insReleaseTitle(t *testing.T, id, lang string, mtl bool, title, latin string) {
	t.Helper()
	require.NoError(t, testDB.Exec(`INSERT INTO src_vndb.releases_titles (id, lang, mtl, title, latin) VALUES (?,?,?,?,?)`, id, lang, mtl, title, latin).Error)
}

func insReleasePlatform(t *testing.T, id, platform string) {
	t.Helper()
	require.NoError(t, testDB.Exec(`INSERT INTO src_vndb.releases_platforms (id, platform) VALUES (?,?)`, id, platform).Error)
}

func i16(v int16) *int16 { return &v }

func countWhere(t *testing.T, q string, args ...any) int64 {
	t.Helper()
	var n int64
	require.NoError(t, testDB.Raw(q, args...).Scan(&n).Error)
	return n
}

func scalarStr(t *testing.T, q string, args ...any) string {
	t.Helper()
	var s string
	require.NoError(t, testDB.Raw(q, args...).Scan(&s).Error)
	return s
}

func TestVNDBReleasesWave(t *testing.T) {
	clean(t)
	cleanReleases(t)

	workA := seedVNDBWork(t, "v10")
	workB := seedVNDBWork(t, "v11")

	// A pre-existing 1:1 stub release on work A with a DLsite workno anchor — the
	// wave must never touch it.
	var stubID int64
	require.NoError(t, testDB.Raw(`INSERT INTO catalog_release (work_id, kind, extra, created_at) VALUES (?, 1, '{}', now()) RETURNING id`, workA).Scan(&stubID).Error)
	require.NoError(t, testDB.Exec(`INSERT INTO catalog_external_ref (entity_type, entity_id, source_id, external_id, link_kind, matched_by)
		VALUES (6, ?, 4, 'RJ999', 0, 'rule:dlsite-work-import')`, stubID).Error)

	// r1: single-vn complete, full date, minage 18, two platforms, ja+en titles.
	insRelease(t, "r1", "ja", 20200115, i16(18), false, false, true)
	insReleaseVN(t, "r1", "v10", "complete")
	insReleaseTitle(t, "r1", "ja", false, "タイトルA", "Title A romaji")
	insReleaseTitle(t, "r1", "en", false, "Title A", "")
	insReleasePlatform(t, "r1", "win")
	insReleasePlatform(t, "r1", "swi")

	// r2: patch (kind precedence over rtype), year-month partial date, freeware.
	insRelease(t, "r2", "ja", 20210300, nil, true, true, false)
	insReleaseVN(t, "r2", "v10", "complete")
	insReleaseTitle(t, "r2", "ja", false, "パッチ", "")
	insReleasePlatform(t, "r2", "win")

	// r3: trial + minage 0 (all-ages, meaningful).
	insRelease(t, "r3", "ja", 20190505, i16(0), false, false, true)
	insReleaseVN(t, "r3", "v10", "trial")
	insReleaseTitle(t, "r3", "ja", false, "体験版", "")

	// r4: multi-vn (covers v10 AND v11) → one row per work, NO anchor.
	insRelease(t, "r4", "ja", 20220101, nil, false, false, true)
	insReleaseVN(t, "r4", "v10", "complete")
	insReleaseVN(t, "r4", "v11", "complete")
	insReleaseTitle(t, "r4", "ja", false, "合作", "")

	// r5: TBA (99999999) → no date.
	insRelease(t, "r5", "ja", 99999999, nil, false, false, true)
	insReleaseVN(t, "r5", "v11", "complete")
	insReleaseTitle(t, "r5", "ja", false, "未定", "")

	// r6: original language en but only an MTL en title → no title, Lang=en.
	insRelease(t, "r6", "en", 20180101, nil, false, false, false)
	insReleaseVN(t, "r6", "v11", "complete")
	insReleaseTitle(t, "r6", "en", true, "MTL Title", "")

	// --- dry run: plan only, writes nothing ---
	dry, err := New(testDB, nil, Options{DryRun: true}).RunReleases()
	require.NoError(t, err)
	assert.Equal(t, 7, dry.InGatePairs, "r1,r2,r3,r4(A),r4(B),r5,r6")
	assert.Equal(t, 7, dry.Planned)
	assert.Equal(t, 7, dry.ReleasesWritten, "dry would-be")
	assert.Equal(t, 5, dry.AnchorsWritten, "single-vn: r1,r2,r3,r5,r6")
	assert.Equal(t, 2, dry.MultiVNUnanchored, "r4 → work A + work B")
	assert.Equal(t, 1, dry.NoDate, "r5 TBA")
	assert.Equal(t, 1, dry.NoTitle, "r6 mtl-only olang")
	assert.Equal(t, 5, dry.KindDefault, "r1,r4A,r4B,r5,r6")
	assert.Equal(t, 1, dry.KindTrial)
	assert.Equal(t, 1, dry.KindPatch)
	assert.Equal(t, int64(0), countWhere(t, `SELECT count(*) FROM catalog_release WHERE extra->>'vndb_id' IS NOT NULL`), "dry writes nothing")

	// --- apply ---
	st, err := New(testDB, nil, Options{}).RunReleases()
	require.NoError(t, err)
	assert.Equal(t, 7, st.ReleasesWritten)
	assert.Equal(t, 5, st.AnchorsWritten)
	assert.Zero(t, st.Errors)

	// 7 new vndb rows + the untouched stub = 8 total.
	assert.Equal(t, int64(8), countWhere(t, `SELECT count(*) FROM catalog_release`))
	assert.Equal(t, int64(7), countWhere(t, `SELECT count(*) FROM catalog_release WHERE extra->>'vndb_id' IS NOT NULL`))

	// Existing stub is untouched (kind + empty extra + its dlsite anchor).
	assert.Equal(t, int64(1), countWhere(t, `SELECT count(*) FROM catalog_release WHERE id=? AND kind=? AND extra::text='{}'`, stubID, model.ReleaseKindDigital))
	assert.Equal(t, int64(1), countWhere(t, `SELECT count(*) FROM catalog_external_ref WHERE entity_type=6 AND external_id='RJ999' AND source_id=4`))

	// Exact release anchors: 5 vndb (r1,r2,r3,r5,r6); r4 (multi-vn) gets none.
	assert.Equal(t, int64(5), countWhere(t, `SELECT count(*) FROM catalog_external_ref WHERE entity_type=6 AND source_id=2 AND link_kind=0 AND matched_by='rule:vndb-release-import'`))
	assert.Equal(t, int64(0), countWhere(t, `SELECT count(*) FROM catalog_external_ref WHERE entity_type=6 AND source_id=2 AND external_id='r4'`))

	// r4 → exactly two rows, one per covered work, both carrying vndb_id=r4.
	assert.Equal(t, int64(2), countWhere(t, `SELECT count(*) FROM catalog_release WHERE extra->>'vndb_id'='r4'`))
	assert.Equal(t, int64(1), countWhere(t, `SELECT count(*) FROM catalog_release WHERE extra->>'vndb_id'='r4' AND work_id=?`, workA))
	assert.Equal(t, int64(1), countWhere(t, `SELECT count(*) FROM catalog_release WHERE extra->>'vndb_id'='r4' AND work_id=?`, workB))

	// r1 field mapping.
	var r1Kind int16
	var r1Lang, r1Title, r1Platform *string
	var r1Y, r1M, r1D *int16
	require.NoError(t, testDB.Raw(`SELECT kind, lang, title, platform, released_y, released_m, released_d
		FROM catalog_release WHERE extra->>'vndb_id'='r1'`).Row().
		Scan(&r1Kind, &r1Lang, &r1Title, &r1Platform, &r1Y, &r1M, &r1D))
	assert.Equal(t, model.ReleaseKindDefault, r1Kind)
	require.NotNil(t, r1Lang)
	assert.Equal(t, "ja", *r1Lang)
	require.NotNil(t, r1Title)
	assert.Equal(t, "タイトルA", *r1Title)
	require.NotNil(t, r1Platform)
	assert.Equal(t, "swi", *r1Platform, "first platform code sorted ascending")
	require.NotNil(t, r1Y)
	assert.Equal(t, int16(2020), *r1Y)
	require.NotNil(t, r1M)
	assert.Equal(t, int16(1), *r1M)
	require.NotNil(t, r1D)
	assert.Equal(t, int16(15), *r1D)

	// r1 Extra content.
	assert.Equal(t, "18", scalarStr(t, `SELECT extra->>'minage' FROM catalog_release WHERE extra->>'vndb_id'='r1'`))
	assert.Equal(t, "false", scalarStr(t, `SELECT extra->>'freeware' FROM catalog_release WHERE extra->>'vndb_id'='r1'`))
	assert.Equal(t, "true", scalarStr(t, `SELECT extra->>'official' FROM catalog_release WHERE extra->>'vndb_id'='r1'`))
	assert.Equal(t, `["en", "ja"]`, scalarStr(t, `SELECT extra->'languages' FROM catalog_release WHERE extra->>'vndb_id'='r1'`))
	assert.Equal(t, `["swi", "win"]`, scalarStr(t, `SELECT extra->'platforms' FROM catalog_release WHERE extra->>'vndb_id'='r1'`))

	// r2 patch + partial date (day dropped) + minage unknown → key omitted.
	assert.Equal(t, int64(1), countWhere(t, `SELECT count(*) FROM catalog_release WHERE extra->>'vndb_id'='r2' AND kind=? AND released_y=2021 AND released_m=3 AND released_d IS NULL`, model.ReleaseKindPatch))
	assert.Equal(t, int64(1), countWhere(t, `SELECT count(*) FROM catalog_release WHERE extra->>'vndb_id'='r2' AND extra->>'minage' IS NULL`))
	// r3 minage 0 (all-ages) → key present as 0.
	assert.Equal(t, "0", scalarStr(t, `SELECT extra->>'minage' FROM catalog_release WHERE extra->>'vndb_id'='r3'`))
	// r5 TBA → null date trio.
	assert.Equal(t, int64(1), countWhere(t, `SELECT count(*) FROM catalog_release WHERE extra->>'vndb_id'='r5' AND released_y IS NULL`))
	// r6 mtl-only olang → title NULL, lang still en.
	assert.Equal(t, int64(1), countWhere(t, `SELECT count(*) FROM catalog_release WHERE extra->>'vndb_id'='r6' AND title IS NULL AND lang='en'`))

	// One imported revision per new release.
	assert.Equal(t, int64(7), countWhere(t, `SELECT count(*) FROM catalog_revision WHERE entity_type=6 AND action=?`, model.RevisionActionImported))

	// --- idempotence: second apply writes nothing ---
	st2, err := New(testDB, nil, Options{}).RunReleases()
	require.NoError(t, err)
	assert.Zero(t, st2.ReleasesWritten)
	assert.Zero(t, st2.AnchorsWritten)
	assert.Equal(t, 7, st2.SkippedExisting)
	assert.Equal(t, int64(8), countWhere(t, `SELECT count(*) FROM catalog_release`))
}

func TestVNDBReleasesLimit(t *testing.T) {
	clean(t)
	cleanReleases(t)
	// Two works; --limit 1 keeps only the lowest work id's releases.
	workA := seedVNDBWork(t, "v20")
	_ = seedVNDBWork(t, "v21")
	insRelease(t, "r10", "ja", 20200101, nil, false, false, true)
	insReleaseVN(t, "r10", "v20", "complete")
	insReleaseTitle(t, "r10", "ja", false, "A", "")
	insRelease(t, "r11", "ja", 20200101, nil, false, false, true)
	insReleaseVN(t, "r11", "v21", "complete")
	insReleaseTitle(t, "r11", "ja", false, "B", "")

	st, err := New(testDB, nil, Options{Limit: 1}).RunReleases()
	require.NoError(t, err)
	assert.Equal(t, 1, st.ReleasesWritten)
	assert.Equal(t, int64(1), countWhere(t, `SELECT count(*) FROM catalog_release WHERE work_id=?`, workA))
}

// TestVNDBReleasesRetiredHolderIsNotVacant pins the liveness-blind resume
// index: a soft-deleted release still answers to its vndb id (and still holds
// the exact anchor, which uq_catalog_external_ref_exact would not let a second
// one join), so a later pass must skip it instead of resurrecting it.
func TestVNDBReleasesRetiredHolderIsNotVacant(t *testing.T) {
	clean(t)
	cleanReleases(t)
	seedVNDBWork(t, "v30")
	insRelease(t, "r20", "ja", 20200101, nil, false, false, true)
	insReleaseVN(t, "r20", "v30", "complete")
	insReleaseTitle(t, "r20", "ja", false, "退役", "")

	first, err := New(testDB, nil, Options{}).RunReleases()
	require.NoError(t, err)
	require.Equal(t, 1, first.ReleasesWritten)
	require.Equal(t, 1, first.AnchorsWritten)

	require.NoError(t, testDB.Exec(`UPDATE catalog_release SET deleted_at = now() WHERE extra->>'vndb_id'='r20'`).Error)

	st, err := New(testDB, nil, Options{}).RunReleases()
	require.NoError(t, err)
	assert.Zero(t, st.Planned, "a retired holder is a claim, not a vacancy")
	assert.Zero(t, st.ReleasesWritten)
	assert.Equal(t, 1, st.SkippedRetired)
	assert.Zero(t, st.SkippedExisting, "the skip is counted in its own class, not folded into the live one")
	assert.Equal(t, int64(1), countWhere(t, `SELECT count(*) FROM catalog_release WHERE extra->>'vndb_id'='r20'`), "no resurrection")
}

func TestParseVNDBReleased(t *testing.T) {
	maxYear := 2029
	cases := []struct {
		released     int
		wantY        int16
		wantM, wantD *int16
		ok           bool
	}{
		{20200115, 2020, i16(1), i16(15), true},
		{20210300, 2021, i16(3), nil, true}, // day 0 → partial
		{20190000, 2019, nil, nil, true},    // month+day 0 → year only
		{99999999, 0, nil, nil, false},      // TBA
		{19000101, 0, nil, nil, false},      // before the 1950 floor
		{20200015, 2020, nil, nil, true},    // month 0 with day → month/day dropped
	}
	for _, c := range cases {
		y, m, d, ok := parseVNDBReleased(c.released, maxYear)
		assert.Equal(t, c.ok, ok, "released %d ok", c.released)
		if !ok {
			continue
		}
		assert.Equal(t, c.wantY, y, "released %d year", c.released)
		assert.Equal(t, derefOr16(c.wantM), derefOr16(m), "released %d month", c.released)
		assert.Equal(t, derefOr16(c.wantD), derefOr16(d), "released %d day", c.released)
	}
}

func derefOr16(p *int16) int16 {
	if p == nil {
		return -1
	}
	return *p
}
