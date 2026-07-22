package importer

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// seedSubject inserts one src_bangumi.subject (type=4) with the boilerplate
// NOT-NULL columns defaulted; metaTags / tags are raw jsonb text ("" → NULL).
func seedSubject(t *testing.T, id int64, name, nameCN, metaTags, tags string, nsfw bool) {
	t.Helper()
	mt := "NULL"
	if metaTags != "" {
		mt = "'" + metaTags + "'::jsonb"
	}
	tg := "NULL"
	if tags != "" {
		tg = "'" + tags + "'::jsonb"
	}
	require.NoError(t, testDB.Exec(`INSERT INTO src_bangumi.subject
		(id, type, name, name_cn, infobox_raw, parse_error, platform, summary, nsfw, date, series, score, rank, meta_tags, tags, parser_version, ingested_at)
		VALUES (?,4,?,?,'','',4001,'',?, '', false, 0, 0, `+mt+`, `+tg+`, 'v', now())`,
		id, name, nameCN, nsfw).Error)
}

func TestBgmType4GatedWave(t *testing.T) {
	clean(t)
	// eg staging needs a gamename column for the cross-source corpus query.
	require.NoError(t, testDB.Exec(`ALTER TABLE games ADD COLUMN IF NOT EXISTS gamename text`).Error)

	// --- gated subjects (created) ---
	seedSubject(t, 1001, "絶対×小夜曲エターナル", "", `["PC","VN","游戏","R18"]`, "", true)                     // P: PC+VN, nsfw→R18
	seedSubject(t, 1002, "青空アドベンチャーPCゲーム", "", `["PC","ADV","R18","游戏"]`, "", false)                // P: PC+ADV+age-rated
	seedSubject(t, 1005, "明示ギャルゲー作品", "", `["Galgame","PC","游戏"]`, "", false)                       // T: curated Galgame
	seedSubject(t, 1006, "同人ゲーム民間伝承作品", "", `["PC","游戏"]`, `[{"name":"galgame","count":5}]`, false) // T: folksonomy ≥3
	seedSubject(t, 1008, "クロスソース限定作品", "", `["PC","游戏"]`, "", false)                                // X: cross-source only
	seedSubject(t, 1014, "", "中文限定galgame作品", `["Galgame","PC","游戏"]`, "", false)                   // T + Chinese-only

	// --- NOT gated ---
	seedSubject(t, 1003, "西方冒険物語ADV作品", "", `["PC","ADV","游戏"]`, "", false)                       // ADV without age-rating → not P
	seedSubject(t, 1004, "鉄砲撃ちまくりオンライン", "", `["PC","FPS","游戏"]`, "", false)                      // bare PC (TF2 class)
	seedSubject(t, 1007, "弱票同人ゲーム作品", "", `["PC","游戏"]`, `[{"name":"galgame","count":2}]`, false) // folksonomy <3

	// --- console/mobile-exclusive guard (excluded even though Galgame-tagged) ---
	seedSubject(t, 1009, "家庭用専売乙女ゲーム", "主机专卖乙女", `["Galgame","NS","游戏"]`, "", false)

	// --- existing-work title collision (gated by T, skipped) ---
	seedSubject(t, 1010, "既存衝突タイトル作品", "", `["Galgame","PC","游戏"]`, "", false)
	seedExistingWork(t, "既存衝突タイトル作品")

	// --- intra-batch collision (two same-title gated subjects, both skipped) ---
	seedSubject(t, 1011, "重複同名ゲーム作品", "", `["Galgame","PC","游戏"]`, "", false)
	seedSubject(t, 1012, "重複同名ゲーム作品", "", `["Galgame","PC","游戏"]`, "", false)

	// --- already-anchored subject (out of pool, idempotency) ---
	seedSubject(t, 1013, "既に錨定済み作品", "", `["Galgame","PC","游戏"]`, "", false)
	seedAnchoredWork(t, 1013)

	// --- cross-source corpus: a DLsite GAME title equal to subject 1008's name ---
	require.NoError(t, testDB.Exec(`INSERT INTO works (workno, work_name, work_type_string, status)
		VALUES ('RJ900','クロスソース限定作品','アドベンチャー','fetched')`).Error)

	// === dry-run survey ===
	dry, err := New(testDB, testDB, Options{DryRun: true}).RunBgmType4Gated(testDB)
	require.NoError(t, err)
	assert.Equal(t, 13, dry.PoolTotal, "S1..S12 + S14 (S13 anchored, out of pool)")
	assert.Equal(t, 1, dry.ExcludedConsoleMobile, "S9 console-exclusive")
	assert.Equal(t, 12, dry.EligiblePool)
	assert.Equal(t, 2, dry.SigP)
	assert.Equal(t, 6, dry.SigT, "S5,S6,S10,S11,S12,S14")
	assert.Equal(t, 1, dry.SigX, "S8")
	assert.Equal(t, 9, dry.GatedTotal)
	assert.Equal(t, 1, dry.SkippedTitleCollision, "S10")
	assert.Equal(t, 2, dry.SkippedIntraCollision, "S11+S12")
	assert.Equal(t, 6, dry.ToCreate)
	assert.Zero(t, dry.WorksCreated, "dry writes nothing")
	assert.Len(t, dry.CollisionSamples, 1)
	assert.Equal(t, int64(1010), dry.CollisionSamples[0].SubjectID)

	// === apply ===
	st, err := New(testDB, testDB, Options{}).RunBgmType4Gated(testDB)
	require.NoError(t, err)
	assert.Equal(t, 6, st.WorksCreated)
	assert.Equal(t, 6, st.AnchorsCreated)
	assert.Equal(t, 6, st.RevisionsCreated)
	assert.Equal(t, 6, st.TitlesCreated, "5 ja-only works + S14 zh-Hans-only = 6 title rows")

	// Every minted work is bodyless medium=galgame + exact anchor + imported rev.
	assert.Equal(t, int64(6), scalarInt(t, `SELECT count(*) FROM catalog_work w
		JOIN catalog_external_ref e ON e.entity_id=w.id AND e.entity_type=5 AND e.matched_by='rule:bgm-type4-gated' AND e.link_kind=0
		WHERE w.medium_id=1 AND w.site IS NULL AND w.status=0`))
	assert.Equal(t, int64(6), scalarInt(t, `SELECT count(*) FROM catalog_revision WHERE entity_type=5 AND action=5 AND revision=1
		AND entity_id IN (SELECT entity_id FROM catalog_external_ref WHERE matched_by='rule:bgm-type4-gated')`))

	// S1: nsfw → R18, ja official title.
	assert.Equal(t, int64(1), scalarInt(t, `SELECT count(*) FROM catalog_work w
		JOIN catalog_external_ref e ON e.entity_id=w.id AND e.external_id='1001' AND e.matched_by='rule:bgm-type4-gated'
		WHERE w.content_rating=2 AND w.olang='ja'`))
	assert.Equal(t, int64(1), scalarInt(t, `SELECT count(*) FROM catalog_work_title t
		JOIN catalog_external_ref e ON e.entity_id=t.work_id AND e.external_id='1001' AND e.matched_by='rule:bgm-type4-gated'
		WHERE t.lang='ja' AND t.kind=0 AND t.title='絶対×小夜曲エターナル'`))
	// S14: Chinese-only → olang zh-Hans, single zh-Hans title, no ja title.
	assert.Equal(t, int64(1), scalarInt(t, `SELECT count(*) FROM catalog_work w
		JOIN catalog_external_ref e ON e.entity_id=w.id AND e.external_id='1014' AND e.matched_by='rule:bgm-type4-gated'
		WHERE w.olang='zh-Hans'`))
	assert.Equal(t, int64(1), scalarInt(t, `SELECT count(*) FROM catalog_work_title t
		JOIN catalog_external_ref e ON e.entity_id=t.work_id AND e.external_id='1014' AND e.matched_by='rule:bgm-type4-gated'
		WHERE t.lang='zh-Hans' AND t.title='中文限定galgame作品'`))
	// The collision subject (1010) and the console-exclusive (1009) were NOT created.
	assert.Zero(t, scalarInt(t, `SELECT count(*) FROM catalog_external_ref WHERE matched_by='rule:bgm-type4-gated' AND external_id IN ('1009','1010','1011','1012','1013','1003','1004','1007')`))

	// === idempotency: second apply writes zero ===
	st2, err := New(testDB, testDB, Options{}).RunBgmType4Gated(testDB)
	require.NoError(t, err)
	assert.Zero(t, st2.WorksCreated)
	assert.Zero(t, st2.ToCreate)
	assert.Equal(t, 6, dry.ToCreate) // dry snapshot unchanged
}

