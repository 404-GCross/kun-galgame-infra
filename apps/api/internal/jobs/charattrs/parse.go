package charattrs

import (
	"math"
	"regexp"
	"strconv"
	"strings"

	"api/internal/platform/catalog/model"
)

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

func vndbGate(v int16, lo, hi int16) *int16 {
	if v > 0 && inRange(v, lo, hi) {
		return i16p(v)
	}
	return nil
}

func vndbCup(c string) *string {
	c = strings.ToUpper(strings.TrimSpace(c))
	if c == "" {
		return nil
	}
	return strp(c)
}

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
	return bgmBirthday{keepRaw: true}
}

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

var reFirstNumber = regexp.MustCompile(`(\d+(?:\.\d+)?)`)

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
	if m := reCupParen.FindStringSubmatch(v); m != nil {
		out.cup = strp(strings.ToUpper(m[1]))
	} else if m := reCupWord.FindStringSubmatch(v); m != nil {
		out.cup = strp(strings.ToUpper(m[1]))
	}
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

func ParseHeightCM(raw string) (v *int16, found bool) {
	m := parseBGMMeasure(raw, minHeight, maxHeight)
	return m.value, m.found
}

func ParseWeightKG(raw string) (v *int16, found bool) {
	m := parseBGMMeasure(raw, minWeight, maxWeight)
	return m.value, m.found
}

func ParseBloodType(raw string) *int16 { return parseBGMBlood(raw) }

func ParseBWH(raw string) (bust, waist, hip *int16, cup *string) {
	p := parseBGMBWH(raw)
	return p.bust, p.waist, p.hip, p.cup
}

func ParseBirthdayMD(raw string) (month, day *int16) {
	b := parseBGMBirthday(raw)
	return b.month, b.day
}
