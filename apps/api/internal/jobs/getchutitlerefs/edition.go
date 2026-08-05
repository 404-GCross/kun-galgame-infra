package getchutitlerefs

import (
	"regexp"
	"strings"

	"golang.org/x/text/unicode/norm"
)

// EDITIONS, AND WHY THEY ARE THE WHOLE PROBLEM.
//
// The match keys a product against catalog_work_title, which holds the WORK's
// title — bare, with no edition marker. A Getchu product, in contrast, IS one
// edition, and 27% of the crawled titles say so:
//
//	Summer Pockets 初回限定版        紅色天井艶妖綺譚 新装版
//	よつのは DVD-ROM版               虐襲 Windows7対応版
//
// That single fact splits the unresolved population in two, and one mechanism
// answers both halves:
//
//   - a product whose title carries a marker never matches the bare work title
//     at all, so it lands in no_title — invisible, not ambiguous;
//   - a product whose title carries none matches the work, and then every
//     same-day sibling release is equally plausible → ambiguous_release.
//
// Stripping the marker recovers the first group; reading the marker back
// discriminates within the second.
//
// SAFETY. Stripping only ever widens the candidate set — it cannot by itself
// mint anything. A wrongly stripped title still has to resolve to exactly one
// work, agree on the exact release date, and pass roster confirmation before it
// is written exact. The one marker deliberately NOT stripped is 完全版: unlike
// a packaging variant it routinely names a genuinely different product (a
// remake with its own roster and its own date), so folding it into the base
// work would be a claim about identity rather than about packaging.

var (
	// reEditionTail is the structural rule: one or more trailing
	// whitespace-delimited segments ending in 版. It generalizes past any
	// vocabulary — 1,560 of the crawled markers are long-tail forms
	// (価格改定版, 抱き枕カバー付き版, VISTA版) that no closed list would catch.
	// The whitespace requirement is what keeps it off a title that merely ends
	// in that character.
	reEditionTail = regexp.MustCompile(`(?:[[:space:]　]+[^[:space:]　]*版)+$`)

	// glueEditions are the markers common enough to appear with no separator at
	// all. Each is a packaging or distribution variant and never a title.
	glueEditions = []string{
		"ダウンロード版", "パッケージ版", "初回限定版", "期間限定生産版",
		"ＤＬ版", "DL版", "初回版", "通常版", "廉価版", "限定版",
	}

	// keepEditions are markers the tail rule would otherwise strip but that can
	// name a distinct product. See the SAFETY note above.
	keepEditions = []string{"完全版"}
)

// SplitEdition separates a product title into the work it names and the edition
// it is. The edition is returned verbatim; use EditionClass to compare it
// against a catalog release title.
func SplitEdition(title string) (base, edition string) {
	s := strings.TrimSpace(title)
	for _, k := range keepEditions {
		if strings.HasSuffix(s, k) {
			return s, ""
		}
	}
	if loc := reEditionTail.FindStringIndex(s); loc != nil {
		return strings.TrimSpace(s[:loc[0]]), strings.TrimSpace(s[loc[0]:])
	}
	for _, g := range glueEditions {
		if rest := strings.TrimSuffix(s, g); rest != s && strings.TrimSpace(rest) != "" {
			return strings.TrimSpace(rest), g
		}
	}
	return s, ""
}

// editionClasses groups markers that denote the SAME edition in the two
// catalogues under one label. Getchu says DVD-ROM版 where the catalog says
// パッケージ版; both mean "the boxed copy". Order matters: the longest, most
// specific form has to be tested first.
var editionClasses = []struct {
	class   string
	markers []string
}{
	{"download", []string{"ダウンロード版", "ＤＬ版", "DL版", "dl版", "デジタル版"}},
	{"package", []string{"パッケージ版", "DVD-ROM版", "CD-ROM版", "ＤＶＤ版", "DVD版"}},
	{"limited", []string{"初回限定版", "期間限定生産版", "豪華特装版", "特装版", "限定版", "初回版", "デラックス版"}},
	{"budget", []string{"価格改定版", "廉価版", "ベスト版", "BEST版", "普及版", "新装版"}},
	{"regular", []string{"通常版"}},
}

// EditionClass reduces an edition marker to the coarse class the two
// catalogues can actually be compared on. An unrecognized marker returns "",
// which the caller must read as "no opinion" rather than as a mismatch — an
// OS-compatibility reissue (Windows7対応版) says nothing about packaging.
func EditionClass(s string) string {
	s = norm.NFKC.String(strings.ToUpper(strings.TrimSpace(s)))
	for _, ec := range editionClasses {
		for _, m := range ec.markers {
			if strings.Contains(s, norm.NFKC.String(strings.ToUpper(m))) {
				return ec.class
			}
		}
	}
	return ""
}

// TitleEditionClass reads the edition class off a full title, for the catalog
// side where the marker is embedded in catalog_release.title rather than
// supplied separately.
func TitleEditionClass(title string) string {
	_, ed := SplitEdition(title)
	if ed == "" {
		// The catalog writes some markers mid-title ("… ダウンロード版" is a
		// tail, but "Dive1+2　期間限定生産版" is separated by an ideographic
		// space that the tail rule already covers). Fall back to scanning the
		// whole string so an embedded marker is not missed.
		return EditionClass(title)
	}
	return EditionClass(ed)
}
