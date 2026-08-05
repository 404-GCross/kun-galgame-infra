package getchutitlerefs

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func rel(id, work int64, title, brand, date string) catalogRelease {
	return catalogRelease{ReleaseID: id, WorkID: work, Title: title, Brand: brand, RDate: date}
}

func item(id, title, brand, date string) getchuItem {
	return getchuItem{GetchuID: id, Title: title, Brand: brand, RDate: date}
}

// Punctuation and width differ freely between the two catalogues and never
// identify a product.
func TestNormTitleIgnoresPunctuationAndWidth(t *testing.T) {
	assert.Equal(t, NormTitle("好きなものは好きだからしょうがない!!-FIRST LIMIT-"),
		NormTitle("好きなものは好きだからしょうがない！！ FIRST LIMIT"))
	assert.Equal(t, NormTitle("ＦＬＯＷＥＲＳ"), NormTitle("FLOWERS"))
}

// The three shapes that made real matches look unconfirmed, all seen in
// production.
func TestNormPersonNameAbsorbsTranscriptionDifferences(t *testing.T) {
	// Getchu appends a romaji gloss.
	assert.Equal(t, NormPersonName("澤樹 告"), NormPersonName("澤樹 告 ＜Tsuguru Sawaki＞"))
	// The two catalogues separate transliterated names differently.
	assert.Equal(t, NormPersonName("シャルロッテ＝カミル＝ハーリンガム"),
		NormPersonName("シャルロッテ・カミル・ハーリンガム"))
	// Case and width.
	assert.Equal(t, NormPersonName("Shiona"), NormPersonName("ＳＨＩＯＮＡ"))
	// Distinct people must stay distinct.
	assert.NotEqual(t, NormPersonName("日吉 美弥"), NormPersonName("日吉 奈菜"))
}

func TestMatchResolvesOnTitleBrandAndDate(t *testing.T) {
	rels := []catalogRelease{rel(10, 1, "Harmonia", "Key", "2016/03/25")}
	got, st := match([]getchuItem{item("g1", "Ｈａｒｍｏｎｉａ", "Key", "2016/03/25")}, rels)
	assert.Equal(t, 1, len(got))
	assert.Equal(t, int64(10), got[0].ReleaseID)
	assert.Equal(t, int64(1), got[0].WorkID)
	assert.Equal(t, 0, st.NoTitleMatch)
}

// The date is what separates a game from its own re-release. Without it a
// budget edition would anchor onto the original.
func TestMatchRefusesWhenTheDateDiffers(t *testing.T) {
	rels := []catalogRelease{rel(10, 1, "Harmonia", "Key", "2016/03/25")}
	got, st := match([]getchuItem{item("g1", "Harmonia", "Key", "2019/07/26")}, rels)
	assert.Empty(t, got)
	assert.Equal(t, 1, st.DateDiffers)
}

// One title and brand naming two different works cannot be arbitrated by a
// date, so both are dropped — ambiguity is a skip, never a pick.
func TestMatchRefusesAmbiguousWork(t *testing.T) {
	rels := []catalogRelease{
		rel(10, 1, "Twin", "Brand", "2016/03/25"),
		rel(11, 2, "Twin", "Brand", "2016/03/25"),
	}
	got, st := match([]getchuItem{item("g1", "Twin", "Brand", "2016/03/25")}, rels)
	assert.Empty(t, got)
	assert.Equal(t, 1, st.AmbiguousWork)
}

// One work shipping two editions the same day: picking either would attach the
// product to an arbitrary sibling, and a Getchu product IS one edition.
func TestMatchRefusesAmbiguousReleaseWithinOneWork(t *testing.T) {
	rels := []catalogRelease{
		rel(10, 1, "Same", "Brand", "2016/03/25"),
		rel(11, 1, "Same", "Brand", "2016/03/25"),
	}
	got, st := match([]getchuItem{item("g1", "Same", "Brand", "2016/03/25")}, rels)
	assert.Empty(t, got)
	assert.Equal(t, 1, st.AmbiguousRelease)
}

// A work with several dated releases still resolves, as long as exactly one of
// them shares the product's date.
func TestMatchPicksTheReleaseSharingTheDate(t *testing.T) {
	rels := []catalogRelease{
		rel(10, 1, "Game", "Brand", "2016/03/25"),
		rel(11, 1, "Game", "Brand", "2018/09/28"),
	}
	got, st := match([]getchuItem{item("g1", "Game", "Brand", "2018/09/28")}, rels)
	assert.Equal(t, 1, len(got))
	assert.Equal(t, int64(11), got[0].ReleaseID)
	assert.Equal(t, 0, st.AmbiguousRelease)
}

func TestMatchCountsUnmatched(t *testing.T) {
	_, st := match([]getchuItem{item("g1", "Nothing", "Nobody", "2016/03/25")}, nil)
	assert.Equal(t, 1, st.NoTitleMatch)
}
