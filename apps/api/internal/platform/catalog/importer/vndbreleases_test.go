package importer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"api/internal/platform/catalog/model"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func cleanReleases(t *testing.T) {
	t.Helper()
	for _, tb := range []string{
		"src_vndb.releases", "src_vndb.releases_vn",
		"src_vndb.releases_titles", "src_vndb.releases_platforms",
	} {
		require.NoError(t, testDB.Exec("TRUNCATE "+tb+" RESTART IDENTITY CASCADE").Error)
	}
}

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

	var stubID int64
	require.NoError(t, testDB.Raw(`INSERT INTO catalog_release (work_id, kind, extra, created_at) VALUES (?, 1, '{}', now()) RETURNING id`, workA).Scan(&stubID).Error)
	require.NoError(t, testDB.Exec(`INSERT INTO catalog_external_ref (entity_type, entity_id, source_id, external_id, link_kind, matched_by)
		VALUES (6, ?, 4, 'RJ999', 0, 'rule:dlsite-work-import')`, stubID).Error)

	insRelease(t, "r1", "ja", 20200115, i16(18), false, false, true)
	insReleaseVN(t, "r1", "v10", "complete")
	insReleaseTitle(t, "r1", "ja", false, "タイトルA", "Title A romaji")
	insReleaseTitle(t, "r1", "en", false, "Title A", "")
	insReleasePlatform(t, "r1", "win")
	insReleasePlatform(t, "r1", "swi")

	insRelease(t, "r2", "ja", 20210300, nil, true, true, false)
	insReleaseVN(t, "r2", "v10", "complete")
	insReleaseTitle(t, "r2", "ja", false, "パッチ", "")
	insReleasePlatform(t, "r2", "win")

	insRelease(t, "r3", "ja", 20190505, i16(0), false, false, true)
	insReleaseVN(t, "r3", "v10", "trial")
	insReleaseTitle(t, "r3", "ja", false, "体験版", "")

	insRelease(t, "r4", "ja", 20220101, nil, false, false, true)
	insReleaseVN(t, "r4", "v10", "complete")
	insReleaseVN(t, "r4", "v11", "complete")
	insReleaseTitle(t, "r4", "ja", false, "合作", "")

	insRelease(t, "r5", "ja", 99999999, nil, false, false, true)
	insReleaseVN(t, "r5", "v11", "complete")
	insReleaseTitle(t, "r5", "ja", false, "未定", "")

	insRelease(t, "r6", "en", 20180101, nil, false, false, false)
	insReleaseVN(t, "r6", "v11", "complete")
	insReleaseTitle(t, "r6", "en", true, "MTL Title", "")

	dry, err := New(testDB, nil, Options{DryRun: true}).RunReleases()
	require.NoError(t, err)
	assert.Equal(t, 7, dry.InGatePairs, "r1,r2,r3,r4(A),r4(B),r5,r6")
	assert.Equal(t, 7, dry.Planned)
	assert.Equal(t, 7, dry.ReleasesWritten, "dry would-be")
	assert.Equal(t, 5, dry.AnchorsWritten, "single-vn: r1,r2,r3,r5,r6")
	assert.Equal(t, 2, dry.ProbableRefsWritten, "the two r4 rows carry probable refs instead")
	assert.Zero(t, dry.ProbableBackfilled, "the stub has no vndb_id, so nothing to backfill")
	assert.Zero(t, dry.AnchorHeldByOther)
	assert.Equal(t, 2, dry.MultiVNUnanchored, "r4 → work A + work B")
	assert.Equal(t, 1, dry.NoDate, "r5 TBA")
	assert.Equal(t, 1, dry.NoTitle, "r6 mtl-only olang")
	assert.Equal(t, 5, dry.KindDefault, "r1,r4A,r4B,r5,r6")
	assert.Equal(t, 1, dry.KindTrial)
	assert.Equal(t, 1, dry.KindPatch)
	assert.Equal(t, int64(0), countWhere(t, `SELECT count(*) FROM catalog_release WHERE extra->>'vndb_id' IS NOT NULL`), "dry writes nothing")

	st, err := New(testDB, nil, Options{}).RunReleases()
	require.NoError(t, err)
	assert.Equal(t, 7, st.ReleasesWritten)
	assert.Equal(t, 5, st.AnchorsWritten)
	assert.Equal(t, 2, st.ProbableRefsWritten, "apply agrees with the dry plan")
	assert.Zero(t, st.AnchorRaceLost)
	assert.Zero(t, st.BatchFailures)
	assert.Zero(t, st.Errors)

	assert.Equal(t, int64(8), countWhere(t, `SELECT count(*) FROM catalog_release`))
	assert.Equal(t, int64(7), countWhere(t, `SELECT count(*) FROM catalog_release WHERE extra->>'vndb_id' IS NOT NULL`))

	assert.Equal(t, int64(1), countWhere(t, `SELECT count(*) FROM catalog_release WHERE id=? AND kind=? AND extra::text='{}'`, stubID, model.ReleaseKindDigital))
	assert.Equal(t, int64(1), countWhere(t, `SELECT count(*) FROM catalog_external_ref WHERE entity_type=6 AND external_id='RJ999' AND source_id=4`))

	assert.Equal(t, int64(5), countWhere(t, `SELECT count(*) FROM catalog_external_ref WHERE entity_type=6 AND source_id=2 AND link_kind=0 AND matched_by='rule:vndb-release-import'`))
	assert.Equal(t, int64(0), countWhere(t, `SELECT count(*) FROM catalog_external_ref WHERE entity_type=6 AND source_id=2 AND external_id='r4' AND link_kind=0`),
		"a multi-vn release never claims the exact slot")
	assert.Equal(t, int64(2), countWhere(t, `SELECT count(*) FROM catalog_external_ref WHERE entity_type=6 AND source_id=2 AND external_id='r4' AND link_kind=1 AND matched_by='rule:vndb-release-import-probable'`))
	assert.Equal(t, int64(0), countWhere(t, `
		SELECT count(*) FROM catalog_release rel
		WHERE rel.extra->>'vndb_id' IS NOT NULL
		  AND NOT EXISTS (SELECT 1 FROM catalog_external_ref r WHERE r.entity_type=6 AND r.entity_id=rel.id AND r.source_id=2)`))

	assert.Equal(t, int64(2), countWhere(t, `SELECT count(*) FROM catalog_release WHERE extra->>'vndb_id'='r4'`))
	assert.Equal(t, int64(1), countWhere(t, `SELECT count(*) FROM catalog_release WHERE extra->>'vndb_id'='r4' AND work_id=?`, workA))
	assert.Equal(t, int64(1), countWhere(t, `SELECT count(*) FROM catalog_release WHERE extra->>'vndb_id'='r4' AND work_id=?`, workB))

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

	assert.Equal(t, "18", scalarStr(t, `SELECT extra->>'minage' FROM catalog_release WHERE extra->>'vndb_id'='r1'`))
	assert.Equal(t, "false", scalarStr(t, `SELECT extra->>'freeware' FROM catalog_release WHERE extra->>'vndb_id'='r1'`))
	assert.Equal(t, "true", scalarStr(t, `SELECT extra->>'official' FROM catalog_release WHERE extra->>'vndb_id'='r1'`))
	assert.Equal(t, `["en", "ja"]`, scalarStr(t, `SELECT extra->'languages' FROM catalog_release WHERE extra->>'vndb_id'='r1'`))
	assert.Equal(t, `["swi", "win"]`, scalarStr(t, `SELECT extra->'platforms' FROM catalog_release WHERE extra->>'vndb_id'='r1'`))

	assert.Equal(t, int64(1), countWhere(t, `SELECT count(*) FROM catalog_release WHERE extra->>'vndb_id'='r2' AND kind=? AND released_y=2021 AND released_m=3 AND released_d IS NULL`, model.ReleaseKindPatch))
	assert.Equal(t, int64(1), countWhere(t, `SELECT count(*) FROM catalog_release WHERE extra->>'vndb_id'='r2' AND extra->>'minage' IS NULL`))
	assert.Equal(t, "0", scalarStr(t, `SELECT extra->>'minage' FROM catalog_release WHERE extra->>'vndb_id'='r3'`))
	assert.Equal(t, int64(1), countWhere(t, `SELECT count(*) FROM catalog_release WHERE extra->>'vndb_id'='r5' AND released_y IS NULL`))
	assert.Equal(t, int64(1), countWhere(t, `SELECT count(*) FROM catalog_release WHERE extra->>'vndb_id'='r6' AND title IS NULL AND lang='en'`))

	assert.Equal(t, int64(7), countWhere(t, `SELECT count(*) FROM catalog_revision WHERE entity_type=6 AND action=?`, model.RevisionActionImported))

	st2, err := New(testDB, nil, Options{}).RunReleases()
	require.NoError(t, err)
	assert.Zero(t, st2.Planned)
	assert.Zero(t, st2.ReleasesWritten)
	assert.Zero(t, st2.AnchorsWritten)
	assert.Zero(t, st2.ProbableRefsWritten)
	assert.Zero(t, st2.ProbableBackfilled, "phase 1 has nothing left to do")
	assert.Zero(t, st2.AnchorHeldByOther, "the wave's own rows are skipped by the ROW index, never re-judged")
	assert.Equal(t, 7, st2.SkippedExisting)
	assert.Equal(t, int64(8), countWhere(t, `SELECT count(*) FROM catalog_release`))
	assert.Equal(t, int64(7), countWhere(t, `SELECT count(*) FROM catalog_external_ref WHERE entity_type=6 AND source_id=2`))
}

