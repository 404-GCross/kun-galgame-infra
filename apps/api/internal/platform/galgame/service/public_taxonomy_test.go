package service

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"

	"api/internal/platform/galgame/dto"
	"api/internal/platform/galgame/model"
	"api/internal/platform/galgame/repository"

	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// W1b route-B endgame: curated taxonomy four-family public projection. These
// DB-backed tests pin the by-id shapes (G2), the content_limit gating on series
// previews + the tag-category gate (G3), and the batched (non-N+1) series preview
// hydration (G4). The in-process smoke (galgameapp.TestPublicW1bSmoke) covers the
// full HTTP wiring end-to-end.

func newTaxSvc() *PublicTaxonomyService {
	getRepos()
	return NewPublicTaxonomyService(testTagRepo, testOfficialRepo, testEngineRepo, testSeriesRepo, testSvc)
}

// seedTaxGalgame inserts a published galgame optionally in a series with a given
// content rating.
func seedTaxGalgame(t *testing.T, id int, seriesID *int, contentLimit string) {
	t.Helper()
	require.NoError(t, testDB.Create(&model.Galgame{
		ID: id, NameJaJP: fmt.Sprintf("tax-game-%d", id), UserID: 1, Status: 0,
		ContentLimit: contentLimit, OriginalLanguage: "ja-jp", AgeLimit: "all", ReleasePrecision: "day",
		SeriesID: seriesID,
	}).Error)
}

func TestPublicTagEntityAndList(t *testing.T) {
	cleanTables(t)
	ctx := context.Background()
	svc := newTaxSvc()

	tagID := createTestTag(t, "curated-tag", "content")
	require.NoError(t, testDB.Model(&model.GalgameTag{}).Where("id = ?", tagID).
		Update("description", "a curated description").Error)
	require.NoError(t, testDB.Create(&model.GalgameTagAlias{GalgameTagID: tagID, Name: "curated-alias"}).Error)
	g1 := 810001
	seedTaxGalgame(t, g1, nil, "sfw")
	require.NoError(t, testDB.Create(&model.GalgameTagRelation{GalgameID: g1, TagID: tagID}).Error)

	// Detail by id.
	ent, found, err := svc.Tag(ctx, tagID)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, "curated-tag", ent.Name)
	require.Equal(t, "content", ent.Category)
	require.Equal(t, "a curated description", ent.Description)
	require.Equal(t, []string{"curated-alias"}, ent.Aliases)
	require.Equal(t, 1, ent.GalgameCount)
	require.NotEmpty(t, ent.Created)
	require.NotEmpty(t, ent.Updated)

	// Missing id → not found (by-id 404 path).
	_, found, err = svc.Tag(ctx, 999999)
	require.NoError(t, err)
	require.False(t, found)

	// List carries the same curated shape + total.
	list, err := svc.TagList(ctx, 1, 50, "")
	require.NoError(t, err)
	require.GreaterOrEqual(t, list.Total, int64(1))
	var seen bool
	for _, it := range list.Items {
		if it.ID == tagID {
			seen = true
			require.Equal(t, 1, it.GalgameCount)
			require.Equal(t, []string{"curated-alias"}, it.Aliases)
		}
	}
	require.True(t, seen, "created tag must appear in the list")

	// galgame-ids reverse face.
	ids, err := svc.TagGalgameIDs(ctx, tagID)
	require.NoError(t, err)
	require.Equal(t, []int{g1}, ids)
	// Empty entity → [] not nil.
	emptyTag := createTestTag(t, "empty-tag", "content")
	ids, err = svc.TagGalgameIDs(ctx, emptyTag)
	require.NoError(t, err)
	require.NotNil(t, ids)
	require.Empty(t, ids)
}

