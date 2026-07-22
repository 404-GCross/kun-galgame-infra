package charattrs

import (
	"math"
	"regexp"
	"strconv"
	"strings"

	"api/internal/platform/catalog/model"
)

// attrs is the set of typical-set character attributes one source proposes for
// one character. Every field is a nullable pointer: nil = the source says
// nothing (or a sentinel / out-of-range value that was dropped). The write
// layer applies the survivorship + provenance rules per non-nil field.
type attrs struct {
	month  *int16
	day    *int16
	blood  *int16
	height *int16
	weight *int16
	bust   *int16
	waist  *int16
	hip    *int16
	cup    *string
	gender *int16
}

// Range gates (refs/proj/81 范围哨卡). A parsed value outside its gate never
// reaches a real column; the Bangumi lane preserves the raw string in extra and
// counts it (out_of_range). VNDB out-of-range values are simply dropped (its
// typed columns carry the same garbage a Bangumi free-text field would, so the
// gate protects both).
const (
	minMonth, maxMonth   = 1, 12
	minDay, maxDay       = 1, 31
	minHeight, maxHeight = 50, 300
	minWeight, maxWeight = 20, 300
	minBWH, maxBWH       = 30, 200
)

func inRange(v, lo, hi int16) bool { return v >= lo && v <= hi }

func i16p(v int16) *int16   { return &v }
func strp(v string) *string { return &v }

// --- shared value hygiene ---

// normalizeWidth folds full-width ASCII letters/digits to half-width so
// "Ａ型" / "１６０cm" parse like their half-width forms. Non-ASCII runes pass
// through untouched.
func normalizeWidth(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= '０' && r <= '９':
			b.WriteRune(r - '０' + '0')
		case r >= 'Ａ' && r <= 'Ｚ':
			b.WriteRune(r - 'Ａ' + 'A')
		case r >= 'ａ' && r <= 'ｚ':
			b.WriteRune(r - 'ａ' + 'a')
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// bgmUnknownSentinels are the recognized "unknown / not applicable" tokens in a
// Bangumi infobox value. A field whose value is one of these carries no
// information: it is dropped entirely (no column, no extra). 无/無/なし are
// deliberately NOT global sentinels — under 性别 they read as a genuine "other"
// gender expression, which the gender normalizer handles on its own terms.
var bgmUnknownSentinels = map[string]bool{
	"不明": true, "未知": true, "不详": true, "不詳": true, "未詳": true,
	"未公開": true, "未公开": true, "非公開": true, "非公开": true, "保密": true,
}

// isUnknownSentinel reports whether a value is an unknown token or a run of only
// placeholder punctuation (?, ？, -, — …) — e.g. a bare "？" or "???".
func isUnknownSentinel(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" {
		return true
	}
	if bgmUnknownSentinels[s] {
		return true
	}
	for _, r := range s {
		switch r {
		case '?', '？', '-', '—', '−', '–', '‐', ' ', '　':
		default:
			return false
		}
	}
	return true
}

// --- VNDB typed-column decoders (src_vndb.chars) ---

// vndbSexGender maps VNDB's apparent-sex enum to the Gender vocabulary: m→male,
// f→female, any other non-empty value (b=both, n=none) → other, ""→unknown.
// The spoiler columns (spoil_sex/spoil_gender/gender) are never read.
func vndbSexGender(sex string) *int16 {
	switch strings.ToLower(strings.TrimSpace(sex)) {
	case "m":
		return i16p(model.GenderMale)
	case "f":
		return i16p(model.GenderFemale)
	case "":
		return nil
	default:
		return i16p(model.GenderOther)
	}
}

// vndbBlood maps VNDB's bloodt enum to the BloodType vocabulary. "unknown"/""
// (the sentinels) → nil.
func vndbBlood(b string) *int16 {
	switch strings.ToLower(strings.TrimSpace(b)) {
	case "a":
		return i16p(model.BloodTypeA)
	case "b":
		return i16p(model.BloodTypeB)
	case "ab":
		return i16p(model.BloodTypeAB)
	case "o":
		return i16p(model.BloodTypeO)
	default:
		return nil
	}
}

