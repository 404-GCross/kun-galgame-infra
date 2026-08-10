package getchuportraits

import (
	"testing"

	"api/internal/jobs/getchuchars"

	"github.com/stretchr/testify/assert"
)

func plate(id string, ord int, url string) (nameplateKey, string) {
	return nameplateKey{GetchuID: id, Ordinal: ord}, url
}

func plates(pairs ...any) map[nameplateKey]string {
	out := map[nameplateKey]string{}
	for i := 0; i < len(pairs); i += 3 {
		k, v := plate(pairs[i].(string), pairs[i+1].(int), pairs[i+2].(string))
		out[k] = v
	}
	return out
}

func TestPickPlateUsesTheRichestEditionWhenItHasArt(t *testing.T) {
	c := getchuchars.Candidate{
		CharacterID: 1, GetchuID: "100", Ordinal: 2,
		Editions: []getchuchars.Edition{{GetchuID: "100", Ordinal: 2}, {GetchuID: "200", Ordinal: 5}},
	}
	got, ok := pickPlate(c, plates(
		"100", 2, "https://www.getchu.com/brandnew/100/c100chara3.jpg",
		"200", 5, "https://www.getchu.com/brandnew/200/c200chara9.jpg",
	))
	assert.True(t, ok)
	assert.Equal(t, "100", got.GetchuID)
	assert.Equal(t, "c100chara3.jpg", got.File)
}

func TestPickPlateFallsBackToAnEditionThatHasArt(t *testing.T) {
	c := getchuchars.Candidate{
		CharacterID: 1, GetchuID: "100", Ordinal: 2,
		Editions: []getchuchars.Edition{{GetchuID: "100", Ordinal: 2}, {GetchuID: "200", Ordinal: 5}},
	}
	got, ok := pickPlate(c, plates("200", 5, "https://www.getchu.com/brandnew/200/c200chara9.jpg"))
	assert.True(t, ok)
	assert.Equal(t, "200", got.GetchuID, "must use the edition that actually has the image")
	assert.Equal(t, "c200chara9.jpg", got.File)
}

func TestPickPlateWillNotBorrowAnotherOrdinalsImage(t *testing.T) {
	c := getchuchars.Candidate{
		CharacterID: 1, GetchuID: "100", Ordinal: 2,
		Editions: []getchuchars.Edition{{GetchuID: "100", Ordinal: 2}},
	}
	_, ok := pickPlate(c, plates("100", 7, "https://www.getchu.com/brandnew/100/c100chara7.jpg"))
	assert.False(t, ok)
}

func TestPickPlateReportsNoArt(t *testing.T) {
	c := getchuchars.Candidate{
		CharacterID: 1, GetchuID: "100", Ordinal: 2,
		Editions: []getchuchars.Edition{{GetchuID: "100", Ordinal: 2}},
	}
	_, ok := pickPlate(c, plates())
	assert.False(t, ok)
}

func TestPickPlateHandlesAnEditionlessCandidate(t *testing.T) {
	c := getchuchars.Candidate{CharacterID: 1, GetchuID: "100", Ordinal: 2}
	got, ok := pickPlate(c, plates("100", 2, "https://www.getchu.com/brandnew/100/c100chara3.jpg"))
	assert.True(t, ok)
	assert.Equal(t, "c100chara3.jpg", got.File)
}

func TestMirrorPath(t *testing.T) {
	assert.Equal(t, "/m/100/c100chara3.jpg", mirrorPath("/m", "100", "c100chara3.jpg"))
	assert.Equal(t, "/m/100/c100chara3.jpg", mirrorPath("/m/", "100", "c100chara3.jpg"))
}

func TestWindow(t *testing.T) {
	all := []candidate{{CharacterID: 1}, {CharacterID: 2}, {CharacterID: 3}}
	assert.Len(t, window(all, 0, 0), 3)
	assert.Equal(t, int64(2), window(all, 0, 1)[0].CharacterID)
	assert.Len(t, window(all, 2, 0), 2)
	assert.Nil(t, window(all, 0, 9))
}
