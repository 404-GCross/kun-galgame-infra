package model

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// Pins every enum group to unique values — a slipped finger when adding a
// constant must fail loudly, because these values are persisted and can
// never be renumbered.
func TestConstantGroupsHaveUniqueValues(t *testing.T) {
	groups := map[string][]int16{
		"entity_type": {
			EntityTypePerson, EntityTypeCreditName, EntityTypeOrg,
			EntityTypeLabel, EntityTypeCharacter, EntityTypeWork, EntityTypeRelease,
		},
		"gender": {GenderMale, GenderFemale, GenderOther},
		"credit_name_kind": {
			CreditNameKindMain, CreditNameKindPenName,
			CreditNameKindDistinctPersona, CreditNameKindFormerName,
		},
		"link_visibility": {LinkVisibilityPublic, LinkVisibilityHidden},
		"alias_kind":      {AliasKindTranslation, AliasKindSpellingVariant, AliasKindSearchHint},
		"label_kind": {
			LabelKindGameBrand, LabelKindBunko, LabelKindPublisher,
			LabelKindAnimeStudio, LabelKindDoujinCircle, LabelKindGroup,
		},
		"revision_action": {
			RevisionActionCreated, RevisionActionUpdated, RevisionActionMergedSource,
			RevisionActionMergedTarget, RevisionActionSplit, RevisionActionImported,
			RevisionActionRedirect, RevisionActionReverted,
		},
		"relation_domain": {RelationDomainWork, RelationDomainEntity},
	}
	for name, values := range groups {
		seen := make(map[int16]bool, len(values))
		for _, v := range values {
			assert.False(t, seen[v], "%s: duplicate value %d", name, v)
			seen[v] = true
		}
	}
}

// The polymorphic discriminator values are pinned forever — renumbering them
// would silently re-address every redirect/usage/revision row.
func TestEntityTypeValuesArePinned(t *testing.T) {
	assert.Equal(t, int16(0), EntityTypePerson)
	assert.Equal(t, int16(1), EntityTypeCreditName)
	assert.Equal(t, int16(2), EntityTypeOrg)
	assert.Equal(t, int16(3), EntityTypeLabel)
	assert.Equal(t, int16(4), EntityTypeCharacter)
	assert.Equal(t, int16(5), EntityTypeWork)
	assert.Equal(t, int16(6), EntityTypeRelease)
}