// vndbBirthday decodes VNDB's mmdd-encoded birthday (920 = Sep 20; 0 = unset).
// Returns (month, day) only when both are in range.
func vndbBirthday(bday int16) (month, day *int16) {
	if bday <= 0 {
		return nil, nil
	}
	m := bday / 100
	d := bday % 100
	if !inRange(m, minMonth, maxMonth) || !inRange(d, minDay, maxDay) {
		return nil, nil
	}
	return i16p(m), i16p(d)
}

// vndbGate returns v when it is positive and inside [lo,hi], else nil (0/NULL =
// unset sentinel, out-of-range = garbage).
func vndbGate(v int16, lo, hi int16) *int16 {
	if v > 0 && inRange(v, lo, hi) {
		return i16p(v)
	}
	return nil
}

// vndbCup returns the upper-cased cup token, or nil when empty.
func vndbCup(c string) *string {
	c = strings.ToUpper(strings.TrimSpace(c))
	if c == "" {
		return nil
	}
	return strp(c)
}

// --- Bangumi infobox value parsers (src_bangumi.character.infobox_parsed) ---

// bgmBirthday is the parse of a 生日 value. keepRaw asks the caller to preserve
// the original string in extra — set when a year is present (dropped from the
// month/day columns), the value has trailing/other text, or a part is
// out-of-range: information the two columns cannot hold (refs/proj/81).
type bgmBirthday struct {
	month   *int16
	day     *int16
	keepRaw bool
}

var (
	reBgmBirthday  = regexp.MustCompile(`(?:(\d{1,4})\s*年)?\s*(\d{1,2})\s*月\s*(\d{1,2})\s*日`)
	reBgmMonthOnly = regexp.MustCompile(`^\s*(\d{1,2})\s*月\s*$`)
)

func parseBGMBirthday(raw string) bgmBirthday {
	v := normalizeWidth(strings.TrimSpace(raw))
	if isUnknownSentinel(v) {
		return bgmBirthday{}
	}
	if m := reBgmBirthday.FindStringSubmatch(v); m != nil {
		mo, _ := strconv.Atoi(m[2])
		d, _ := strconv.Atoi(m[3])
		out := bgmBirthday{}
		if inRange(int16(mo), minMonth, maxMonth) {
			out.month = i16p(int16(mo))
		}
		if inRange(int16(d), minDay, maxDay) {
			out.day = i16p(int16(d))
		}
		// Keep the raw string when it holds more than the two columns capture:
		// a year, an out-of-range part, or surrounding text.
		out.keepRaw = m[1] != "" || out.month == nil || out.day == nil || m[0] != v
		return out
	}
	if m := reBgmMonthOnly.FindStringSubmatch(v); m != nil {
		mo, _ := strconv.Atoi(m[1])
		out := bgmBirthday{}
		if inRange(int16(mo), minMonth, maxMonth) {
			out.month = i16p(int16(mo))
		}
		out.keepRaw = out.month == nil
		return out
	}
	// Non-sentinel but unparseable (e.g. "夏", a season) — preserve verbatim.
	return bgmBirthday{keepRaw: true}
}

// parseBGMBlood maps a 血型 value to the BloodType vocabulary. A non-enum but
// non-sentinel value (fictional types like X型/F型) returns nil and is dropped
// (not preserved — the column vocabulary cannot honor it).
func parseBGMBlood(raw string) *int16 {
	v := normalizeWidth(strings.TrimSpace(raw))
	v = strings.TrimSpace(strings.TrimSuffix(strings.ToUpper(v), "型"))
	switch v {
	case "A":
		return i16p(model.BloodTypeA)
	case "B":
		return i16p(model.BloodTypeB)
	case "AB":
		return i16p(model.BloodTypeAB)
	case "O":
		return i16p(model.BloodTypeO)
	default:
		return nil
	}
}

