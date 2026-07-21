package importer

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// insertSubject inserts a src_bangumi.subject row with the NOT NULL columns filled.
func insertSubject(t *testing.T, id int64, stype, platform int, name, nameCN string) {
	t.Helper()
	require.NoError(t, testDB.Exec(`INSERT INTO src_bangumi.subject
		(id, type, name, name_cn, infobox_raw, parse_error, platform, summary, nsfw, date, series, score, rank, parser_version, ingested_at)
		VALUES (?, ?, ?, ?, '', '', ?, '', false, '', false, 0, 0, 'v', now())`,
		id, stype, name, nameCN, platform).Error)
}

// TestBangumiXmedia exercises the evidence gate (only anchored-galgame-reachable
// subjects register), the medium routing (anime/manga/novel + art-book skip),
// the adaptation edge direction (cross-media work adaptation_of galgame),
// bidirectional 改编 dedup, and idempotency.
func TestBangumiXmedia(t *testing.T) {
	if testDB == nil {
		t.Skip("no test db")
	}
	clean(t)

	w100 := seedAnchoredWork(t, 100) // an anchored galgame

	insertSubject(t, 200, 2, 0, "アニメ200", "动画200")     // anime
	insertSubject(t, 201, 1, 1001, "漫画201", "漫画201cn") // book platform 漫画 → manga
	insertSubject(t, 202, 1, 1002, "小説202", "")        // book platform 小说 → novel
	insertSubject(t, 203, 1, 1003, "画集203", "")        // book platform 画集 → skipped
	insertSubject(t, 300, 2, 0, "無関係アニメ", "")          // reachable from NO galgame

	require.NoError(t, testDB.Exec(`INSERT INTO src_bangumi.subject_relation (subject_id, relation_type, related_subject_id, item_order) VALUES
		(100, 1, 200, 0), (200, 1, 100, 0),  -- anime, both directions (bidirectional 改编) → one edge
		(100, 1, 201, 0),                     -- manga (galgame is subject)
		(202, 1, 100, 0),                     -- novel (galgame is the related side)
		(100, 1, 203, 0)`).Error) // art book → skipped_platform, no edge

	// Dry.
	dry, err := New(testDB, nil, Options{DryRun: true}).RunBangumiXmedia()
	require.NoError(t, err)
	assert.Equal(t, 1, dry.RegisteredAnime)
	assert.Equal(t, 1, dry.RegisteredManga)
	assert.Equal(t, 1, dry.RegisteredNovel)
	assert.Equal(t, 1, dry.SkippedPlatform, "画集 platform is not manga/novel")
	assert.Equal(t, 3, dry.Edges, "anime + manga + novel; the art book has no edge")
	assert.Zero(t, dry.EdgesWritten)
	assert.Zero(t, scalarInt(t, `SELECT count(*) FROM catalog_work WHERE medium_id IN (2,3,4)`), "dry writes no works")

	// Run.
	st, err := New(testDB, nil, Options{}).RunBangumiXmedia()
	require.NoError(t, err)
	assert.Equal(t, 3, st.EdgesWritten)

	// Registered works carry the bid self-anchor + right medium; 300 is absent.
	assert.Equal(t, int64(1), scalarInt(t, `SELECT count(*) FROM catalog_work w JOIN catalog_external_ref r ON r.entity_type=5 AND r.entity_id=w.id AND r.source_id=3 AND r.link_kind=0 AND r.matched_by='rule:bangumi-xmedia-import' WHERE w.medium_id=4 AND w.site IS NULL AND r.external_id='200'`), "anime 200 registered + anchored")
	assert.Equal(t, int64(1), scalarInt(t, `SELECT count(*) FROM catalog_work w JOIN catalog_external_ref r ON r.entity_type=5 AND r.entity_id=w.id AND r.source_id=3 AND r.matched_by='rule:bangumi-xmedia-import' WHERE w.medium_id=2 AND r.external_id='201'`), "manga 201")
	assert.Equal(t, int64(1), scalarInt(t, `SELECT count(*) FROM catalog_work w JOIN catalog_external_ref r ON r.entity_type=5 AND r.entity_id=w.id AND r.source_id=3 AND r.matched_by='rule:bangumi-xmedia-import' WHERE w.medium_id=3 AND r.external_id='202'`), "novel 202")
	assert.Zero(t, scalarInt(t, `SELECT count(*) FROM catalog_external_ref WHERE source_id=3 AND matched_by='rule:bangumi-xmedia-import' AND external_id IN ('203','300')`), "art book skipped; unreachable anime not registered")

	// Direction: the anime is the adaptation OF the galgame (anime --adaptation_of--> galgame).
	animeW := scalarInt(t, `SELECT entity_id FROM catalog_external_ref WHERE source_id=3 AND matched_by='rule:bangumi-xmedia-import' AND external_id='200'`)
	assert.Equal(t, int64(1), scalarInt(t, `SELECT count(*) FROM catalog_work_relation WHERE a_work_id=`+itoa64(animeW)+` AND b_work_id=`+itoa64(w100)+` AND relation_type_id=1 AND source_id=3`), "anime adaptation_of galgame")
	assert.Zero(t, scalarInt(t, `SELECT count(*) FROM catalog_work_relation WHERE a_work_id=`+itoa64(w100)+` AND b_work_id=`+itoa64(animeW)+` AND relation_type_id=1`), "not the reverse direction")

	// zh title only added when name_cn differs and is non-empty.
	assert.Equal(t, int64(1), scalarInt(t, `SELECT count(*) FROM catalog_work_title t JOIN catalog_external_ref r ON r.entity_type=5 AND r.entity_id=t.work_id AND r.external_id='200' AND r.matched_by='rule:bangumi-xmedia-import' WHERE t.lang='zh' AND t.title='动画200'`), "zh title for anime 200")

	// Idempotent.
	st2, err := New(testDB, nil, Options{}).RunBangumiXmedia()
	require.NoError(t, err)
	assert.Zero(t, st2.RegisteredAnime+st2.RegisteredManga+st2.RegisteredNovel)
	assert.Zero(t, st2.EdgesWritten)
	assert.Equal(t, 3, st2.AlreadyWork)
	assert.Equal(t, 3, st2.AlreadyEdge)
}
