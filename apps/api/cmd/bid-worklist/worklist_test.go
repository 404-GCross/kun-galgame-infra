package main

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"api/internal/platform/catalog/llmsuggest"
	catmigrate "api/internal/platform/catalog/migrate"
	catseed "api/internal/platform/catalog/seed"
	srcb "api/internal/platform/catalog/srcbangumi"
	gmodel "api/internal/platform/galgame/model"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	glog "gorm.io/gorm/logger"
)

var testDB *gorm.DB

func TestMain(m *testing.M) {
	dsn := os.Getenv("TEST_DATABASE_DSN")
	if dsn == "" {
		dsn = "host=localhost port=5432 user=postgres password=postgres dbname=kun_catalog_test sslmode=disable"
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: glog.Default.LogMode(glog.Silent)})
	if err != nil {
		fmt.Fprintf(os.Stderr, "SKIP: db connect: %v\n", err)
		os.Exit(0)
	}
	for _, step := range []func() error{
		func() error { return catmigrate.Run(db) },
		func() error { return catseed.Run(db) },
		func() error { return llmsuggest.EnsureSchema(db) },
		func() error { return srcb.EnsureSchema(db) },
		func() error {
			return db.AutoMigrate(&gmodel.Galgame{}, &gmodel.GalgameAlias{}, &gmodel.GalgameRevision{}, &gmodel.GalgameBangumiMeta{})
		},
		func() error {
			return db.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS idx_galgame_alias_unique ON galgame_alias (galgame_id, name)`).Error
		},
	} {
		if err := step(); err != nil {
			fmt.Fprintf(os.Stderr, "SKIP: setup: %v\n", err)
			os.Exit(0)
		}
	}
	testDB = db
	os.Exit(m.Run())
}

func cleanTables(t *testing.T) {
	t.Helper()
	for _, tbl := range []string{
		"src_llm.bid_identity_verdict", "src_bangumi.subject",
		"catalog_external_ref", "catalog_match_rejection", "catalog_work",
		"galgame_bangumi_meta", "galgame_alias", "galgame_revision", "galgame",
	} {
		require.NoError(t, testDB.Exec("TRUNCATE "+tbl+" RESTART IDENTITY CASCADE").Error)
	}
}

// fixture seeds one enriched galgame + its bangumi subject + verdict + catalog
// work/ref. nameEnriched: galgame.name_zh_cn == subject.name_cn (safe to revert)
// unless overridden by handEdit.
func fixture(t *testing.T, bid int64, subjectNameCN, subjectSummary, aliasInfobox, liveNameZhCN, liveIntro string) (verdictID, galgameID, workID int64) {
	t.Helper()
	galgameID, workID = 100, 500
	// catalog work + bangumi exact ref
	require.NoError(t, testDB.Exec(`INSERT INTO catalog_work (id, medium_id, olang, display_name, content_rating, status, extra, field_provenance)
		VALUES (?, 1, 'ja', 'W', 0, 0, '{}', '{}')`, workID).Error)
	require.NoError(t, testDB.Exec(`INSERT INTO catalog_external_ref (entity_type, entity_id, source_id, external_id, link_kind, matched_by)
		VALUES (5, ?, 3, ?, 0, 'rule:wiki-bid-typed')`, workID, itoa(bid)).Error)
	// bangumi subject (the enrichment source), type=4 game
	infobox := fmt.Sprintf(`{"Fields":[{"Key":"别名","Items":[{"Value":%q}]}]}`, aliasInfobox)
	require.NoError(t, testDB.Exec(`INSERT INTO src_bangumi.subject
		(id, type, name, name_cn, infobox_raw, parse_error, platform, summary, nsfw, date, series, score, rank, parser_version, ingested_at, infobox_parsed, tags, score_details)
		VALUES (?, 4, 'JPName', ?, '', '', 0, ?, false, '', false, 0, 0, 'v', now(), ?::jsonb, '[]'::jsonb, '{}'::jsonb)`,
		bid, subjectNameCN, subjectSummary, infobox).Error)
	// wiki galgame (enriched) + a latest revision snapshot + one appended alias + meta
	require.NoError(t, testDB.Exec(`INSERT INTO galgame (id, vndb_id, bid, name_en_us, name_ja_jp, name_zh_cn, name_zh_tw, intro_en_us, intro_ja_jp, intro_zh_cn, intro_zh_tw, status, user_id)
		VALUES (?, 'v1', ?, '', 'JPName', ?, '', '', '', ?, '', 0, 1)`, galgameID, bid, liveNameZhCN, liveIntro).Error)
	require.NoError(t, testDB.Exec(`INSERT INTO galgame_revision (galgame_id, revision, action, user_id, snapshot, changed_fields, is_minor)
		VALUES (?, 1, 'created', 1, ?::jsonb, '[]'::jsonb, false)`, galgameID,
		fmt.Sprintf(`{"name_zh_cn": %q, "intro_zh_cn": %q, "aliases": [%q]}`, liveNameZhCN, liveIntro, subjectNameCN)).Error)
	require.NoError(t, testDB.Exec(`INSERT INTO galgame_alias (galgame_id, name, created, updated) VALUES (?, ?, now(), now())`, galgameID, subjectNameCN).Error)
	require.NoError(t, testDB.Exec(`INSERT INTO galgame_bangumi_meta (galgame_id, bid, score, rank, total, nsfw, synced_at) VALUES (?, ?, 8, 1, 0, false, now())`, galgameID, bid).Error)
	// verdict row (different)
	require.NoError(t, testDB.Exec(`INSERT INTO src_llm.bid_identity_verdict (work_id, galgame_id, bid, layer, verdict, wiki_names, subject_names, input_hash, model, prompt_version, confidence)
		VALUES (?, ?, ?, 'suspect', 'different', 'wikiN', 'subjN', ?, 'm', 'p', 0.9)`, workID, galgameID, bid, "h"+itoa(bid)).Error)
	require.NoError(t, testDB.Raw(`SELECT id FROM src_llm.bid_identity_verdict WHERE galgame_id=?`, galgameID).Scan(&verdictID).Error)
	return verdictID, galgameID, workID
}

func writeReceipt(t *testing.T, vid int64, decision string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "r.tsv")
	require.NoError(t, os.WriteFile(p, fmt.Appendf(nil, "verdict_id\tdecision\tnotes\n%d\t%s\ttest\n", vid, decision), 0o644))
	return p
}

// TestWrongChainSafeRevert: name enrichment-equal → reverted; intro hand-edited
// → skipped; alias removed; bid cleared; meta deleted; ref revoked; rejection
// written; resolution recorded.
func TestWrongChainSafeRevert(t *testing.T) {
	cleanTables(t)
	// name_zh_cn == subject name_cn (safe) ; intro hand-edited (≠ summary).
	vid, gid, wid := fixture(t, 13, "克拉纳德", "克拉纳德简介", "AFTER", "克拉纳德", "用户手改的简介")
	st, err := runApply(testDB, testDB, writeReceipt(t, vid, "wrong"), true)
	require.NoError(t, err)
	assert.Equal(t, 1, st.Wrong)
	assert.Equal(t, 1, st.FieldsReverted) // name reverted
	assert.Equal(t, 1, st.FieldsSkipped)  // intro hand-edited
	assert.Equal(t, 1, st.RefsRevoked)
	assert.Equal(t, 1, st.RejectionsWritten)

	var g struct {
		BID       *int64 `gorm:"column:bid"`
		NameZhCN  string `gorm:"column:name_zh_cn"`
		IntroZhCN string `gorm:"column:intro_zh_cn"`
	}
	testDB.Raw(`SELECT bid, name_zh_cn, intro_zh_cn FROM galgame WHERE id=?`, gid).Scan(&g)
	assert.Nil(t, g.BID, "bid cleared (blocks reconcile re-attach)")
	assert.Equal(t, "", g.NameZhCN, "safe revert to empty")
	assert.Equal(t, "用户手改的简介", g.IntroZhCN, "hand-edited intro NOT clobbered")
	assert.Zero(t, scalar(t, `SELECT count(*) FROM galgame_bangumi_meta WHERE galgame_id=?`, gid))
	assert.Zero(t, scalar(t, `SELECT count(*) FROM galgame_alias WHERE galgame_id=? AND name='克拉纳德'`, gid))
	assert.Zero(t, scalar(t, `SELECT count(*) FROM catalog_external_ref WHERE entity_id=? AND source_id=3`, wid), "ref revoked")
	assert.Equal(t, int64(1), scalar(t, `SELECT count(*) FROM catalog_match_rejection WHERE entity_id=? AND source_id=3 AND external_id='13'`, wid), "negative knowledge")
	assert.Equal(t, "wrong", scalarStr(t, `SELECT resolution FROM src_llm.bid_identity_verdict WHERE id=?`, vid))

	// Idempotency: re-run the same receipt → already-resolved, zero new work.
	st2, err := runApply(testDB, testDB, writeReceipt(t, vid, "wrong"), true)
	require.NoError(t, err)
	assert.Equal(t, 1, st2.Already)
	assert.Equal(t, 0, st2.Wrong)
}

// TestOKZeroWrite: ok decision touches nothing but the resolution.
func TestOKZeroWrite(t *testing.T) {
	cleanTables(t)
	vid, gid, _ := fixture(t, 20, "名", "简介", "AL", "名", "简介")
	st, err := runApply(testDB, testDB, writeReceipt(t, vid, "ok"), true)
	require.NoError(t, err)
	assert.Equal(t, 1, st.OK)
	assert.Equal(t, 0, st.RefsRevoked)
	// galgame untouched.
	assert.Equal(t, int64(20), scalar(t, `SELECT bid FROM galgame WHERE id=?`, gid))
	assert.Equal(t, "ok", scalarStr(t, `SELECT resolution FROM src_llm.bid_identity_verdict WHERE id=?`, vid))
}

// TestSmear: identity is right, aliases smeared → resolution recorded (for the
// --export-smears cleanup queue), no rollback.
func TestSmear(t *testing.T) {
	cleanTables(t)
	vid, gid, _ := fixture(t, 40, "名", "简介", "AL", "名", "简介")
	st, err := runApply(testDB, testDB, writeReceipt(t, vid, "smear"), true)
	require.NoError(t, err)
	assert.Equal(t, 1, st.Smear)
	assert.Equal(t, int64(40), scalar(t, `SELECT bid FROM galgame WHERE id=?`, gid), "smear leaves identity intact")
	assert.Equal(t, "smear", scalarStr(t, `SELECT resolution FROM src_llm.bid_identity_verdict WHERE id=?`, vid))
	// --export-smears surfaces it.
	p := filepath.Join(t.TempDir(), "s.tsv")
	require.NoError(t, runExportSmears(testDB, p))
	b, _ := os.ReadFile(p)
	assert.Contains(t, string(b), itoa(gid))
}

// TestDryZeroWrite: dry-run writes nothing.
func TestDryZeroWrite(t *testing.T) {
	cleanTables(t)
	vid, gid, _ := fixture(t, 30, "N", "S", "A", "N", "hand")
	_, err := runApply(testDB, testDB, writeReceipt(t, vid, "wrong"), false)
	require.NoError(t, err)
	assert.Equal(t, int64(30), scalar(t, `SELECT bid FROM galgame WHERE id=?`, gid), "dry: bid untouched")
	assert.Zero(t, scalar(t, `SELECT count(*) FROM catalog_match_rejection`), "dry: no rejection")
	assert.Equal(t, "", scalarStr(t, `SELECT resolution FROM src_llm.bid_identity_verdict WHERE id=?`, vid), "dry: unresolved")
}

// TestWrongRebid: correct bid given → old revoked, new bid set, fill-empty, new ref.
func TestWrongRebid(t *testing.T) {
	cleanTables(t)
	vid, gid, wid := fixture(t, 13, "克拉纳德", "sum", "AL", "克拉纳德", "手改")
	// the corrected subject (type=4 game).
	require.NoError(t, testDB.Exec(`INSERT INTO src_bangumi.subject
		(id, type, name, name_cn, infobox_raw, parse_error, platform, summary, nsfw, date, series, score, rank, parser_version, ingested_at, infobox_parsed, tags, score_details)
		VALUES (77, 4, 'Right', '正确中文名', '', '', 0, '正确简介', false, '', false, 0, 0, 'v', now(), '{}'::jsonb, '[]'::jsonb, '{}'::jsonb)`).Error)
	st, err := runApply(testDB, testDB, writeReceipt(t, vid, "wrong:77"), true)
	require.NoError(t, err)
	assert.Equal(t, 1, st.WrongRebid)
	assert.Equal(t, int64(77), scalar(t, `SELECT bid FROM galgame WHERE id=?`, gid), "bid corrected")
	// old ref revoked + rejected; new ref added.
	assert.Equal(t, int64(1), scalar(t, `SELECT count(*) FROM catalog_match_rejection WHERE external_id='13'`), "old bid rejected")
	assert.Equal(t, int64(1), scalar(t, `SELECT count(*) FROM catalog_external_ref WHERE entity_id=? AND source_id=3 AND external_id='77'`, wid), "new exact ref")
	// name_zh_cn was reverted to '' then re-filled from the new subject.
	assert.Equal(t, "正确中文名", scalarStr(t, `SELECT name_zh_cn FROM galgame WHERE id=?`, gid))
}

func scalar(t *testing.T, q string, args ...any) int64 {
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