// TestVNDBReleasesAnchorHeldByAnotherWork is the wave-202 case that used to take
// the whole apply down on a 23505: upstream now gates a rid through work A, but
// the rid's exact anchor already sits on a release under work B. The ROW index
// (work, rid) honestly says "A has not done this", so the row IS created — but
// the ANCHOR index (rid alone) says the slot is taken, so no exact ref is minted
// and B's anchor is left exactly as it was.
func TestVNDBReleasesAnchorHeldByAnotherWork(t *testing.T) {
	clean(t)
	cleanReleases(t)

	workA := seedVNDBWork(t, "v40")
	workB := seedVNDBWork(t, "v41")

	var holderID int64
	require.NoError(t, testDB.Raw(`INSERT INTO catalog_release (work_id, kind, extra, created_at)
		VALUES (?, 0, '{"vndb_id": "r30"}', now()) RETURNING id`, workB).Scan(&holderID).Error)
	require.NoError(t, testDB.Exec(`INSERT INTO catalog_external_ref (entity_type, entity_id, source_id, external_id, link_kind, matched_by)
		VALUES (6, ?, 2, 'r30', 0, 'rule:vndb-release-import')`, holderID).Error)

	insRelease(t, "r30", "ja", 20200101, nil, false, false, true)
	insReleaseVN(t, "r30", "v40", "complete")
	insReleaseTitle(t, "r30", "ja", false, "移った作品", "")

	staleOut := filepath.Join(t.TempDir(), "stale.tsv")
	dry, err := New(testDB, nil, Options{DryRun: true, StaleAnchorsOut: staleOut}).RunReleases()
	require.NoError(t, err)
	assert.Equal(t, 1, dry.Planned)
	assert.Zero(t, dry.AnchorsWritten, "the slot is not free")
	assert.Equal(t, 1, dry.ProbableRefsWritten)
	assert.Equal(t, 1, dry.AnchorHeldByOther)
	assert.Equal(t, 1, dry.StaleAnchorHolders)
	assert.Zero(t, dry.MultiVNUnanchored, "the skip has its own class, not folded into the multi-vn one")
	assert.Zero(t, dry.Errors)

	body, err := os.ReadFile(staleOut)
	require.NoError(t, err)
	lines := strings.Split(strings.TrimRight(string(body), "\n"), "\n")
	require.Len(t, lines, 2, "header + one row")
	assert.Contains(t, lines[0], "holder_work_vid")
	cols := strings.Split(lines[1], "\t")
	require.Len(t, cols, 10)
	assert.Equal(t, "r30", cols[0])
	assert.Equal(t, "v40", cols[2], "gate work vid")
	assert.Equal(t, "v41", cols[5], "holder work vid")
	assert.Equal(t, "false", cols[6], "the holder is alive")
	assert.Equal(t, "v40", cols[7], "upstream vids")

	st, err := New(testDB, nil, Options{}).RunReleases()
	require.NoError(t, err)
	assert.Equal(t, 1, st.ReleasesWritten, "no error, and the row is still created")
	assert.Zero(t, st.AnchorsWritten)
	assert.Equal(t, 1, st.ProbableRefsWritten)
	assert.Equal(t, 1, st.AnchorHeldByOther)
	assert.Zero(t, st.AnchorRaceLost)
	assert.Zero(t, st.BatchFailures)
	assert.Zero(t, st.Errors)

	assert.Equal(t, int64(1), countWhere(t, `SELECT count(*) FROM catalog_external_ref
		WHERE entity_type=6 AND source_id=2 AND external_id='r30' AND link_kind=0
		  AND entity_id=? AND matched_by='rule:vndb-release-import'`, holderID))
	var newID int64
	require.NoError(t, testDB.Raw(`SELECT id FROM catalog_release WHERE work_id=? AND extra->>'vndb_id'='r30'`, workA).Scan(&newID).Error)
	require.NotZero(t, newID)
	assert.Equal(t, int64(1), countWhere(t, `SELECT count(*) FROM catalog_external_ref
		WHERE entity_type=6 AND entity_id=? AND source_id=2 AND external_id='r30' AND link_kind=1
		  AND matched_by='rule:vndb-release-import-probable'`, newID))

	st2, err := New(testDB, nil, Options{}).RunReleases()
	require.NoError(t, err)
	assert.Zero(t, st2.Planned)
	assert.Equal(t, 1, st2.SkippedExisting)
	assert.Zero(t, st2.AnchorHeldByOther)
	assert.Equal(t, int64(2), countWhere(t, `SELECT count(*) FROM catalog_release WHERE extra->>'vndb_id'='r30'`))
}

