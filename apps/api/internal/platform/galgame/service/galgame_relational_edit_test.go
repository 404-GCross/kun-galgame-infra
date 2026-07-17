package service

import (
	"context"
	"testing"

	"api/internal/platform/galgame/dto"
	"api/internal/platform/galgame/model"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Regression: a galgame edit must persist tag/official/engine relation
// changes through the snapshot/ApplySnapshot path, the recorded revision
// snapshot must equal the resulting DB state (canonical), and pointer
// presence semantics must hold (nil = keep, []  = clear). Pre-fix,
// PUT /galgame/:gid silently dropped all relation edits AND recorded a
// snapshot taken from the un-mutated relations (corrupt history).

func sp(s string) *string { return &s }

func tagIDsOf(t *testing.T, gid int) []int {
	t.Helper()
	var ids []int
	require.NoError(t, testDB.Model(&model.GalgameTagRelation{}).
		Where("galgame_id = ?", gid).Order("tag_id").Pluck("tag_id", &ids).Error)
	return ids
}

// tagRelations / officialRelations return tag_id|official_id → source for a game.
func tagRelations(t *testing.T, gid int) map[int]string {
	t.Helper()
	var rels []model.GalgameTagRelation
	require.NoError(t, testDB.Where("galgame_id = ?", gid).Find(&rels).Error)
	out := make(map[int]string, len(rels))
	for _, r := range rels {
		out[r.TagID] = r.Source
	}
	return out
}

func officialRelations(t *testing.T, gid int) map[int]string {
	t.Helper()
	var rels []model.GalgameOfficialRelation
	require.NoError(t, testDB.Where("galgame_id = ?", gid).Find(&rels).Error)
	out := make(map[int]string, len(rels))
	for _, r := range rels {
		out[r.OfficialID] = r.Source
	}
	return out
}

// latestRevSnapshot reads the newest revision snapshot through the bridge
// (E2a: writes land in the engine tables; 0 = latest).
func latestRevSnapshot(t *testing.T, gid int) *model.Snapshot {
	t.Helper()
	rev := bridgeRevision(t, gid, 0)
	snap, err := model.SnapshotFromJSON(rev.Snapshot)
	require.NoError(t, err)
	return snap
}

func revCount(t *testing.T, gid int) int64 {
	t.Helper()
	return bridgeRevisionCount(t, gid)
}

func TestUpdate_RelationOverlaySemantics(t *testing.T) {
	cleanTables(t)
	getRepos()
	ctx := context.Background()

	gid := makeGalgame(t) // UserID=1, Status=0
	t1 := createTestTag(t, "t1", "content")
	t2 := createTestTag(t, "t2", "content")
	t3 := createTestTag(t, "t3", "content")
	require.NoError(t, testDB.Create(&model.GalgameTagRelation{GalgameID: gid, TagID: t1}).Error)
	require.NoError(t, testDB.Create(&model.GalgameTagRelation{GalgameID: gid, TagID: t2}).Error)

	// (1) Omitting tag_ids (nil) must NOT touch relations — a name-only
	// edit can't silently wipe tags.
	_, err := testSvc.Update(ctx, 1, gid, nil, &dto.UpdateGalgameRequest{
		NameZhCN: sp("新名字"),
	})
	require.NoError(t, err)
	assert.ElementsMatch(t, []int{t1, t2}, tagIDsOf(t, gid), "nil tag_ids must keep relations")
	// Revision snapshot must reflect actual state (canonical, sorted).
	snap := latestRevSnapshot(t, gid)
	assert.Equal(t, []int{t1, t2}, snap.TagIDs)
	assert.Equal(t, "新名字", snap.NameZhCN)

	// (2) Explicit set = authoritative full replacement.
	newIDs := []int{t3, t2}
	_, err = testSvc.Update(ctx, 1, gid, nil, &dto.UpdateGalgameRequest{
		TagIDs: &newIDs,
	})
	require.NoError(t, err)
	assert.ElementsMatch(t, []int{t2, t3}, tagIDsOf(t, gid))
	assert.Equal(t, []int{t2, t3}, latestRevSnapshot(t, gid).TagIDs, "snapshot canonical-sorted == DB")

	// (3) Explicit empty slice clears all relations.
	empty := []int{}
	_, err = testSvc.Update(ctx, 1, gid, nil, &dto.UpdateGalgameRequest{
		TagIDs: &empty,
	})
	require.NoError(t, err)
	assert.Empty(t, tagIDsOf(t, gid))
	assert.Empty(t, latestRevSnapshot(t, gid).TagIDs)

	// (4) No-op edit (identical empty again, nothing else) produces NO
	// new revision.
	before := revCount(t, gid)
	_, err = testSvc.Update(ctx, 1, gid, nil, &dto.UpdateGalgameRequest{
		TagIDs: &empty,
	})
	require.NoError(t, err)
	assert.Equal(t, before, revCount(t, gid), "no-change edit must not create a revision")
}

func TestUpdate_OfficialAndEngineRelations(t *testing.T) {
	cleanTables(t)
	getRepos()
	ctx := context.Background()

	gid := makeGalgame(t)
	o1 := createTestOfficial(t, "o1", "company")
	e1 := createTestEngine(t, "e1")

	oIDs := []int{o1}
	eIDs := []int{e1}
	_, err := testSvc.Update(ctx, 1, gid, nil, &dto.UpdateGalgameRequest{
		OfficialIDs: &oIDs,
		EngineIDs:   &eIDs,
	})
	require.NoError(t, err)

	var oRel, eRel int64
	testDB.Model(&model.GalgameOfficialRelation{}).Where("galgame_id = ?", gid).Count(&oRel)
	testDB.Model(&model.GalgameEngineRelation{}).Where("galgame_id = ?", gid).Count(&eRel)
	assert.Equal(t, int64(1), oRel)
	assert.Equal(t, int64(1), eRel)

	snap := latestRevSnapshot(t, gid)
	assert.Equal(t, []int{o1}, snap.OfficialIDs)
	assert.Equal(t, []int{e1}, snap.EngineIDs)
}

// mergeUserAndVndbLinks: user links kept, vndb links appended after, echoes
// and info/stats hosts dropped, source="vndb" among user candidates ignored.
func TestMergeUserAndVndbLinks(t *testing.T) {
	vndbLinks := []model.SnapshotLink{
		{Name: "Steam", Link: "https://store.steampowered.com/app/1/", Source: "vndb", SourceKey: "steam"},
		{Name: "DLsite", Link: "https://www.dlsite.com/x", Source: "vndb", SourceKey: "dlsite"},
	}
	user := []model.SnapshotLink{
		{Name: "官网", Link: "https://x.example"},                        // genuine user link → kept
		{Name: "官网", Link: "https://x.example"},                        // exact-duplicate URL → collapsed
		{Name: "Steam", Link: "https://store.steampowered.com/app/1/"}, // echoed managed host → dropped
		{Name: "Wikipedia", Link: "https://en.wikipedia.org/wiki/X"},   // info host → dropped
		{Name: "stale vndb", Link: "https://old", Source: "vndb"},      // source=vndb among user → ignored
	}
	got := mergeUserAndVndbLinks(user, vndbLinks)

	require.Len(t, got, 3)
	assert.Equal(t, "官网", got[0].Name)
	assert.Equal(t, "", got[0].Source, "user link stays unmarked")
	assert.Equal(t, []model.SnapshotLink{vndbLinks[0], vndbLinks[1]}, got[1:], "vndb set appended verbatim, after user links")
}

// released / aliases / links must be editable via the unified PUT path,
// atomically (one edit = one revision), with the snapshot == DB.
func TestUpdate_ReleasedAliasesLinks(t *testing.T) {
	cleanTables(t)
	getRepos()
	ctx := context.Background()
	gid := makeGalgame(t)

	rel := "2019-08-16"
	tba := false
	al := []string{"别名A", "别名B"}
	lk := []dto.GalgameLinkInput{{Name: "官网", Link: "https://x.example"}}
	before := revCount(t, gid)

	_, err := testSvc.Update(ctx, 1, gid, nil, &dto.UpdateGalgameRequest{
		ReleaseDate:    &rel,
		ReleaseDateTBA: &tba,
		Aliases:        &al,
		Links:          &lk,
	})
	require.NoError(t, err)

	snap := latestRevSnapshot(t, gid)
	require.NotNil(t, snap.ReleaseDate)
	assert.Equal(t, "2019-08-16", *snap.ReleaseDate)
	assert.False(t, snap.ReleaseDateTBA)
	assert.ElementsMatch(t, []string{"别名A", "别名B"}, snap.Aliases)
	require.Len(t, snap.Links, 1)
	assert.Equal(t, "官网", snap.Links[0].Name)
	assert.Equal(t, int64(1), revCount(t, gid)-before, "one edit = exactly one revision (atomic)")

	// DB reflects it.
	var aliasCnt, linkCnt int64
	testDB.Model(&model.GalgameAlias{}).Where("galgame_id = ?", gid).Count(&aliasCnt)
	testDB.Model(&model.GalgameLink{}).Where("galgame_id = ?", gid).Count(&linkCnt)
	assert.Equal(t, int64(2), aliasCnt)
	assert.Equal(t, int64(1), linkCnt)

	// Omitting them on a later edit must NOT wipe them (presence).
	nm := "新名"
	_, err = testSvc.Update(ctx, 1, gid, nil, &dto.UpdateGalgameRequest{NameZhCN: &nm})
	require.NoError(t, err)
	s2 := latestRevSnapshot(t, gid)
	assert.ElementsMatch(t, []string{"别名A", "别名B"}, s2.Aliases, "omitted aliases must persist")
	require.Len(t, s2.Links, 1)
	require.NotNil(t, s2.ReleaseDate)
	assert.Equal(t, "2019-08-16", *s2.ReleaseDate)
}

// A user edit replaces only the USER links; the sync-managed source="vndb"
// links (store/official, maintained by the cron) must survive the edit and
// must not duplicate when the client echoes one back. Pre-fix, any edit that
// carried Links wiped every vndb link until the next sync (the cause of the
// backfill's ~10 concurrent-edit casualties).
func TestUpdate_PreservesVndbLinks(t *testing.T) {
	cleanTables(t)
	getRepos()
	ctx := context.Background()
	gid := makeGalgame(t)

	// Simulate the cron sync: a vndb-sourced store link lives in galgame_link.
	require.NoError(t, testDB.Create(&model.GalgameLink{
		GalgameID: gid, UserID: 1,
		Name: "Steam", Link: "https://store.steampowered.com/app/1/",
		Source: "vndb", SourceKey: "steam",
	}).Error)

	// User edits, submitting only their own link.
	lk := []dto.GalgameLinkInput{{Name: "官网", Link: "https://x.example"}}
	_, err := testSvc.Update(ctx, 1, gid, nil, &dto.UpdateGalgameRequest{Links: &lk})
	require.NoError(t, err)

	snap := latestRevSnapshot(t, gid)
	require.Len(t, snap.Links, 2, "user link + preserved vndb link")
	assert.Equal(t, "官网", snap.Links[0].Name)
	assert.Equal(t, "", snap.Links[0].Source)
	assert.Equal(t, "Steam", snap.Links[1].Name)
	assert.Equal(t, "vndb", snap.Links[1].Source)
	assert.Equal(t, "steam", snap.Links[1].SourceKey)

	// DB matches, vndb provenance intact.
	var links []model.GalgameLink
	require.NoError(t, testDB.Where("galgame_id = ?", gid).Order("id").Find(&links).Error)
	require.Len(t, links, 2)
	assert.Equal(t, "vndb", links[1].Source)

	// Client echoes the managed Steam URL back as a plain link → no duplicate,
	// and no new revision (the resulting set is unchanged).
	before := revCount(t, gid)
	lk2 := []dto.GalgameLinkInput{
		{Name: "官网", Link: "https://x.example"},
		{Name: "Steam", Link: "https://store.steampowered.com/app/1/"},
	}
	_, err = testSvc.Update(ctx, 1, gid, nil, &dto.UpdateGalgameRequest{Links: &lk2})
	require.NoError(t, err)
	snap2 := latestRevSnapshot(t, gid)
	require.Len(t, snap2.Links, 2, "echoed vndb link must not duplicate")
	assert.Equal(t, "Steam", snap2.Links[1].Name)
	assert.Equal(t, "vndb", snap2.Links[1].Source)
	assert.Equal(t, before, revCount(t, gid), "echo-only edit changes nothing → no new revision")
}

// Anti-regression invariant: EVERY editable model.Snapshot field must be
// reachable through UpdateGalgameRequest (bid/BangumiID is the only
// reserved exception). If someone adds a snapshot field but forgets the
// DTO/overlay wiring (the exact class of bug this whole effort fixed),
// this fails.
func TestEditableSnapshotFieldsAllReachable(t *testing.T) {
	cleanTables(t)
	getRepos()
	ctx := context.Background()
	gid := makeGalgame(t)
	tg := createTestTag(t, "rt", "content")
	of := createTestOfficial(t, "ro", "company")
	en := createTestEngine(t, "re")

	s := func(v string) *string { return &v }
	b := func(v bool) *bool { return &v }
	releaseDate := "2020-01-01"
	// 64-char sha-256 placeholders for cover / screenshot hashes.
	coverHash := "c1c1c1c1c1c1c1c1c1c1c1c1c1c1c1c1c1c1c1c1c1c1c1c1c1c1c1c1c1c1c1c1"
	shotHash := "5a5a5a5a5a5a5a5a5a5a5a5a5a5a5a5a5a5a5a5a5a5a5a5a5a5a5a5a5a5a5a5a"
	want := &model.Snapshot{
		VNDBID: "v777", ReleaseDate: &releaseDate, ReleaseDateTBA: true,
		NameEnUS: "EN", NameJaJP: "JA", NameZhCN: "ZH", NameZhTW: "TW",
		Banner:    "https://b.example/x.webp",
		IntroEnUS: "ie", IntroJaJP: "ij", IntroZhCN: "iz", IntroZhTW: "it",
		ContentLimit: "nsfw", OriginalLanguage: "en-us", AgeLimit: "r18",
		Aliases: []string{"a1"}, TagIDs: []int{tg}, OfficialIDs: []int{of},
		EngineIDs: []int{en}, Links: []model.SnapshotLink{{Name: "L", Link: "https://l.example"}},
		Covers:      []model.SnapshotCover{{ImageHash: coverHash, SortOrder: 0, Sexual: 1, Violence: 0, Source: "user", SourceKey: ""}},
		Screenshots: []model.SnapshotScreenshot{{ImageHash: shotHash, SortOrder: 0, Caption: "CG 01", Sexual: 2, Violence: 0}},
	}
	al := want.Aliases
	li := []dto.GalgameLinkInput{{Name: "L", Link: "https://l.example"}}
	ti, oi, ei := want.TagIDs, want.OfficialIDs, want.EngineIDs
	cv := []dto.GalgameCoverInput{{ImageHash: coverHash, SortOrder: 0, Sexual: 1, Source: "user"}}
	sh := []dto.GalgameScreenshotInput{{ImageHash: shotHash, SortOrder: 0, Caption: "CG 01", Sexual: 2}}
	// E2a: a vndb_id change now requires galgame.edit_any (squatting lesson) —
	// the reachability sweep runs with staff roles so every field stays covered.
	_, err := testSvc.Update(ctx, 1, gid, []string{"admin"}, &dto.UpdateGalgameRequest{
		VNDBID: s("v777"), ReleaseDate: s("2020-01-01"), ReleaseDateTBA: b(true),
		NameEnUS: s("EN"), NameJaJP: s("JA"), NameZhCN: s("ZH"), NameZhTW: s("TW"),
		Banner:    s("https://b.example/x.webp"),
		IntroEnUS: s("ie"), IntroJaJP: s("ij"), IntroZhCN: s("iz"), IntroZhTW: s("it"),
		ContentLimit: s("nsfw"), OriginalLanguage: s("en-us"), AgeLimit: s("r18"),
		Aliases: &al, Links: &li, TagIDs: &ti, OfficialIDs: &oi, EngineIDs: &ei,
		Covers: &cv, Screenshots: &sh,
	})
	require.NoError(t, err)

	got := latestRevSnapshot(t, gid)
	assert.Equal(t, want.VNDBID, got.VNDBID)
	require.NotNil(t, got.ReleaseDate)
	assert.Equal(t, *want.ReleaseDate, *got.ReleaseDate)
	assert.Equal(t, want.ReleaseDateTBA, got.ReleaseDateTBA)
	assert.Equal(t, want.NameEnUS, got.NameEnUS)
	assert.Equal(t, want.NameJaJP, got.NameJaJP)
	assert.Equal(t, want.NameZhCN, got.NameZhCN)
	assert.Equal(t, want.NameZhTW, got.NameZhTW)
	assert.Equal(t, want.Banner, got.Banner)
	assert.Equal(t, want.IntroEnUS, got.IntroEnUS)
	assert.Equal(t, want.IntroJaJP, got.IntroJaJP)
	assert.Equal(t, want.IntroZhCN, got.IntroZhCN)
	assert.Equal(t, want.IntroZhTW, got.IntroZhTW)
	assert.Equal(t, want.ContentLimit, got.ContentLimit)
	assert.Equal(t, want.OriginalLanguage, got.OriginalLanguage)
	assert.Equal(t, want.AgeLimit, got.AgeLimit)
	assert.ElementsMatch(t, want.Aliases, got.Aliases)
	assert.ElementsMatch(t, want.TagIDs, got.TagIDs)
	assert.ElementsMatch(t, want.OfficialIDs, got.OfficialIDs)
	assert.ElementsMatch(t, want.EngineIDs, got.EngineIDs)
	require.Len(t, got.Links, 1)
	assert.Equal(t, want.Links[0], got.Links[0])
	require.Len(t, got.Covers, 1)
	assert.Equal(t, want.Covers[0], got.Covers[0])
	require.Len(t, got.Screenshots, 1)
	assert.Equal(t, want.Screenshots[0], got.Screenshots[0])
}

// Admin Create now also goes through ApplySnapshot — relations, aliases,
// released default and the auto VNDB link must all materialise, and
// revision 1's snapshot must equal the resulting DB state.
func TestCreate_ThroughApplySnapshot(t *testing.T) {
	cleanTables(t)
	getRepos()
	ctx := context.Background()
	tg := createTestTag(t, "ct", "content")

	g, err := testSvc.Create(ctx, 9, &dto.CreateGalgameRequest{
		VNDBID:   "v424242",
		NameZhCN: "造物主题",
		Aliases:  "x, y",
		TagIDs:   []int{tg},
	})
	require.NoError(t, err)
	require.NotNil(t, g)

	rev := bridgeRevision(t, g.ID, 1) // E2a: bridge read
	snap, err := model.SnapshotFromJSON(rev.Snapshot)
	require.NoError(t, err)
	assert.Equal(t, "v424242", snap.VNDBID)
	assert.Nil(t, snap.ReleaseDate, "no release_date provided → unknown (nil)")
	assert.False(t, snap.ReleaseDateTBA, "no TBA provided → default false")
	assert.ElementsMatch(t, []string{"x", "y"}, snap.Aliases)
	assert.Equal(t, []int{tg}, snap.TagIDs)
	require.Len(t, snap.Links, 1)
	assert.Equal(t, "https://vndb.org/v424242", snap.Links[0].Link)

	// snapshot == DB
	var tagCnt, linkCnt int64
	testDB.Model(&model.GalgameTagRelation{}).Where("galgame_id = ?", g.ID).Count(&tagCnt)
	testDB.Model(&model.GalgameLink{}).Where("galgame_id = ?", g.ID).Count(&linkCnt)
	assert.Equal(t, int64(1), tagCnt)
	assert.Equal(t, int64(1), linkCnt)
}

// ReconcileVndbTags: source="vndb" tags reconcile to the fresh set (add new,
// drop stale, convert overlapping user rows), user (source="") tags are
// preserved, and a re-run is a no-op.
func TestReconcileVndbTags(t *testing.T) {
	cleanTables(t)
	getRepos()
	ctx := context.Background()
	gid := makeGalgame(t)
	tUser := createTestTag(t, "user-tag", "content")
	tStale := createTestTag(t, "stale-vndb", "content")
	tV2 := createTestTag(t, "vndb-2", "content")
	tV3 := createTestTag(t, "vndb-3", "content")

	require.NoError(t, testDB.Create(&model.GalgameTagRelation{GalgameID: gid, TagID: tUser, Source: ""}).Error)
	require.NoError(t, testDB.Create(&model.GalgameTagRelation{GalgameID: gid, TagID: tStale, Source: "vndb", SpoilerLevel: 1}).Error)

	changed, err := ReconcileVndbTags(ctx, testDB, gid, map[int]int{tV2: 2, tV3: 0}, true)
	require.NoError(t, err)
	assert.True(t, changed)

	got := tagRelations(t, gid)
	assert.Equal(t, "", got[tUser], "user tag preserved")
	_, hasStale := got[tStale]
	assert.False(t, hasStale, "stale vndb tag removed")
	assert.Equal(t, "vndb", got[tV2])
	assert.Equal(t, "vndb", got[tV3])
	require.Len(t, got, 3)

	// spoiler carried from the desired map.
	var sp int
	testDB.Model(&model.GalgameTagRelation{}).Where("galgame_id = ? AND tag_id = ?", gid, tV2).Pluck("spoiler_level", &sp)
	assert.Equal(t, 2, sp)

	// Idempotent.
	changed, err = ReconcileVndbTags(ctx, testDB, gid, map[int]int{tV2: 2, tV3: 0}, true)
	require.NoError(t, err)
	assert.False(t, changed, "re-run must be a no-op")

	// A user tag that VNDB also lists converts to vndb (vndb authoritative).
	changed, err = ReconcileVndbTags(ctx, testDB, gid, map[int]int{tV2: 2, tV3: 0, tUser: 0}, true)
	require.NoError(t, err)
	assert.True(t, changed)
	assert.Equal(t, "vndb", tagRelations(t, gid)[tUser], "user tag in VNDB set becomes vndb-managed")
}

// A user edit of tag_ids/official_ids must preserve the sync-managed
// source="vndb" relations (union), like it does for links.
func TestUpdate_PreservesVndbRelations(t *testing.T) {
	cleanTables(t)
	getRepos()
	ctx := context.Background()
	gid := makeGalgame(t)
	tUser := createTestTag(t, "u-tag", "content")
	tUser2 := createTestTag(t, "u-tag-2", "content")
	tVndb := createTestTag(t, "v-tag", "content")
	oUser := createTestOfficial(t, "u-off", "company")
	oVndb := createTestOfficial(t, "v-off", "company")

	require.NoError(t, testDB.Create(&model.GalgameTagRelation{GalgameID: gid, TagID: tUser, Source: ""}).Error)
	require.NoError(t, testDB.Create(&model.GalgameTagRelation{GalgameID: gid, TagID: tVndb, Source: "vndb"}).Error)
	require.NoError(t, testDB.Create(&model.GalgameOfficialRelation{GalgameID: gid, OfficialID: oUser, Source: ""}).Error)
	require.NoError(t, testDB.Create(&model.GalgameOfficialRelation{GalgameID: gid, OfficialID: oVndb, Source: "vndb"}).Error)

	// User adds a tag (real change) and submits only their officials — neither
	// echoes the vndb relations.
	tagIDs := []int{tUser, tUser2}
	offIDs := []int{oUser}
	_, err := testSvc.Update(ctx, 1, gid, nil, &dto.UpdateGalgameRequest{TagIDs: &tagIDs, OfficialIDs: &offIDs})
	require.NoError(t, err)

	gotT := tagRelations(t, gid)
	assert.Equal(t, "vndb", gotT[tVndb], "vndb tag must survive the edit")
	assert.Equal(t, "", gotT[tUser])
	assert.Equal(t, "", gotT[tUser2])
	gotO := officialRelations(t, gid)
	assert.Equal(t, "vndb", gotO[oVndb], "vndb official must survive the edit")
	assert.Equal(t, "", gotO[oUser])

	snap := latestRevSnapshot(t, gid)
	assert.ElementsMatch(t, []int{tUser, tUser2, tVndb}, snap.TagIDs)
	assert.ElementsMatch(t, []int{oUser, oVndb}, snap.OfficialIDs)
}
