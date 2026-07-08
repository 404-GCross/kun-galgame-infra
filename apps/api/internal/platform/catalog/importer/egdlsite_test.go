package importer

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"api/internal/platform/catalog/model"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/datatypes"
)

// seedClaimedWork creates a claimed galgame work with one title and returns id.
func seedClaimedWork(t *testing.T, title string) int64 {
	return seedClaimedWorkPW(t, title, 9999)
}

// seedClaimedWorkPW is seedClaimedWork with an explicit product_work_id (so a
// test can seed several claimed works without colliding on (site, pw)).
func seedClaimedWorkPW(t *testing.T, title string, pw int64) int64 {
	t.Helper()
	site := "galgame_wiki"
	w := model.CatalogWork{
		MediumID: mediumGalgame, Site: &site, ProductWorkID: &pw, OLang: "ja",
		DisplayName: title, Status: model.WorkStatusLive,
		Extra: datatypes.JSON(`{}`), FieldProvenance: datatypes.JSON(`{}`),
	}
	require.NoError(t, testDB.Create(&w).Error)
	require.NoError(t, testDB.Create(&model.CatalogWorkTitle{WorkID: w.ID, Lang: "ja", Title: title, Kind: model.WorkTitleKindOfficial}).Error)
	return w.ID
}

// TestEGDLsiteWave exercises the parse-first-then-mint dispatch across all five
// branches plus the VG=publisher mapping and idempotency.
func TestEGDLsiteWave(t *testing.T) {
	if testDB == nil {
		t.Skip("no test db")
	}
	clean(t)

	// A claimed wiki work, reconciled to EG game 100 via the vndb rosetta.
	w := seedClaimedWork(t, "既存タイトル")
	require.NoError(t, testDB.Exec(`INSERT INTO catalog_external_ref
		(entity_type, entity_id, source_id, external_id, link_kind, matched_by)
		VALUES (5, ?, 5, '100', 1, 'rule:eg-vndb-rosetta')`, w).Error)

	// EG games claiming dlsite ids.
	require.NoError(t, testDB.Exec(`INSERT INTO games (id, dlsite_id) VALUES
		(100,'RJ0ATT'), (200,'RJ0MINT'), (300,'RJ0AMB'), (301,'RJ0AMB'), (400,'RJ0MISS')`).Error)

	// DLsite works. RJ0MINT carries a VG maker (commercial → publisher).
	require.NoError(t, testDB.Exec(`INSERT INTO works (workno, work_name, work_name_kana, maker_id, maker_name, age_category, work_type_string, status, product_json) VALUES
		('RJ0ATT','付属作品','','RG100','同人サークル','3','アドベンチャー','fetched','{"creaters":{"voice_by":[{"id":"7001","name":"声優X","classification":"voice_by"}]}}'::jsonb),
		('RJ0MINT','新作','しんさく','VG200','ブランド社','2','ロールプレイング','fetched','{"creaters":{"voice_by":[{"id":"7002","name":"声優Y","classification":"voice_by"}]}}'::jsonb),
		('RJ0AMB','曖昧','','RG300','','1','アドベンチャー','fetched','{"creaters":[]}'::jsonb)`).Error)

	st, err := New(testDB, testDB, Options{}).RunEGDLsite(testDB)
	require.NoError(t, err)
	assert.Equal(t, 1, st.Attached)
	assert.Equal(t, 1, st.Minted)
	assert.Equal(t, 1, st.Ambiguous, "RJ0AMB claimed by two EG games")
	assert.Equal(t, 1, st.Missing, "RJ0MISS not in dlsite staging")
	assert.Zero(t, st.Already)

	// --- attach: the claimed work gained a dlsite release + anchor + edge + credit + search-hint title ---
	assert.Equal(t, int64(1), scalarInt(t, `SELECT count(*) FROM catalog_release WHERE work_id=`+itoa64(w)), "one release attached to the claimed work")
	assert.Equal(t, int64(1), scalarInt(t, `SELECT count(*) FROM catalog_external_ref r JOIN catalog_release rel ON rel.id=r.entity_id
		WHERE rel.work_id=`+itoa64(w)+` AND r.entity_type=6 AND r.source_id=4 AND r.link_kind=0 AND r.matched_by='rule:eg-dlsite-rosetta' AND r.external_id='RJ0ATT'`))
	assert.Equal(t, int64(1), scalarInt(t, `SELECT count(*) FROM catalog_work_label WHERE work_id=`+itoa64(w)), "attribution edge")
	assert.Equal(t, int64(1), scalarInt(t, `SELECT count(*) FROM catalog_credit WHERE work_id=`+itoa64(w)+` AND source_id=4`), "dlsite creater credit")
	assert.Equal(t, int64(1), scalarInt(t, `SELECT count(*) FROM catalog_work_title WHERE work_id=`+itoa64(w)+` AND kind=3 AND title='付属作品'`), "search-hint title for the differing dlsite name")

	// --- mint: a new unclaimed galgame work with a PROBABLE eg ref + release anchor ---
	assert.Equal(t, int64(1), scalarInt(t, `SELECT count(*) FROM catalog_work WHERE medium_id=1 AND site IS NULL AND display_name='新作'`), "minted unclaimed galgame work")
	assert.Equal(t, int64(1), scalarInt(t, `SELECT count(*) FROM catalog_external_ref WHERE entity_type=5 AND source_id=5 AND external_id='200' AND link_kind=1 AND matched_by='rule:eg-dlsite-rosetta'`), "probable EG work-ref")
	assert.Equal(t, int64(1), scalarInt(t, `SELECT count(*) FROM catalog_external_ref WHERE entity_type=6 AND source_id=4 AND external_id='RJ0MINT' AND link_kind=0 AND matched_by='rule:eg-dlsite-rosetta'`), "release SKU anchor")

	// VG maker → publisher label (kind=2).
	assert.Equal(t, int64(1), scalarInt(t, `SELECT count(*) FROM catalog_label l JOIN catalog_external_ref r ON r.entity_type=3 AND r.entity_id=l.id AND r.source_id=4 AND r.external_id='VG200' WHERE l.kind=2`), "VJ commercial maker → publisher label")

	// --- idempotent: a second run re-touches nothing (both worknos now anchored) ---
	st2, err := New(testDB, testDB, Options{}).RunEGDLsite(testDB)
	require.NoError(t, err)
	assert.Equal(t, 2, st2.Already)
	assert.Zero(t, st2.Attached+st2.Minted+st2.ReleasesCreated+st2.EGRefsWritten)
}

