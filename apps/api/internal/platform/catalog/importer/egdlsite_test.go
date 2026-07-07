package importer

import (
	"strconv"
	"testing"

	"api/internal/platform/catalog/model"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/datatypes"
)

// seedClaimedWork creates a claimed galgame work with one title and returns id.
func seedClaimedWork(t *testing.T, title string) int64 {
	t.Helper()
	site := "galgame_wiki"
	pw := int64(9999)
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

func itoa64(n int64) string {
	return strconv.FormatInt(n, 10)
}