func TestPublicTagListCategoryGate(t *testing.T) {
	cleanTables(t)
	ctx := context.Background()
	svc := newTaxSvc()

	createTestTag(t, "sexual-tag", "sexual")
	createTestTag(t, "content-tag", "content")

	// sfw hides the sexual category.
	sfw, err := svc.TagList(ctx, 1, 50, "sfw")
	require.NoError(t, err)
	require.False(t, containsTagNamed(sfw.Items, "sexual-tag"), "sfw list must hide the sexual category")
	require.True(t, containsTagNamed(sfw.Items, "content-tag"))

	// all (content_limit resolved to "") includes both.
	all, err := svc.TagList(ctx, 1, 50, "")
	require.NoError(t, err)
	require.True(t, containsTagNamed(all.Items, "sexual-tag"))
	require.True(t, containsTagNamed(all.Items, "content-tag"))
}

func containsTagNamed(items []dto.PublicTagEntity, name string) bool {
	for i := range items {
		if items[i].Name == name {
			return true
		}
	}
	return false
}

func TestPublicTagMultiIntersection(t *testing.T) {
	cleanTables(t)
	ctx := context.Background()
	svc := newTaxSvc()

	t1 := createTestTag(t, "multi-a", "content")
	t2 := createTestTag(t, "multi-b", "content")
	both := 820001
	onlyOne := 820002
	seedTaxGalgame(t, both, nil, "sfw")
	seedTaxGalgame(t, onlyOne, nil, "sfw")
	require.NoError(t, testDB.Create(&model.GalgameTagRelation{GalgameID: both, TagID: t1}).Error)
	require.NoError(t, testDB.Create(&model.GalgameTagRelation{GalgameID: both, TagID: t2}).Error)
	require.NoError(t, testDB.Create(&model.GalgameTagRelation{GalgameID: onlyOne, TagID: t1}).Error)

	res, err := svc.TagMulti(ctx, []int{t1, t2}, 1, 24, "sfw", PublicItemInclude{}, false)
	require.NoError(t, err)
	require.Equal(t, int64(1), res.Total)
	require.Len(t, res.Items, 1)
	require.Equal(t, both, res.Items[0].ID)

	// Empty ids → empty (non-nil) page.
	res, err = svc.TagMulti(ctx, nil, 1, 24, "sfw", PublicItemInclude{}, false)
	require.NoError(t, err)
	require.Equal(t, int64(0), res.Total)
	require.NotNil(t, res.Items)
	require.Empty(t, res.Items)
}

func TestPublicOfficialAndEngineEntity(t *testing.T) {
	cleanTables(t)
	ctx := context.Background()
	svc := newTaxSvc()

	offID := createTestOfficial(t, "curated-maker", "company")
	require.NoError(t, testDB.Model(&model.GalgameOfficial{}).Where("id = ?", offID).Updates(map[string]any{
		"original": "オリジナル", "link": "https://maker.example/", "lang": "ja-jp", "description": "maker desc",
	}).Error)
	require.NoError(t, testDB.Create(&model.GalgameOfficialAlias{GalgameOfficialID: offID, Name: "maker-alias"}).Error)
	og := 830001
	seedTaxGalgame(t, og, nil, "sfw")
	require.NoError(t, testDB.Create(&model.GalgameOfficialRelation{GalgameID: og, OfficialID: offID}).Error)

	oe, found, err := svc.Official(ctx, offID)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, "curated-maker", oe.Name)
	require.Equal(t, "company", oe.Category)
	require.Equal(t, "オリジナル", oe.Original)
	require.Equal(t, "https://maker.example/", oe.Link)
	require.Equal(t, "ja-jp", oe.Lang)
	require.Equal(t, "maker desc", oe.Description)
	require.Equal(t, []string{"maker-alias"}, oe.Aliases)
	require.Equal(t, 1, oe.GalgameCount)

	oids, err := svc.OfficialGalgameIDs(ctx, offID)
	require.NoError(t, err)
	require.Equal(t, []int{og}, oids)

	// Engine (alias parsed from inline jsonb).
	eng := &model.GalgameEngine{Name: "curated-engine", Description: "engine desc", Alias: []byte(`["e-alias-1","e-alias-2"]`)}
	require.NoError(t, testDB.Create(eng).Error)
	eg := 830002
	seedTaxGalgame(t, eg, nil, "sfw")
	require.NoError(t, testDB.Create(&model.GalgameEngineRelation{GalgameID: eg, EngineID: eng.ID}).Error)

	ee, found, err := svc.Engine(ctx, eng.ID)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, "curated-engine", ee.Name)
	require.Equal(t, "engine desc", ee.Description)
	require.Equal(t, []string{"e-alias-1", "e-alias-2"}, ee.Alias)
	require.Equal(t, 1, ee.GalgameCount)

	elist, err := svc.EngineList(ctx, 1, 50)
	require.NoError(t, err)
	require.GreaterOrEqual(t, elist.Total, int64(1))

	eids, err := svc.EngineGalgameIDs(ctx, eng.ID)
	require.NoError(t, err)
	require.Equal(t, []int{eg}, eids)

	// Missing ids → not found.
	_, found, _ = svc.Official(ctx, 999999)
	require.False(t, found)
	_, found, _ = svc.Engine(ctx, 999999)
	require.False(t, found)
}

