package model

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestKeysOf_SortedDeterministic(t *testing.T) {
	got := KeysOf(map[string]bool{"screenshots": true, "bid": true, "aliases": true})
	assert.Equal(t, []string{"aliases", "bid", "screenshots"}, got, "keys must be sorted for stable jsonb")
	assert.Empty(t, KeysOf(map[string]bool{}))
	assert.Empty(t, KeysOf(nil))
}

// TestGalgameRevision_ChangedFieldsTriState pins the NULL / [] / [...] contract
// that GetRevisionDiff relies on to decide "recorded" vs "legacy fallback".
func TestGalgameRevision_ChangedFieldsTriState(t *testing.T) {
	// NULL (zero value) = legacy revision written before the column existed.
	var legacy GalgameRevision
	assert.False(t, legacy.HasChangedFields(), "zero value = legacy = fallback")
	assert.Nil(t, legacy.ChangedFieldsList())

	// Recorded, non-empty.
	var rev GalgameRevision
	rev.SetChangedFields([]string{"screenshots"})
	assert.True(t, rev.HasChangedFields())
	assert.Equal(t, []string{"screenshots"}, rev.ChangedFieldsList())

	// Recorded but empty (claim / admin status transition) must still be
	// non-NULL, else /diff would mistake it for a legacy revision and fall back.
	for _, in := range [][]string{nil, {}} {
		var empty GalgameRevision
		empty.SetChangedFields(in)
		assert.True(t, empty.HasChangedFields(), "recorded-empty must be non-NULL")
		assert.Empty(t, empty.ChangedFieldsList())
	}
}