// bgmGender maps a 性别/性別 value to the Gender vocabulary by unambiguous
// token: male markers (男/雄/♂/公) without any female marker → male; female
// markers (女/雌/♀/母) without any male marker → female. A value carrying both
// (男/女, 男→女, 雄性50%｜雌性50%) or neither (无性别, 扶她) is not asserted —
// Bangumi 性别 free-text is too varied to safely bucket the tail as "other",
// unlike VNDB's controlled sex enum.
func bgmGender(raw string) *int16 {
	v := normalizeWidth(strings.TrimSpace(raw))
	if isUnknownSentinel(v) {
		return nil
	}
	hasMale := strings.ContainsAny(v, "男雄♂") || strings.Contains(v, "公")
	hasFemale := strings.ContainsAny(v, "女雌♀母")
	switch {
	case hasMale && !hasFemale:
		return i16p(model.GenderMale)
	case hasFemale && !hasMale:
		return i16p(model.GenderFemale)
	default:
		return nil
	}
}

// reFirstNumber pulls the first integer/decimal out of a measurement string
// ("165cm" → 165, "約 48.5 kg" → 48.5).
var reFirstNumber = regexp.MustCompile(`(\d+(?:\.\d+)?)`)

// bgmMeasure is the parse of a 身高/体重 value. found reports a number was
// present; inRange whether it passed the gate. When found && !inRange the
// caller preserves the raw string in extra and counts it out_of_range.
type bgmMeasure struct {
	value   *int16
	found   bool
	inRange bool
}

func parseBGMMeasure(raw string, lo, hi int16) bgmMeasure {
	v := normalizeWidth(strings.TrimSpace(raw))
	if isUnknownSentinel(v) {
		return bgmMeasure{}
	}
	s := reFirstNumber.FindString(v)
	if s == "" {
		return bgmMeasure{}
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return bgmMeasure{}
	}
	n := int16(math.Round(f))
	if !inRange(n, lo, hi) {
		return bgmMeasure{found: true}
	}
	return bgmMeasure{value: i16p(n), found: true, inRange: true}
}

// bgmBWH is the parse of a BWH value: three measurements + an embedded cup.
// oor is true when a number was present but out of range (the caller preserves
// the raw string + counts it).
type bgmBWH struct {
	bust  *int16
	waist *int16
	hip   *int16
	cup   *string
	oor   bool
}

var (
	reBWHLabeled = regexp.MustCompile(`(?i)B\s*(\d{2,3})\D{0,6}?W\s*(\d{2,3})\D{0,6}?H\s*(\d{2,3})`)
	reBWHBare    = regexp.MustCompile(`(\d{2,3})\s*/\s*(\d{2,3})\s*/\s*(\d{2,3})`)
	reCupParen   = regexp.MustCompile(`\(\s*([A-Za-z]{1,4})\s*\)`)
	reCupWord    = regexp.MustCompile(`(?i)([A-Za-z]{1,4})\s*(?:カップ|cup)`)
)

func parseBGMBWH(raw string) bgmBWH {
	v := normalizeWidth(strings.TrimSpace(raw))
	if isUnknownSentinel(v) {
		return bgmBWH{}
	}
	var out bgmBWH
	// Cup: "(E)" or "Eカップ" / "E cup".
	if m := reCupParen.FindStringSubmatch(v); m != nil {
		out.cup = strp(strings.ToUpper(m[1]))
	} else if m := reCupWord.FindStringSubmatch(v); m != nil {
		out.cup = strp(strings.ToUpper(m[1]))
	}
	// Three measurements: labeled B../W../H.. first, then bare a/b/c.
	var nums []string
	if m := reBWHLabeled.FindStringSubmatch(v); m != nil {
		nums = m[1:4]
	} else if m := reBWHBare.FindStringSubmatch(v); m != nil {
		nums = m[1:4]
	}
	if nums != nil {
		dst := []**int16{&out.bust, &out.waist, &out.hip}
		for i, s := range nums {
			n, _ := strconv.Atoi(s)
			if inRange(int16(n), minBWH, maxBWH) {
				*dst[i] = i16p(int16(n))
			} else {
				out.oor = true
			}
		}
	}
	return out
}