func TestPublicSeriesPreviewSfwGate(t *testing.T) {
	cleanTables(t)
	ctx := context.Background()
	svc := newTaxSvc()

	sID := createTestSeries(t, "gated-series")
	seedTaxGalgame(t, 840001, &sID, "sfw")
	seedTaxGalgame(t, 840002, &sID, "nsfw")

	// Detail sfw: only the sfw member; count in sync.
	sfw, found, err := svc.Series(ctx, sID, "sfw", PublicItemInclude{})
	require.NoError(t, err)
	require.True(t, found)
	require.Len(t, sfw.Galgames, 1, "sfw series detail must drop the nsfw member")
	require.Equal(t, 840001, sfw.Galgames[0].ID)
	require.Equal(t, 1, sfw.GalgameCount)

	// Detail all: both members.
	all, found, err := svc.Series(ctx, sID, "", PublicItemInclude{})
	require.NoError(t, err)
	require.True(t, found)
	require.Len(t, all.Galgames, 2)
	require.Equal(t, 2, all.GalgameCount)

	// List sfw: the series' cnt reflects the gated set.
	list, err := svc.SeriesList(ctx, 1, 24, "sfw", PublicItemInclude{})
	require.NoError(t, err)
	var got *int
	for i := range list.Items {
		if list.Items[i].ID == sID {
			c := list.Items[i].GalgameCount
			got = &c
		}
	}
	require.NotNil(t, got, "series must appear in the list")
	require.Equal(t, 1, *got, "sfw list count must reflect the gated member set")

	// Missing id → not found.
	_, found, _ = svc.Series(ctx, 999999, "sfw", PublicItemInclude{})
	require.False(t, found)
}

func TestPublicSeriesListNonN1(t *testing.T) {
	cleanTables(t)
	ctx := context.Background()

	// 6 series, each with 3 published sfw members.
	gid := 850000
	for s := 1; s <= 6; s++ {
		sID := createTestSeries(t, fmt.Sprintf("n1-series-%d", s))
		for m := 0; m < 3; m++ {
			gid++
			seedTaxGalgame(t, gid, &sID, "sfw")
		}
	}

	count := func(limit int) int64 {
		var n int64
		sess := testDB.Session(&gorm.Session{Logger: countingLogger{Interface: logger.Default.LogMode(logger.Silent), n: &n}})
		gsvc := NewGalgameService(
			repository.NewGalgameRepository(sess),
			repository.NewRevisionRepository(sess),
			repository.NewPRRepository(sess),
			repository.NewUserReadonlyRepository(sess),
		).WithCDNBase("https://cdn.example.com/img")
		tsvc := NewPublicTaxonomyService(
			repository.NewTagRepository(sess), repository.NewOfficialRepository(sess),
			repository.NewEngineRepository(sess), repository.NewSeriesRepository(sess), gsvc)
		data, err := tsvc.SeriesList(ctx, 1, limit, "sfw", PublicItemInclude{})
		require.NoError(t, err)
		require.NotEmpty(t, data.Items)
		return atomic.LoadInt64(&n)
	}

	small, large := count(2), count(6)
	require.Equal(t, small, large, "series-list query count must be constant across page size (non-N+1): 2=%d 6=%d", small, large)
	require.LessOrEqualf(t, large, int64(6), "expected a small constant query count, got %d — possible N+1", large)
}

