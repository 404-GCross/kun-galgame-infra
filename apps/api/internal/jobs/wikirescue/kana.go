package wikirescue

// The KANA CLASSIFIER — the language detector steps u and v share
// (refs/proj/146 §1, refs/plans/10-data-layer-retirement/02 §6.6).
//
// The problem it solves: 3,908 CLAIMED wiki bodies carry text in a zh intro
// column while both the ja and the en column are blank. Two very different
// things hide in that one class — a Japanese ORIGINAL misfiled into the Chinese
// column (a data-entry error to correct, step u) and a genuine Chinese
// translation whose original exists nowhere (a rescue to adopt, step v) — and
// nothing in the schema tells them apart. The text itself has to.
//
// The signal is KANA DENSITY: the share of a text's CJK-script characters that
// are kana. Japanese prose is ~50-70% kana (every particle and inflection);
// Chinese prose is ~0% and only picks up kana when it quotes a Japanese title or
// character name. The measured distribution over the real class is sharply
// bimodal with an EMPTY valley at 0.20-0.25 (see refs/proj/146-artifacts/
// kana-histogram.md), which is what makes a threshold defensible rather than
// arbitrary.
//
// Three deliberate properties:
//
//   - ASYMMETRIC RISK. A false "Japanese" verdict is expensive — step u would
//     move Chinese text into the ja column, blanking the Chinese face AND feeding
//     the wrong source to machine translation. A false "Chinese" verdict merely
//     leaves the text where the pre-flip wiki face already showed it. So the
//     Japanese threshold sits far above the valley (0.45, deep inside the
//     Japanese mode) instead of at it.
//   - A GRAY BAND, PARKED. Between the two verdicts sits a real population the
//     density cannot judge: fields holding the Chinese translation AND the
//     Japanese original together, which no single-column move can split. Those
//     are classified gray, touched by NEITHER step, and parked to JSON for a
//     human ruling.
//   - A MINIMUM VOLUME. A 5-character field ("犯人は誰だ！") or a Chinese line
//     quoting one Japanese title ("**「超昂閃忍ハルカ」的IF Story AVG**", density
//     0.43) produces a density built from single digits, where one character
//     flips the verdict. Under kanaMinCJK the density is not evidence, so a
//     Japanese-looking short field is parked rather than moved.
const (
	// kanaJaDensity is the density at or above which a text is JAPANESE. The
	// Japanese mode peaks at 0.55-0.75; 0.45 implies Japanese contributes ~85%+
	// of the CJK volume even under the worst-case bilingual mix.
	kanaJaDensity = 0.45
	// kanaGrayDensity is the density below which a text is CHINESE — the upper
	// edge of the Chinese mode (782 works sit under 0.20, the valley above it is
	// empty).
	kanaGrayDensity = 0.20
	// kanaMinCJK is the CJK-character count a density needs to mean anything.
	// Roughly one sentence.
	kanaMinCJK = 24
)

// kanaClass is one text's language verdict.
type kanaClass int

const (
	// kanaZh is Chinese (or at least: not detectably Japanese) — step v adopts it
	// as a zh source row and step u leaves it alone.
	kanaZh kanaClass = iota
	// kanaGray is undecidable — bilingual, or too short to measure. NEITHER step
	// touches it; it is parked for adjudication.
	kanaGray
	// kanaJa is Japanese misfiled into a Chinese column — step u reattributes it.
	kanaJa
)

func (c kanaClass) String() string {
	switch c {
	case kanaJa:
		return "ja"
	case kanaGray:
		return "gray"
	default:
		return "zh"
	}
}

// classifyKana returns one text's verdict along with the evidence behind it (the
// density and the CJK volume it was computed over), because the ledger and the
// parked artifact both report the evidence, not just the verdict.
func classifyKana(s string) (kanaClass, float64, int) {
	kana, han := kanaCounts(s)
	cjk := kana + han
	if cjk == 0 {
		// No CJK at all (a Latin-only or Korean field). Not detectably Japanese,
		// so it stays where it is and step v preserves it verbatim.
		return kanaZh, 0, 0
	}
	density := float64(kana) / float64(cjk)
	switch {
	case density < kanaGrayDensity:
		return kanaZh, density, cjk
	case density >= kanaJaDensity && cjk >= kanaMinCJK:
		return kanaJa, density, cjk
	default:
		return kanaGray, density, cjk
	}
}

// kanaCounts counts a text's kana and its han (ideographic) characters.
//
// Two range choices are load-bearing:
//
//   - U+30FB (・, the katakana middle dot) is EXCLUDED from kana even though it
//     lives in the katakana block: Chinese uses it to separate transliterated
//     names, so counting it would push Chinese texts toward the Japanese verdict.
//   - U+30FC (ー, the prolonged sound mark) IS kana — it is a genuine Japanese
//     orthography marker and is vanishingly rare in Chinese.
func kanaCounts(s string) (kana, han int) {
	for _, r := range s {
		switch {
		case r >= 0x3041 && r <= 0x3096, // hiragana
			r >= 0x309D && r <= 0x309F, // hiragana iteration marks + ゟ
			r >= 0x30A1 && r <= 0x30FA, // katakana
			r >= 0x30FC && r <= 0x30FF, // ー and the katakana iteration marks
			r >= 0xFF66 && r <= 0xFF9D: // halfwidth katakana
			kana++
		case r >= 0x4E00 && r <= 0x9FFF, // CJK unified ideographs
			r >= 0x3400 && r <= 0x4DBF, // extension A
			r >= 0xF900 && r <= 0xFAFF: // compatibility ideographs
			han++
		}
	}
	return kana, han
}
