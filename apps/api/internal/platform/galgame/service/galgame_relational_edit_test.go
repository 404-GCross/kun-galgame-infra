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

func latestRevSnapshot(t *testing.T, gid int) *model.Snapshot {
	t.Helper()
	var rev model.GalgameRevision
	require.NoError(t, testDB.Where("galgame_id = ?", gid).
		Order("revision DESC").First(&rev).Error)
	snap, err := model.SnapshotFromJSON(rev.Snapshot)
	require.NoError(t, err)
	return snap
}

func revCount(t *testing.T, gid int) int64 {
	t.Helper()
	var n int64
	testDB.Model(&model.GalgameRevision{}).Where("galgame_id = ?", gid).Count(&n)
	return n
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
		Banner: "https://b.example/x.webp",
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
	_, err := testSvc.Update(ctx, 1, gid, nil, &dto.UpdateGalgameRequest{
		VNDBID: s("v777"), ReleaseDate: s("2020-01-01"), ReleaseDateTBA: b(true),
		NameEnUS: s("EN"), NameJaJP: s("JA"), NameZhCN: s("ZH"), NameZhTW: s("TW"),
		Banner: s("https://b.example/x.webp"),
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

	var rev model.GalgameRevision
	require.NoError(t, testDB.Where("galgame_id = ? AND revision = 1", g.ID).First(&rev).Error)
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
