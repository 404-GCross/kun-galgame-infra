package service

import (
	"testing"

	"api/internal/platform/catalog/editspec"
	"api/internal/platform/catalog/model"
	"api/internal/platform/editing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func suppressKey(t *testing.T, workID int64, key string) {
	t.Helper()
	require.NoError(t, testDB.Create(&editing.SuppressedRow{
		EntityType: editspec.TypeWork, EntityID: workID, FieldKey: editspec.FieldWorkCredits,
		IdentityKey: key,
	}).Error)
}

func suppressedCreditKeys(t *testing.T, workID int64) []string {
	t.Helper()
	keys, err := editing.LoadSuppressedKeys(t.Context(), testDB, editspec.TypeWork, workID, editspec.FieldWorkCredits)
	require.NoError(t, err)
	return keys
}

func executeMerge(t *testing.T, entityType int16, src, dst int64, note string) {
	t.Helper()
	p, err := testMerge.ProposeMerge(t.Context(), entityType, src, dst, 7, note)
	require.NoError(t, err)
	approveAndForceExecutable(t, p.ID)
	require.NoError(t, testMerge.ExecuteMerge(t.Context(), p.ID, nil))
}

// TestCreditIdentityFollowsRealMerge runs the pillar-7 follow statements against
// the real registry and the real tables. catalog.work.credits is the first
// registered field that ever declared an entity ref, so before this wave every
// merge produced zero follow statements and the whole mechanism was proven only
// against a synthetic registry.
func TestCreditIdentityFollowsRealMerge(t *testing.T) {
	t.Run("CreditNameMerge", func(t *testing.T) {
		cleanTables(t)
		role := seededRoleID(t)
		w1, w2 := createWork(t, "作品1"), createWork(t, "作品2")
		src, dst := createCreditName(t, nil, "旧名義"), createCreditName(t, nil, "新名義")
		ch := createCharacter(t, "キャラ")

		// Neither work takes part in the merge — that is exactly why the follow
		// statements cannot filter on entity_id.
		suppressKey(t, w1.ID, editspec.CreditIdentity(role, src.ID, ch.ID))
		suppressKey(t, w1.ID, editspec.CreditIdentity(role, src.ID, 0))
		suppressKey(t, w2.ID, editspec.CreditIdentity(role, src.ID, ch.ID))
		// w2 already carries the key the rewrite would produce: the source key
		// has nowhere to land and must be dropped, not raise 23505.
		suppressKey(t, w2.ID, editspec.CreditIdentity(role, dst.ID, ch.ID))
		// A key naming some other credit_name must not move.
		other := createCreditName(t, nil, "無関係")
		suppressKey(t, w1.ID, editspec.CreditIdentity(role, other.ID, 0))

		executeMerge(t, model.EntityTypeCreditName, src.ID, dst.ID, "same person")

		assert.ElementsMatch(t, []string{
			editspec.CreditIdentity(role, dst.ID, ch.ID),
			editspec.CreditIdentity(role, dst.ID, 0),
			editspec.CreditIdentity(role, other.ID, 0),
		}, suppressedCreditKeys(t, w1.ID))
		assert.ElementsMatch(t, []string{
			editspec.CreditIdentity(role, dst.ID, ch.ID),
		}, suppressedCreditKeys(t, w2.ID), "the collided source key is dropped, not duplicated")
	})

	t.Run("CharacterMerge", func(t *testing.T) {
		cleanTables(t)
		role := seededRoleID(t)
		w1 := createWork(t, "作品1")
		cn := createCreditName(t, nil, "名義")
		src, dst := createCharacter(t, "旧キャラ"), createCharacter(t, "新キャラ")

		suppressKey(t, w1.ID, editspec.CreditIdentity(role, cn.ID, src.ID))
		// The 0 sentinel means "this credit names no character"; a character
		// merge addressed at it must never rewrite it.
		suppressKey(t, w1.ID, editspec.CreditIdentity(role, cn.ID, 0))

		executeMerge(t, model.EntityTypeCharacter, src.ID, dst.ID, "same character")

		assert.ElementsMatch(t, []string{
			editspec.CreditIdentity(role, cn.ID, dst.ID),
			editspec.CreditIdentity(role, cn.ID, 0),
		}, suppressedCreditKeys(t, w1.ID))
	})

	t.Run("ZeroSentinelSurvivesAMergeAddressedToIt", func(t *testing.T) {
		cleanTables(t)
		role := seededRoleID(t)
		w1 := createWork(t, "作品1")
		cn := createCreditName(t, nil, "名義")
		dst := createCharacter(t, "生存キャラ")
		suppressKey(t, w1.ID, editspec.CreditIdentity(role, cn.ID, 0))

		reg := editing.NewRegistry()
		require.NoError(t, editspec.RegisterAll(reg, testDB))
		for _, s := range reg.IdentityFollowStmts(editspec.TagCharacter, 0, dst.ID, nil) {
			require.NoError(t, testDB.Exec(s.SQL, s.Args...).Error)
		}
		assert.Equal(t, []string{editspec.CreditIdentity(role, cn.ID, 0)}, suppressedCreditKeys(t, w1.ID))
	})
}