func TestPublicTagMultiExpandAndChildren(t *testing.T) {
	cleanTables(t)
	ctx := context.Background()
	svc := newTaxSvc()

	scifi := createTestTag(t, "科幻", "content")
	hard := createTestTag(t, "硬科幻", "content")
	vr := createTestTag(t, "虚拟现实", "content")
	harem := createTestTag(t, "后宫结局", "content")

	// 科幻 → 硬科幻 → 虚拟现实: two levels, so expand must walk the closure.
	require.NoError(t, testDB.Create(&model.GalgameTagEdge{ParentID: scifi, ChildID: hard, Source: "vndb"}).Error)
	require.NoError(t, testDB.Create(&model.GalgameTagEdge{ParentID: hard, ChildID: vr, Source: "vndb"}).Error)

	gA, gB, gC, gD := 830001, 830002, 830003, 830004
	seedTaxGalgame(t, gA, nil, "sfw") // 后宫结局 + 科幻 itself
	seedTaxGalgame(t, gB, nil, "sfw") // 后宫结局 + 硬科幻 (depth 1)
	seedTaxGalgame(t, gC, nil, "sfw") // 后宫结局 + 虚拟现实 (depth 2)
	seedTaxGalgame(t, gD, nil, "sfw") // 后宫结局 only — must NOT match
	rel := func(gid, tid int) {
		require.NoError(t, testDB.Create(&model.GalgameTagRelation{GalgameID: gid, TagID: tid}).Error)
	}
	rel(gA, harem)
	rel(gA, scifi)
	rel(gB, harem)
	rel(gB, hard)
	rel(gC, harem)
	rel(gC, vr)
	rel(gD, harem)

	// Flat face (expand=false): only the game carrying 科幻 itself qualifies.
	res, err := svc.TagMulti(ctx, []int{harem, scifi}, 1, 24, "sfw", PublicItemInclude{}, false)
	require.NoError(t, err)
	require.Equal(t, int64(1), res.Total)
	require.Equal(t, gA, res.Items[0].ID)

	// expand=descendants: 科幻 widens to {科幻, 硬科幻, 虚拟现实} — A+B+C, never D.
	res, err = svc.TagMulti(ctx, []int{harem, scifi}, 1, 24, "sfw", PublicItemInclude{}, true)
	require.NoError(t, err)
	require.Equal(t, int64(3), res.Total)
	got := map[int]bool{}
	for i := range res.Items {
		got[res.Items[i].ID] = true
	}
	require.True(t, got[gA] && got[gB] && got[gC])
	require.False(t, got[gD])

	// Expanding a leaf changes nothing: with no out-edges the faces coincide.
	res, err = svc.TagMulti(ctx, []int{harem, vr}, 1, 24, "sfw", PublicItemInclude{}, true)
	require.NoError(t, err)
	require.Equal(t, int64(1), res.Total)
	require.Equal(t, gC, res.Items[0].ID)

	// Tag detail: 科幻 lists its DIRECT children only (硬科幻 — one hop).
	ent, found, err := svc.Tag(ctx, scifi)
	require.NoError(t, err)
	require.True(t, found)
	require.Len(t, ent.Children, 1)
	require.Equal(t, hard, ent.Children[0].ID)
	require.Equal(t, "硬科幻", ent.Children[0].Name)
	require.Equal(t, 1, ent.Children[0].GalgameCount)

	// A leaf tag omits children entirely (nil slice → key absent in JSON).
	ent, found, err = svc.Tag(ctx, vr)
	require.NoError(t, err)
	require.True(t, found)
	require.Nil(t, ent.Children)
}