// TestBgmType4GatedASCIITightening covers the reviewer's X-only pure-non-CJK
// single-corpus drop: a pure-ASCII X-only candidate is admitted ONLY if it hits
// ≥2 corpora; CJK titles and anything with P/T support are unaffected.
func TestBgmType4GatedASCIITightening(t *testing.T) {
	clean(t)
	require.NoError(t, testDB.Exec(`ALTER TABLE games ADD COLUMN IF NOT EXISTS gamename text`).Error)
	require.NoError(t, testDB.Exec(`TRUNCATE src_vndb.releases_titles`).Error)

	// A1: pure-ASCII, X-only, ONE corpus (dlsite) → DROPPED.
	seedSubject(t, 2001, "Manhunt Generic Title", "", `["PC","游戏"]`, "", false)
	seedDLsiteGame(t, "Manhunt Generic Title")
	// A2: pure-ASCII, X-only, TWO corpora (eg + vndb) → SURVIVOR (created).
	seedSubject(t, 2002, "Ascii Multi Corpus Game", "", `["PC","游戏"]`, "", false)
	seedEGGame(t, 9002, "Ascii Multi Corpus Game")
	seedVNDBTitle(t, "r2", "Ascii Multi Corpus Game")
	// A3: CJK title, X-only, ONE corpus → unaffected (created).
	seedSubject(t, 2003, "日本語限定タイトル作品", "", `["PC","游戏"]`, "", false)
	seedDLsiteGame(t, "日本語限定タイトル作品")
	// A4: pure-ASCII, ONE corpus, but P support → unaffected (created).
	seedSubject(t, 2004, "Ascii With P Support", "", `["PC","VN","游戏"]`, "", false)
	seedDLsiteGame(t, "Ascii With P Support")
	// A5: pure-ASCII, ONE corpus, but T support → unaffected (created).
	seedSubject(t, 2005, "Ascii With T Support", "", `["Galgame","PC","游戏"]`, "", false)
	seedDLsiteGame(t, "Ascii With T Support")

	dry, err := New(testDB, testDB, Options{DryRun: true}).RunBgmType4Gated(testDB)
	require.NoError(t, err)
	assert.Equal(t, 5, dry.EligiblePool)
	assert.Equal(t, 1, dry.SkippedASCIIXOnly, "A1 dropped")
	assert.Equal(t, 4, dry.SigX, "A2,A3,A4,A5 keep effective X; A1 dropped")
	assert.Equal(t, 4, dry.GatedTotal, "A2,A3,A4,A5")
	assert.Equal(t, 4, dry.ToCreate)
	require.Len(t, dry.ASCIIDroppedSamples, 1)
	assert.Equal(t, int64(2001), dry.ASCIIDroppedSamples[0].SubjectID)
	require.Len(t, dry.ASCIISurvivorSamples, 1)
	assert.Equal(t, int64(2002), dry.ASCIISurvivorSamples[0].SubjectID)

	st, err := New(testDB, testDB, Options{}).RunBgmType4Gated(testDB)
	require.NoError(t, err)
	assert.Equal(t, 4, st.WorksCreated)
	// A1 (2001) NOT created; A2/A3/A4/A5 created.
	assert.Zero(t, scalarInt(t, `SELECT count(*) FROM catalog_external_ref WHERE matched_by='rule:bgm-type4-gated' AND external_id='2001'`))
	assert.Equal(t, int64(4), scalarInt(t, `SELECT count(*) FROM catalog_external_ref WHERE matched_by='rule:bgm-type4-gated' AND external_id IN ('2002','2003','2004','2005')`))
}

