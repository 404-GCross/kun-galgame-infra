// playtime_dto_test.go — the status codec, which is the only place the wire's
// words and the column's numbers meet. A drift here silently refiles reports
// under the wrong status, and `finished` is the one the public aggregate reads.
package dto

import (
	"testing"

	"api/internal/platform/catalog/model"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPlaytimeStatusRoundTrips(t *testing.T) {
	for _, word := range []string{
		PlaytimeStatusWordPlaying, PlaytimeStatusWordFinished,
		PlaytimeStatusWordDropped, PlaytimeStatusWordOnHold,
	} {
		code, ok := PlaytimeStatusFromWord(word)
		require.True(t, ok, "%q must decode", word)
		assert.Equal(t, word, PlaytimeStatusWord(code), "%q must survive the round trip", word)
	}
}

// An unknown word is REFUSED rather than coerced. Filing a typo as `playing`
// would be invisible to the client and would quietly withhold the report from
// the aggregate it was meant to feed.
func TestUnknownStatusWordIsRefused(t *testing.T) {
	for _, word := range []string{"", "compleded", "FINISHED", "done", "1"} {
		_, ok := PlaytimeStatusFromWord(word)
		assert.False(t, ok, "%q must not decode", word)
	}
}

// The read direction cannot refuse — a row is already stored — so an unknown
// code renders as the neutral word rather than as an empty string a consumer
// would have to special-case.
func TestUnknownStatusCodeRendersNeutral(t *testing.T) {
	assert.Equal(t, PlaytimeStatusWordPlaying, PlaytimeStatusWord(99))
	assert.Equal(t, PlaytimeStatusWordFinished, PlaytimeStatusWord(model.PlaytimeStatusFinished))
}
