package service

import (
	"context"
	"testing"

	"api/internal/platform/galgame/model"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Tests for the tag/official/engine Create / ExistsByName / Delete repo
// methods added for user-introduced (non-VNDB) taxonomy entries.
//
// Scope: repository layer, matching the rest of this package's harness
// (real Postgres test DB, cleanTables + getRepos). Handler-level auth
// (any-logged-in-user create / admin-only delete) and the
// ExistsByName-driven dup check are thin glue and were verified e2e by
// live curl against :9280; this codebase has no Fiber handler harness.

func makeGalgame(t *testing.T) int {
	t.Helper()
	g := &model.Galgame{UserID: 1, Status: 0}
	require.NoError(t, testDB.Create(g).Error)
	return g.ID
}

func TestTagRepo_CreateExistsDelete(t *testing.T) {
	cleanTables(t)
	getRepos()
	ctx := context.Background()

	tag := &model.GalgameTag{Name: "原创标签", Category: "content", Description: "desc"}
	require.NoError(t, testTagRepo.Create(ctx, tag, []string{"alias1", " alias2 ", "", "  "}))
	require.NotZero(t, tag.ID)

	// Row persisted with fields.
	var got model.GalgameTag
	require.NoError(t, testDB.First(&got, tag.ID).Error)
	assert.Equal(t, "原创标签", got.Name)
	assert.Equal(t, "content", got.Category)
	assert.Equal(t, "desc", got.Description)

	// Aliases: blanks skipped, surrounding space trimmed → exactly 2.
	var aliases []model.GalgameTagAlias
	require.NoError(t, testDB.Where("galgame_tag_id = ?", tag.ID).Find(&aliases).Error)
	require.Len(t, aliases, 2)
	names := []string{aliases[0].Name, aliases[1].Name}
	assert.ElementsMatch(t, []string{"alias1", "alias2"}, names)

	// ExistsByName.
	exists, err := testTagRepo.ExistsByName(ctx, "原创标签")
	require.NoError(t, err)
	assert.True(t, exists)
	exists, err = testTagRepo.ExistsByName(ctx, "不存在")
	require.NoError(t, err)
	assert.False(t, exists)

	// Wire a galgame relation, then Delete must cascade.
	gid := makeGalgame(t)
	require.NoError(t, testDB.Create(&model.GalgameTagRelation{GalgameID: gid, TagID: tag.ID}).Error)

	require.NoError(t, testTagRepo.Delete(ctx, tag.ID))

	assert.ErrorContains(t, testDB.First(&model.GalgameTag{}, tag.ID).Error, "record not found")
	var n int64
	testDB.Model(&model.GalgameTagAlias{}).Where("galgame_tag_id = ?", tag.ID).Count(&n)
	assert.Zero(t, n, "tag aliases must be cascaded")
	testDB.Model(&model.GalgameTagRelation{}).Where("tag_id = ?", tag.ID).Count(&n)
	assert.Zero(t, n, "tag relations must be cascaded")
	// The galgame itself must survive (deleting taxonomy ≠ deleting works).
	testDB.Model(&model.Galgame{}).Where("id = ?", gid).Count(&n)
	assert.Equal(t, int64(1), n, "galgame must NOT be deleted")
}

func TestOfficialRepo_CreateExistsDelete(t *testing.T) {
	cleanTables(t)
	getRepos()
	ctx := context.Background()

	off := &model.GalgameOfficial{
		Name: "原创会社", Category: "company",
		Original: "オリジナル", Link: "https://x", Lang: "ja", Description: "d",
	}
	require.NoError(t, testOfficialRepo.Create(ctx, off, []string{"o1", ""}))
	require.NotZero(t, off.ID)

	var got model.GalgameOfficial
	require.NoError(t, testDB.First(&got, off.ID).Error)
	assert.Equal(t, "原创会社", got.Name)
	assert.Equal(t, "company", got.Category)
	assert.Equal(t, "オリジナル", got.Original)
	assert.Equal(t, "https://x", got.Link)
	assert.Equal(t, "ja", got.Lang)

	var aliases []model.GalgameOfficialAlias
	require.NoError(t, testDB.Where("galgame_official_id = ?", off.ID).Find(&aliases).Error)
	require.Len(t, aliases, 1)
	assert.Equal(t, "o1", aliases[0].Name)

	exists, err := testOfficialRepo.ExistsByName(ctx, "原创会社")
	require.NoError(t, err)
	assert.True(t, exists)
	exists, err = testOfficialRepo.ExistsByName(ctx, "无")
	require.NoError(t, err)
	assert.False(t, exists)

	gid := makeGalgame(t)
	require.NoError(t, testDB.Create(&model.GalgameOfficialRelation{GalgameID: gid, OfficialID: off.ID}).Error)

	require.NoError(t, testOfficialRepo.Delete(ctx, off.ID))

	assert.ErrorContains(t, testDB.First(&model.GalgameOfficial{}, off.ID).Error, "record not found")
	var n int64
	testDB.Model(&model.GalgameOfficialAlias{}).Where("galgame_official_id = ?", off.ID).Count(&n)
	assert.Zero(t, n)
	testDB.Model(&model.GalgameOfficialRelation{}).Where("official_id = ?", off.ID).Count(&n)
	assert.Zero(t, n)
	testDB.Model(&model.Galgame{}).Where("id = ?", gid).Count(&n)
	assert.Equal(t, int64(1), n)
}

func TestEngineRepo_CreateExistsDelete(t *testing.T) {
	cleanTables(t)
	getRepos()
	ctx := context.Background()

	eng := &model.GalgameEngine{Name: "原创引擎", Description: "e"}
	require.NoError(t, testEngineRepo.Create(ctx, eng, []string{"e-alias"}))
	require.NotZero(t, eng.ID)

	var got model.GalgameEngine
	require.NoError(t, testDB.First(&got, eng.ID).Error)
	assert.Equal(t, "原创引擎", got.Name)
	// Engine aliases live inline as a jsonb array on the row.
	assert.JSONEq(t, `["e-alias"]`, string(got.Alias))

	// Create with nil aliases must still produce a valid `[]` jsonb.
	eng2 := &model.GalgameEngine{Name: "引擎2"}
	require.NoError(t, testEngineRepo.Create(ctx, eng2, nil))
	require.NotZero(t, eng2.ID)
	var got2 model.GalgameEngine
	require.NoError(t, testDB.Where("id = ?", eng2.ID).First(&got2).Error)
	assert.JSONEq(t, `[]`, string(got2.Alias))

	exists, err := testEngineRepo.ExistsByName(ctx, "原创引擎")
	require.NoError(t, err)
	assert.True(t, exists)
	exists, err = testEngineRepo.ExistsByName(ctx, "x")
	require.NoError(t, err)
	assert.False(t, exists)

	gid := makeGalgame(t)
	require.NoError(t, testDB.Create(&model.GalgameEngineRelation{GalgameID: gid, EngineID: eng.ID}).Error)

	require.NoError(t, testEngineRepo.Delete(ctx, eng.ID))

	assert.ErrorContains(t, testDB.First(&model.GalgameEngine{}, eng.ID).Error, "record not found")
	var n int64
	testDB.Model(&model.GalgameEngineRelation{}).Where("engine_id = ?", eng.ID).Count(&n)
	assert.Zero(t, n)
	testDB.Model(&model.Galgame{}).Where("id = ?", gid).Count(&n)
	assert.Equal(t, int64(1), n)
}

// CountReferences backs the delete gate: plain DELETE is refused while
// relations > 0, ?force=true purges them first. (HTTP force/refuse
// behavior verified e2e by curl; this codebase has no Fiber handler
// harness, so the gate's data source is unit-tested here.)
func TestTaxonomyCountReferences(t *testing.T) {
	cleanTables(t)
	getRepos()
	ctx := context.Background()

	tag := &model.GalgameTag{Name: "T", Category: "content"}
	require.NoError(t, testTagRepo.Create(ctx, tag, []string{"a1", "a2"}))
	off := &model.GalgameOfficial{Name: "O", Category: "company"}
	require.NoError(t, testOfficialRepo.Create(ctx, off, []string{"oa1"}))
	eng := &model.GalgameEngine{Name: "E"}
	require.NoError(t, testEngineRepo.Create(ctx, eng, nil))

	// No relations yet → 0 (delete would be allowed without force).
	rel, alias, err := testTagRepo.CountReferences(ctx, tag.ID)
	require.NoError(t, err)
	assert.Equal(t, int64(0), rel)
	assert.Equal(t, int64(2), alias, "alias count independent of relations")

	// Two galgames reference each entity.
	for range 2 {
		gid := makeGalgame(t)
		require.NoError(t, testDB.Create(&model.GalgameTagRelation{GalgameID: gid, TagID: tag.ID}).Error)
		require.NoError(t, testDB.Create(&model.GalgameOfficialRelation{GalgameID: gid, OfficialID: off.ID}).Error)
		require.NoError(t, testDB.Create(&model.GalgameEngineRelation{GalgameID: gid, EngineID: eng.ID}).Error)
	}

	rel, alias, err = testTagRepo.CountReferences(ctx, tag.ID)
	require.NoError(t, err)
	assert.Equal(t, int64(2), rel)
	assert.Equal(t, int64(2), alias)

	oRel, oAlias, err := testOfficialRepo.CountReferences(ctx, off.ID)
	require.NoError(t, err)
	assert.Equal(t, int64(2), oRel)
	assert.Equal(t, int64(1), oAlias)

	eRel, err := testEngineRepo.CountReferences(ctx, eng.ID)
	require.NoError(t, err)
	assert.Equal(t, int64(2), eRel)

	// Force-purge (Delete) clears them; a subsequent count is 0.
	require.NoError(t, testTagRepo.Delete(ctx, tag.ID))
	rel, _, err = testTagRepo.CountReferences(ctx, tag.ID)
	require.NoError(t, err)
	assert.Equal(t, int64(0), rel)
}