func seedDLsiteGame(t *testing.T, name string) {
	t.Helper()
	require.NoError(t, testDB.Exec(`INSERT INTO works (workno, work_name, work_type_string, status)
		VALUES (?, ?, 'アドベンチャー', 'fetched')`, "RJ"+name, name).Error)
}

func seedEGGame(t *testing.T, id int64, name string) {
	t.Helper()
	require.NoError(t, testDB.Exec(`INSERT INTO games (id, gamename) VALUES (?, ?)`, id, name).Error)
}

func seedVNDBTitle(t *testing.T, id, title string) {
	t.Helper()
	require.NoError(t, testDB.Exec(`INSERT INTO src_vndb.releases_titles (id, lang, mtl, title, latin)
		VALUES (?, 'ja', false, ?, '')`, id, title).Error)
}

// seedExistingWork creates a minimal bodyless work carrying one official title,
// so the gate's collision safety rope finds an existing title to skip against.
func seedExistingWork(t *testing.T, title string) {
	t.Helper()
	var wid int64
	require.NoError(t, testDB.Raw(`INSERT INTO catalog_work (medium_id, olang, display_name, content_rating, status, extra, field_provenance)
		VALUES (1,'ja',?,0,0,'{}','{}') RETURNING id`, title).Scan(&wid).Error)
	require.NoError(t, testDB.Exec(`INSERT INTO catalog_work_title (work_id, lang, title, kind) VALUES (?,'ja',?,0)`, wid, title).Error)
}