// TestEGDLsiteResolveAmbiguous exercises the step-29 three-layer split of a
// dlsite_id claimed by several EG games, keyed on the DISTINCT wiki works its
// claimants resolve to: B1 (one work → attach), B2 (none → mint without an EG
// ref), B3 (several → conflict export, zero auto action). Also asserts the
// default (resolve off) leaves the ambiguous ids untouched, and idempotency.
func TestEGDLsiteResolveAmbiguous(t *testing.T) {
	if testDB == nil {
		t.Skip("no test db")
	}
	clean(t)

	w1 := seedClaimedWorkPW(t, "B1既存", 8001)
	w2 := seedClaimedWorkPW(t, "B3作品A", 8002)
	w3 := seedClaimedWorkPW(t, "B3作品B", 8003)
	rosetta := func(game, work int64) {
		require.NoError(t, testDB.Exec(`INSERT INTO catalog_external_ref
			(entity_type, entity_id, source_id, external_id, link_kind, matched_by)
			VALUES (5, ?, 5, ?, 1, 'rule:eg-vndb-rosetta')`, work, strconv.FormatInt(game, 10)).Error)
	}
	rosetta(500, w1) // B1: only claimant 500 is wiki-matched → one distinct work
	rosetta(700, w2) // B3: claimant 700 → w2
	rosetta(701, w3) // B3: claimant 701 → w3 (a DIFFERENT work)

	require.NoError(t, testDB.Exec(`INSERT INTO games (id, dlsite_id) VALUES
		(500,'RJ0B1'),(501,'RJ0B1'),
		(600,'RJ0B2'),(601,'RJ0B2'),
		(700,'RJ0B3'),(701,'RJ0B3')`).Error)
	require.NoError(t, testDB.Exec(`INSERT INTO works (workno, work_name, work_name_kana, maker_id, maker_name, age_category, work_type_string, status, product_json) VALUES
		('RJ0B1','B1付属','','RG500','','3','アドベンチャー','fetched','{"creaters":[]}'::jsonb),
		('RJ0B2','B2新作','','RG600','','2','アドベンチャー','fetched','{"creaters":[]}'::jsonb),
		('RJ0B3','B3曖昧','','RG700','','1','アドベンチャー','fetched','{"creaters":[]}'::jsonb)`).Error)

	// Default (resolve off): the three ambiguous ids are counted and skipped.
	stOff, err := New(testDB, testDB, Options{}).RunEGDLsite(testDB)
	require.NoError(t, err)
	assert.Equal(t, 3, stOff.Ambiguous)
	assert.Zero(t, stOff.Attached+stOff.Minted+stOff.AmbB1+stOff.AmbB2+stOff.AmbConflicts)
	assert.Zero(t, scalarInt(t, `SELECT count(*) FROM catalog_release WHERE work_id=`+itoa64(w1)), "default off touches nothing")

	// Resolve on.
	confPath := filepath.Join(t.TempDir(), "conf.tsv")
	st, err := New(testDB, testDB, Options{ResolveAmbiguous: true, ConflictsOut: confPath}).RunEGDLsite(testDB)
	require.NoError(t, err)
	assert.Zero(t, st.Ambiguous)
	assert.Equal(t, 1, st.AmbB1, "RJ0B1: one distinct work → attach")
	assert.Equal(t, 1, st.AmbB2, "RJ0B2: no matched claimant → mint")
	assert.Equal(t, 1, st.AmbConflicts, "RJ0B3: two distinct works → conflict")

	// B1: the release attached to w1 with a SKU anchor.
	assert.Equal(t, int64(1), scalarInt(t, `SELECT count(*) FROM catalog_release WHERE work_id=`+itoa64(w1)))
	assert.Equal(t, int64(1), scalarInt(t, `SELECT count(*) FROM catalog_external_ref WHERE entity_type=6 AND source_id=4 AND external_id='RJ0B1' AND link_kind=0 AND matched_by='rule:eg-dlsite-rosetta'`), "B1 SKU anchor")

	// B2: minted unclaimed work carrying a SKU anchor but NO EG probable ref.
	b2work := scalarInt(t, `SELECT id FROM catalog_work WHERE display_name='B2新作' AND site IS NULL`)
	require.NotZero(t, b2work)
	assert.Equal(t, int64(0), scalarInt(t, `SELECT count(*) FROM catalog_external_ref WHERE entity_type=5 AND entity_id=`+itoa64(b2work)+` AND source_id=5`), "B2 mint writes NO EG ref (unattributable)")
	assert.Equal(t, int64(1), scalarInt(t, `SELECT count(*) FROM catalog_external_ref WHERE entity_type=6 AND source_id=4 AND external_id='RJ0B2' AND link_kind=0`), "B2 SKU anchor stands")

	// B3: zero auto action — nothing written for the conflicted SKU; exported.
	assert.Equal(t, int64(0), scalarInt(t, `SELECT count(*) FROM catalog_external_ref WHERE entity_type=6 AND source_id=4 AND external_id='RJ0B3'`), "B3 writes nothing")
	confBytes, err := os.ReadFile(confPath)
	require.NoError(t, err)
	lines := strings.Split(strings.TrimSpace(string(confBytes)), "\n")
	require.Len(t, lines, 2, "header + one conflict row")
	assert.Contains(t, lines[1], "RJ0B3")
	assert.Contains(t, lines[1], itoa64(w2), "conflict row carries both wiki works")
	assert.Contains(t, lines[1], itoa64(w3))

	// Idempotent: a second resolve run re-touches nothing (B1/B2 now anchored);
	// B3 stays a conflict because it was never resolved.
	st2, err := New(testDB, testDB, Options{ResolveAmbiguous: true}).RunEGDLsite(testDB)
	require.NoError(t, err)
	assert.Equal(t, 2, st2.Already, "B1 + B2 worknos now anchored")
	assert.Equal(t, 1, st2.AmbConflicts, "B3 remains a conflict")
	assert.Zero(t, st2.AmbB1+st2.AmbB2)
}

func itoa64(n int64) string {
	return strconv.FormatInt(n, 10)
}
