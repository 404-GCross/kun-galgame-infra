package workratings

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBgmBucketsTotalAndMarshal(t *testing.T) {
	b := bgmBuckets([]byte(`{"1":0,"2":0,"3":0,"4":0,"5":0,"6":1,"7":1,"8":7,"9":5,"10":2}`))
	assert.Equal(t, 16, bucketsTotal(b), "vote_count is the summed histogram")
	assert.JSONEq(t, `{"6":1,"7":1,"8":7,"9":5,"10":2}`, string(marshalBuckets(b)),
		"empty buckets are dropped; an absent key means no votes")
}

func TestBgmBucketsRejectsUnusable(t *testing.T) {
	assert.Nil(t, bgmBuckets(nil))
	assert.Nil(t, bgmBuckets([]byte(`not json`)))
	assert.Zero(t, bucketsTotal(nil))
	assert.Nil(t, marshalBuckets(nil), "no buckets stores NULL, not an empty object")
	assert.Nil(t, marshalBuckets(map[string]int{"1": 0, "2": 0}),
		"an all-zero histogram stores NULL: the source has published no votes")
}

func TestDlsiteBuckets(t *testing.T) {
	got := dlsiteBuckets([]byte(`[{"count":1,"ratio":0,"review_point":1},
		{"count":5,"ratio":0,"review_point":2},{"count":61,"ratio":8,"review_point":3},
		{"count":122,"ratio":17,"review_point":4},{"count":526,"ratio":73,"review_point":5}]`))
	assert.Equal(t, map[string]int{"1": 1, "2": 5, "3": 61, "4": 122, "5": 526}, got,
		"review_point becomes the key; ratio is upstream's own rounding and is dropped")
	assert.Equal(t, 715, bucketsTotal(got))
}

func TestDlsiteBucketsRejectsUnusable(t *testing.T) {
	assert.Nil(t, dlsiteBuckets(nil))
	assert.Nil(t, dlsiteBuckets([]byte(`{"1":2}`)), "the upstream shape is an array, not an object")
	assert.Empty(t, dlsiteBuckets([]byte(`[{"count":3,"review_point":0}]`)),
		"review_point 0 is not a bucket on a 1-5 scale")
}

func TestMarshalStats(t *testing.T) {
	assert.Nil(t, marshalStats(ratingStats{}), "all-absent stores NULL")

	avg, sd := 63.0, 14.0
	out := marshalStats(ratingStats{Average: &avg, Stdev: &sd})
	require.NotNil(t, out)
	assert.JSONEq(t, `{"average":63,"stdev":14}`, string(out),
		"absent members are omitted rather than written as zero — 0 is a real EG score")
}
