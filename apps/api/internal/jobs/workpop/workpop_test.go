package workpop

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPredicateAlias(t *testing.T) {
	unaliased, err := Predicate(Claimed, "")
	require.NoError(t, err)
	assert.Equal(t, "(site IS NOT NULL AND site <> '')", unaliased)

	aliased, err := Predicate(Claimed, "w")
	require.NoError(t, err)
	assert.Equal(t, "(w.site IS NOT NULL AND w.site <> '')", aliased)
}

func TestPredicateEmptyIsAll(t *testing.T) {
	empty, err := Predicate("", "w")
	require.NoError(t, err)
	all, err := Predicate(All, "w")
	require.NoError(t, err)
	assert.Equal(t, all, empty)
	assert.Equal(t, "TRUE", empty)
}

func TestPublishedReadsEveryClaimColumn(t *testing.T) {
	got, err := Predicate(Published, "w")
	require.NoError(t, err)
	for _, col := range []string{"w.site", "w.product_work_id", "w.claim_state"} {
		assert.Contains(t, got, col)
	}
	assert.Contains(t, strings.ReplaceAll(got, "\n\t\t\t", " "), "w.claim_state IS NULL OR w.claim_state = 0")
}

func TestPredicateUnknown(t *testing.T) {
	_, err := Predicate("mine", "w")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown population")
}