func TestVNDBReleasesMultiVNGetsProbableRefs(t *testing.T) {
	clean(t)
	cleanReleases(t)
	seedVNDBWork(t, "v60")
	seedVNDBWork(t, "v61")
	insRelease(t, "r40", "ja", 20200101, nil, false, false, true)
	insReleaseVN(t, "r40", "v60", "complete")
	insReleaseVN(t, "r40", "v61", "complete")
	insReleaseTitle(t, "r40", "ja", false, "合作", "")

	st, err := New(testDB, nil, Options{}).RunReleases()
	require.NoError(t, err)
	assert.Equal(t, 2, st.ReleasesWritten)
	assert.Zero(t, st.AnchorsWritten)
	assert.Equal(t, 2, st.ProbableRefsWritten)
	assert.Equal(t, 2, st.MultiVNUnanchored)
	assert.Zero(t, st.AnchorHeldByOther)
	assert.Equal(t, int64(0), countWhere(t, `SELECT count(*) FROM catalog_external_ref WHERE external_id='r40' AND link_kind=0`))
	assert.Equal(t, int64(2), countWhere(t, `SELECT count(*) FROM catalog_external_ref WHERE entity_type=6 AND source_id=2 AND external_id='r40' AND link_kind=1`))
}

func TestVNDBReleasesProbableBackfill(t *testing.T) {
	clean(t)
	cleanReleases(t)
	work := seedVNDBWork(t, "v50")

	var legacyA, legacyB, anchored int64
	require.NoError(t, testDB.Raw(`INSERT INTO catalog_release (work_id, kind, extra, created_at) VALUES (?, 0, '{"vndb_id": "r50"}', now()) RETURNING id`, work).Scan(&legacyA).Error)
	require.NoError(t, testDB.Raw(`INSERT INTO catalog_release (work_id, kind, extra, created_at) VALUES (?, 0, '{"vndb_id": "r50"}', now()) RETURNING id`, work).Scan(&legacyB).Error)
	require.NoError(t, testDB.Raw(`INSERT INTO catalog_release (work_id, kind, extra, created_at) VALUES (?, 0, '{"vndb_id": "r51"}', now()) RETURNING id`, work).Scan(&anchored).Error)
	require.NoError(t, testDB.Exec(`INSERT INTO catalog_external_ref (entity_type, entity_id, source_id, external_id, link_kind, matched_by)
		VALUES (6, ?, 2, 'r51', 0, 'rule:vndb-release-import')`, anchored).Error)
	require.NoError(t, testDB.Exec(`INSERT INTO catalog_release (work_id, kind, extra, created_at) VALUES (?, 1, '{}', now())`, work).Error)

	dry, err := New(testDB, nil, Options{DryRun: true}).RunReleases()
	require.NoError(t, err)
	assert.Equal(t, 2, dry.ProbableBackfilled, "the two anchorless r50 rows")
	assert.Equal(t, int64(1), countWhere(t, `SELECT count(*) FROM catalog_external_ref WHERE entity_type=6 AND source_id=2`), "dry writes nothing")

	st, err := New(testDB, nil, Options{}).RunReleases()
	require.NoError(t, err)
	assert.Equal(t, 2, st.ProbableBackfilled)
	assert.Equal(t, int64(2), countWhere(t, `SELECT count(*) FROM catalog_external_ref
		WHERE entity_type=6 AND source_id=2 AND external_id='r50' AND link_kind=1 AND matched_by='rule:vndb-release-backfill'`))
	assert.Equal(t, int64(1), countWhere(t, `SELECT count(*) FROM catalog_external_ref
		WHERE entity_type=6 AND entity_id=? AND source_id=2 AND external_id='r51' AND link_kind=0 AND matched_by='rule:vndb-release-import'`, anchored),
		"an already-anchored row is untouched")

	st2, err := New(testDB, nil, Options{}).RunReleases()
	require.NoError(t, err)
	assert.Zero(t, st2.ProbableBackfilled, "idempotent: nothing left anchorless")
	assert.Equal(t, int64(3), countWhere(t, `SELECT count(*) FROM catalog_external_ref WHERE entity_type=6 AND source_id=2`))
}

func TestVNDBReleasesLimit(t *testing.T) {
	clean(t)
	cleanReleases(t)
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
		{20210300, 2021, i16(3), nil, true},
		{20190000, 2019, nil, nil, true},
		{99999999, 0, nil, nil, false},
		{19000101, 0, nil, nil, false},
		{20200015, 2020, nil, nil, true},
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
