package main

import (
	"strings"

	"golang.org/x/text/unicode/norm"
)

// foldName is the wave's name-equality key: NFKC (width/compat forms), lower,
// every separator dropped (spaces plus the middot family CJK names use), and
// itaiji variants collapsed. Two characters whose folded names are equal are
// tier-1 candidates — the 蓮佛雪之進 / 蓮仏 雪之進 shape the roster-era exact
// matcher missed.
func foldName(s string) string {
	s = norm.NFKC.String(s)
	s = strings.ToLower(s)
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch r {
		case ' ', '\t', '　', '・', '･', '·', '•', '=', '＝':
			continue
		}
		if v, ok := itaiji[r]; ok {
			r = v
		}
		b.WriteRune(r)
	}
	return b.String()
}

// itaiji maps common variant kanji (kyūjitai / jinmeiyō variants that sources
// disagree on when writing the SAME name) onto one representative. Deliberately
// small and name-oriented: this is a fold for candidate DETECTION, the LLM
// judge still sees the verbatim names.
var itaiji = map[rune]rune{
	'佛': '仏', '嶋': '島', '嶌': '島', '彥': '彦', '髙': '高', '﨑': '崎', '嵜': '崎',
	'澤': '沢', '邊': '辺', '邉': '辺', '齊': '斉', '齋': '斎', '龍': '竜', '國': '国',
	'櫻': '桜', '圓': '円', '廣': '広', '惠': '恵', '瀨': '瀬', '賴': '頼', '與': '与',
	'萬': '万', '亞': '亜', '榮': '栄', '實': '実', '壽': '寿', '濱': '浜', '彌': '弥',
	'靜': '静', '眞': '真', '樂': '楽', '鐵': '鉄', '藝': '芸', '會': '会', '學': '学',
	'內': '内', '黑': '黒', '德': '徳', '淺': '浅', '繪': '絵', '禮': '礼', '鹽': '塩',
}

// nameSegments splits a display name into its natural segments (family /
// given / middle parts) on the same separators foldName drops, each segment
// itself folded. 硯川・e・涙香 → [硯川 e 涙香].
func nameSegments(s string) []string {
	s = norm.NFKC.String(s)
	s = strings.ToLower(s)
	f := func(r rune) bool {
		switch r {
		case ' ', '\t', '　', '・', '･', '·', '•', '=', '＝':
			return true
		}
		return false
	}
	var out []string
	for _, seg := range strings.FieldsFunc(s, f) {
		var b strings.Builder
		for _, r := range seg {
			if v, ok := itaiji[r]; ok {
				r = v
			}
			b.WriteRune(r)
		}
		if b.Len() > 0 {
			out = append(out, b.String())
		}
	}
	return out
}

// splitScripts partitions a folded name into its non-ASCII plane (CJK / kana)
// and its ASCII plane, so each plane gets its own similarity floor.
func splitScripts(s string) (cjk, ascii string) {
	var c, a strings.Builder
	for _, r := range s {
		if r <= 127 {
			a.WriteRune(r)
		} else {
			c.WriteRune(r)
		}
	}
	return c.String(), a.String()
}

// lcsRunes is the longest CONTIGUOUS common substring length in runes, the
// similarity floor for tier-3: a shared 硯川 or 涙香 block survives arbitrary
// transliteration of the other segments, while unrelated names rarely share a
// long contiguous run.
func lcsRunes(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	if len(ra) == 0 || len(rb) == 0 {
		return 0
	}
	prev := make([]int, len(rb)+1)
	cur := make([]int, len(rb)+1)
	best := 0
	for i := 1; i <= len(ra); i++ {
		for j := 1; j <= len(rb); j++ {
			if ra[i-1] == rb[j-1] {
				cur[j] = prev[j-1] + 1
				if cur[j] > best {
					best = cur[j]
				}
			} else {
				cur[j] = 0
			}
		}
		prev, cur = cur, prev
	}
	return best
}

// namesSimilar is the tier-3 gate, evaluated over the two sides' full name
// sets (display name + aliases, verbatim strings in, folding done here).
//
//   - a shared full segment of ≥2 runes (family or given name written
//     identically) qualifies — the e / ユーフラジー middle-segment case;
//   - otherwise a contiguous common run of ≥3 runes qualifies, or ≥2 when one
//     side's whole folded name is ≤4 runes (short names never reach 3);
//
// Family members share a surname segment, so this gate is deliberately judged
// by the LLM, never auto-merged on its own.
func namesSimilar(aNames, bNames []string) bool {
	for _, an := range aNames {
		aSegs := nameSegments(an)
		aFold := foldName(an)
		for _, bn := range bNames {
			for _, as := range aSegs {
				if len([]rune(as)) < 2 {
					continue
				}
				for _, bs := range nameSegments(bn) {
					if as == bs {
						return true
					}
				}
			}
			bFold := foldName(bn)
			if aFold == "" || bFold == "" {
				continue
			}
			// The two script planes get separate floors: Latin trigrams
			// ("ber" in 蓝Saber × Berserker) are far too common to signal
			// identity, while a shared CJK/kana run of 2-3 is already rare.
			aCJ, aA := splitScripts(aFold)
			bCJ, bA := splitScripts(bFold)
			need := 3
			if len([]rune(aCJ)) <= 4 || len([]rune(bCJ)) <= 4 {
				need = 2
			}
			if aCJ != "" && bCJ != "" && lcsRunes(aCJ, bCJ) >= need {
				return true
			}
			if len(aA) >= 4 && len(bA) >= 4 && lcsRunes(aA, bA) >= 4 {
				return true
			}
		}
	}
	return false
}
