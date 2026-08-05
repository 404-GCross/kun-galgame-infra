package getchutitlerefs

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSplitEditionTakesTheTrailingMarker(t *testing.T) {
	for _, c := range []struct{ in, base, ed string }{
		{"Summer Pockets 初回限定版", "Summer Pockets", "初回限定版"},
		{"よつのは DVD-ROM版", "よつのは", "DVD-ROM版"},
		{"虐襲 Windows7対応版", "虐襲", "Windows7対応版"},
		{"紅色天井艶妖綺譚 新装版", "紅色天井艶妖綺譚", "新装版"},
		// The ideographic space the catalog uses is a separator too.
		{"BALDR SKY Dive2　期間限定生産版", "BALDR SKY Dive2", "期間限定生産版"},
		// Two markers stacked; both come off.
		{"ウルティマオンライン 武刀の天地 アップグレード版 日本語版", "ウルティマオンライン 武刀の天地", "アップグレード版 日本語版"},
	} {
		base, ed := SplitEdition(c.in)
		assert.Equal(t, c.base, base, c.in)
		assert.Equal(t, c.ed, ed, c.in)
	}
}

// The common markers also appear glued straight onto the title.
func TestSplitEditionHandlesTheGluedForms(t *testing.T) {
	base, ed := SplitEdition("さくらむすび初回限定版")
	assert.Equal(t, "さくらむすび", base)
	assert.Equal(t, "初回限定版", ed)
}

// A title with no marker must survive completely untouched — this runs against
// every product, and a title it damages is a match it silently loses.
func TestSplitEditionLeavesAPlainTitleAlone(t *testing.T) {
	for _, s := range []string{
		"Summer Pockets",
		"しゅがてん！-sugarfull tempering-",
		"D.C.～ダ・カーポ～",
	} {
		base, ed := SplitEdition(s)
		assert.Equal(t, s, base)
		assert.Empty(t, ed)
	}
}

// 完全版 routinely names a different product, not a different box. Folding it
// into the base work would be a claim about identity.
func TestSplitEditionRefusesToFoldTheCompleteEdition(t *testing.T) {
	base, ed := SplitEdition("この世の果てで恋を唄う少女YU-NO 完全版")
	assert.Equal(t, "この世の果てで恋を唄う少女YU-NO 完全版", base)
	assert.Empty(t, ed)
}

// The two catalogues use different words for the same box.
func TestEditionClassBridgesTheTwoVocabularies(t *testing.T) {
	assert.Equal(t, "package", EditionClass("DVD-ROM版"))
	assert.Equal(t, "package", EditionClass("パッケージ版"))
	assert.Equal(t, "download", EditionClass("ダウンロード版"))
	assert.Equal(t, "download", EditionClass("ＤＬ版"))
	assert.Equal(t, "limited", EditionClass("初回限定版"))
	assert.Equal(t, "limited", EditionClass("期間限定生産版"))
	assert.Equal(t, "regular", EditionClass("通常版"))
	assert.Equal(t, "budget", EditionClass("廉価版"))
}

// An unrecognized marker is "no opinion", never a mismatch: an OS-compatibility
// reissue says nothing about packaging, and returning a class here would let it
// out-vote a sibling that genuinely agrees.
func TestEditionClassHasNoOpinionOnAnUnknownMarker(t *testing.T) {
	assert.Empty(t, EditionClass("Windows7対応版"))
	assert.Empty(t, EditionClass("日本語版"))
	assert.Empty(t, EditionClass(""))
}

func TestTitleEditionClassReadsAnEmbeddedMarker(t *testing.T) {
	assert.Equal(t, "download", TitleEditionClass("枯れない世界と終わる花 ダウンロード版"))
	assert.Equal(t, "package", TitleEditionClass("しゅがてん！-sugarfull tempering- パッケージ版"))
	assert.Empty(t, TitleEditionClass("Summer Pockets"))
}
