package getchutitlerefs

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func rel(id, work int64, title, brand, date string) catalogRelease {
	return catalogRelease{ReleaseID: id, WorkID: work, Title: title, Brand: brand, RDate: date}
}

// relEd is rel plus the three fields that separate one same-day sibling from
// another: the release's own title, its platform and its language.
func relEd(id, work int64, title, brand, date, rtitle, platform, lang string) catalogRelease {
	r := rel(id, work, title, brand, date)
	r.RTitle, r.Platform, r.Lang = rtitle, platform, lang
	return r
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

// A product whose title carries an edition marker names the bare work. Without
// the strip it never reaches its own catalog entry at all — it is counted as
// "no title match", which is why this bucket hid the biggest population.
func TestMatchReachesTheWorkOnceTheEditionMarkerComesOff(t *testing.T) {
	rels := []catalogRelease{rel(10, 1, "Summer Pockets", "Key", "2018/06/29")}
	got, st := match([]getchuItem{item("g1", "Summer Pockets 初回限定版", "Key", "2018/06/29")}, rels)
	assert.Equal(t, 1, len(got))
	assert.Equal(t, int64(10), got[0].ReleaseID)
	assert.Equal(t, 1, st.MatchedAfterStrip)
}

// The exact title wins over the stripped one, so a work whose own title ends in
// a marker is still matched as itself.
func TestMatchPrefersTheExactTitleOverTheStrippedOne(t *testing.T) {
	rels := []catalogRelease{
		rel(10, 1, "とある作品 新装版", "Brand", "2016/03/25"),
		rel(11, 2, "とある作品", "Brand", "2016/03/25"),
	}
	got, st := match([]getchuItem{item("g1", "とある作品 新装版", "Brand", "2016/03/25")}, rels)
	assert.Equal(t, 1, len(got))
	assert.Equal(t, int64(10), got[0].ReleaseID)
	assert.Equal(t, 0, st.MatchedAfterStrip)
}

// Getchu's pc_soft genre is Windows software sold in Japan, so a console port
// sharing the release day is not a candidate.
func TestMatchDropsSiblingsOnAContradictingPlatform(t *testing.T) {
	rels := []catalogRelease{
		relEd(10, 1, "Game", "Brand", "2016/03/25", "Game パッケージ版", "win", "ja"),
		relEd(11, 1, "Game", "Brand", "2016/03/25", "Game", "ps4", "ja"),
	}
	got, st := match([]getchuItem{item("g1", "Game", "Brand", "2016/03/25")}, rels)
	assert.Equal(t, 1, len(got))
	assert.Equal(t, int64(10), got[0].ReleaseID)
	assert.Equal(t, 1, st.NarrowedByPlatform)
}

// Half of all releases record no platform and no language. An unknown sibling
// must survive the filter, or the rule would quietly prefer whichever rows
// happen to be better documented.
func TestMatchKeepsSiblingsWithNoPlatformRecorded(t *testing.T) {
	rels := []catalogRelease{
		relEd(10, 1, "Game", "Brand", "2016/03/25", "Game", "", ""),
		relEd(11, 1, "Game", "Brand", "2016/03/25", "Game", "", ""),
	}
	_, st := match([]getchuItem{item("g1", "Game", "Brand", "2016/03/25")}, rels)
	assert.Equal(t, 1, st.AmbiguousRelease)
	assert.Equal(t, 0, st.NarrowedByPlatform)
}

// The classic pair: one work, two boxes, one day. The product says which box it
// is and the release titles say which is which.
func TestMatchResolvesTheEditionSplitByTheMarker(t *testing.T) {
	rels := []catalogRelease{
		relEd(10, 1, "枯れない世界と終わる花", "Brand", "2016/03/25", "枯れない世界と終わる花 ダウンロード版", "win", "ja"),
		relEd(11, 1, "枯れない世界と終わる花", "Brand", "2016/03/25", "枯れない世界と終わる花 パッケージ版", "win", "ja"),
	}
	got, st := match([]getchuItem{item("g1", "枯れない世界と終わる花 DVD-ROM版", "Brand", "2016/03/25")}, rels)
	assert.Equal(t, 1, len(got))
	assert.Equal(t, int64(11), got[0].ReleaseID, "DVD-ROM版 is the boxed copy")
	assert.Equal(t, 1, st.NarrowedByEdition)
}

// Two siblings claiming the same edition are still ambiguous. The rule exists
// to resolve, not to pick.
func TestMatchStaysAmbiguousWhenTwoSiblingsClaimTheSameEdition(t *testing.T) {
	rels := []catalogRelease{
		relEd(10, 1, "Game", "Brand", "2016/03/25", "Game 通常版", "win", "ja"),
		relEd(11, 1, "Game", "Brand", "2016/03/25", "Game 通常版", "win", "ja"),
	}
	_, st := match([]getchuItem{item("g1", "Game 通常版", "Brand", "2016/03/25")}, rels)
	assert.Equal(t, 1, st.AmbiguousRelease)
	assert.Equal(t, 0, st.NarrowedByEdition)
}

// A marker the vocabulary does not know must not resolve anything — silence is
// not agreement.
func TestMatchStaysAmbiguousOnAnUnknownEditionMarker(t *testing.T) {
	rels := []catalogRelease{
		relEd(10, 1, "虐襲", "Brand", "2016/03/25", "虐襲 ダウンロード版", "win", "ja"),
		relEd(11, 1, "虐襲", "Brand", "2016/03/25", "虐襲 パッケージ版", "win", "ja"),
	}
	_, st := match([]getchuItem{item("g1", "虐襲 Windows7対応版", "Brand", "2016/03/25")}, rels)
	assert.Equal(t, 1, st.AmbiguousRelease)
}

// The narrowing left exactly one box, but it is a box the product says it is
// not. A choice was available and the evidence refutes the one that was made.
func TestMatchRefusesABoxItsOwnEvidenceContradicts(t *testing.T) {
	rels := []catalogRelease{
		relEd(10, 1, "初恋", "Brand", "2016/03/25", "初恋 パッケージ版", "win", "ja"),
		relEd(11, 1, "初恋", "Brand", "2016/03/25", "初恋", "ps4", "ja"),
	}
	got, st := match([]getchuItem{item("g1", "初恋 豪華限定版", "Brand", "2016/03/25")}, rels)
	assert.Empty(t, got)
	assert.Equal(t, 1, st.EditionConflict)
}

// When the catalog records a SINGLE release for the day, a marker disagreement
// means only that the catalog under-models editions — the work is still right
// and there is no alternative to prefer. Vetoing here would discard correct
// matches to gain nothing.
func TestMatchKeepsTheOnlyReleaseEvenIfItsMarkerDisagrees(t *testing.T) {
	rels := []catalogRelease{
		relEd(10, 1, "アイキス2", "Brand", "2016/03/25", "アイキス2 ダウンロード版", "win", "ja"),
	}
	got, st := match([]getchuItem{item("g1", "アイキス2 通常版", "Brand", "2016/03/25")}, rels)
	assert.Equal(t, 1, len(got))
	assert.Equal(t, 0, st.EditionConflict)
}

func TestMatchCountsUnmatched(t *testing.T) {
	_, st := match([]getchuItem{item("g1", "Nothing", "Nobody", "2016/03/25")}, nil)
	assert.Equal(t, 1, st.NoTitleMatch)
}