func TestMergeKeepsCuratedRowOnCollision(t *testing.T) {
	t.Run("CatalogCredit", func(t *testing.T) {
		cleanTables(t)
		role := seededRoleID(t)
		work := createWork(t, "作品")
		src, dst := createCreditName(t, nil, "旧名義"), createCreditName(t, nil, "新名義")

		curated := createCredit(t, work.ID, src.ID, role, nil)
		require.NoError(t, testDB.Model(curated).Update("source_id", curatedSource).Error)
		upstream := createCredit(t, work.ID, dst.ID, role, nil)
		require.NoError(t, testDB.Model(upstream).Update("source_id", srcVNDB).Error)

		executeMerge(t, model.EntityTypeCreditName, src.ID, dst.ID, "same person")

		var rows []model.CatalogCredit
		require.NoError(t, testDB.Where("work_id = ?", work.ID).Find(&rows).Error)
		require.Len(t, rows, 1, "the collision leaves exactly one row on the survivor")
		assert.Equal(t, curated.ID, rows[0].ID, "the hand-written row is the one that survives")
		require.NotNil(t, rows[0].SourceID)
		assert.EqualValues(t, curatedSource, *rows[0].SourceID)
		assert.Equal(t, dst.ID, rows[0].CreditNameID)
	})

	t.Run("CatalogCharacterAlias", func(t *testing.T) {
		cleanTables(t)
		src, dst := createCharacter(t, "旧キャラ"), createCharacter(t, "新キャラ")
		mk := func(characterID int64, source int16) int64 {
			a := model.CatalogCharacterAlias{
				CharacterID: characterID, Name: "同じ別名", Lang: "ja",
				Kind: model.AliasKindSpellingVariant, SourceID: &source,
			}
			require.NoError(t, testDB.Create(&a).Error)
			return a.ID
		}
		curatedID := mk(src.ID, curatedSource)
		mk(dst.ID, srcVNDB)

		executeMerge(t, model.EntityTypeCharacter, src.ID, dst.ID, "same character")

		var aliases []model.CatalogCharacterAlias
		require.NoError(t, testDB.Where("character_id = ?", dst.ID).Find(&aliases).Error)
		require.Len(t, aliases, 1)
		assert.Equal(t, curatedID, aliases[0].ID, "the hand-written alias is the one that survives")
		require.NotNil(t, aliases[0].SourceID)
		assert.EqualValues(t, curatedSource, *aliases[0].SourceID)
	})

	t.Run("TwoUpstreamRowsKeepTheTarget", func(t *testing.T) {
		cleanTables(t)
		role := seededRoleID(t)
		work := createWork(t, "作品")
		src, dst := createCreditName(t, nil, "旧名義"), createCreditName(t, nil, "新名義")
		createCredit(t, work.ID, src.ID, role, nil)
		kept := createCredit(t, work.ID, dst.ID, role, nil)

		executeMerge(t, model.EntityTypeCreditName, src.ID, dst.ID, "same person")

		var rows []model.CatalogCredit
		require.NoError(t, testDB.Where("work_id = ?", work.ID).Find(&rows).Error)
		require.Len(t, rows, 1)
		assert.Equal(t, kept.ID, rows[0].ID, "with no human lane involved the target still wins")
	})
}
