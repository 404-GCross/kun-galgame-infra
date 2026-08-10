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

func TestUnknownStatusWordIsRefused(t *testing.T) {
	for _, word := range []string{"", "compleded", "FINISHED", "done", "1"} {
		_, ok := PlaytimeStatusFromWord(word)
		assert.False(t, ok, "%q must not decode", word)
	}
}

func TestUnknownStatusCodeRendersNeutral(t *testing.T) {
	assert.Equal(t, PlaytimeStatusWordPlaying, PlaytimeStatusWord(99))
	assert.Equal(t, PlaytimeStatusWordFinished, PlaytimeStatusWord(model.PlaytimeStatusFinished))
}
