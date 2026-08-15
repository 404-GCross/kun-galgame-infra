package service

import (
	"testing"

	"api/internal/platform/catalog/dto"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDecodeRatingBucketsSortsNumerically(t *testing.T) {
	got := decodeRatingBuckets([]byte(`{"10":2,"9":5,"8":7,"7":1,"6":1}`))
	assert.Equal(t, []dto.RatingBucket{
		{Score: 6, Count: 1}, {Score: 7, Count: 1}, {Score: 8, Count: 7},
		{Score: 9, Count: 5}, {Score: 10, Count: 2},
	}, got, "10 is the top bucket of a 1-10 scale, not the one after 1")
}

func TestDecodeRatingBucketsRejectsUnusable(t *testing.T) {
	assert.Nil(t, decodeRatingBuckets(nil))
	assert.Nil(t, decodeRatingBuckets([]byte(`not json`)))
	assert.Nil(t, decodeRatingBuckets([]byte(`{}`)))
	assert.Nil(t, decodeRatingBuckets([]byte(`{"good":3}`)), "a non-numeric key has no place on a scale")
}

func TestDecodeRatingStats(t *testing.T) {
	assert.Nil(t, decodeRatingStats(nil))
	assert.Nil(t, decodeRatingStats([]byte(`{}`)), "an object with no member reads the same as no stats")

	got := decodeRatingStats([]byte(`{"average":63,"stdev":14,"min":0,"max":90}`))
	require.NotNil(t, got)
	require.NotNil(t, got.Min)
	assert.Zero(t, *got.Min, "0 is a real EG score, not an absent minimum")
	assert.Equal(t, 63.0, *got.Average)
	assert.Equal(t, 90.0, *got.Max)
}
