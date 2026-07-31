package personmint

import (
	"encoding/json"
	"regexp"
	"strconv"
	"strings"

	"api/internal/platform/catalog/model"
)

// Bangumi person infobox keys this wave reads.
const (
	keyGender   = "性别"
	keyBirthday = "生日"
)

// bgmFacts is what one bangumi person contributes to survivorship.
type bgmFacts struct {
	// Gender is the RAW infobox value; normalization happens in normGender so
	// the conflict report can quote what the source actually said.
	Gender                 string
	BirthY, BirthM, BirthD *int16
}

// field is one parsed infobox field — scalar (Value) or array (Items).
type field struct {
	Key   string `json:"Key"`
	Value string `json:"Value"`
}

// parseBangumiFacts unwraps infobox_parsed and picks the two fields this wave
// reads. The Fields member is JSON null on 2 rows and a bare SCALAR on others
// (the wave-81 charattrs finding, re-confirmed by wave 152 §2.5); neither is an
// error and neither may be guessed at — an unreadable infobox simply supplies
// nothing.
func parseBangumiFacts(raw []byte) bgmFacts {
	var out bgmFacts
	if len(raw) == 0 {
		return out
	}
	var envelope struct {
		Fields json.RawMessage `json:"Fields"`
	}
	if json.Unmarshal(raw, &envelope) != nil || len(envelope.Fields) == 0 {
		return out
	}
	var fields []field
	if json.Unmarshal(envelope.Fields, &fields) != nil {
		return out
	}
	for _, f := range fields {
		switch f.Key {
		case keyGender:
			if out.Gender == "" {
				out.Gender = strings.TrimSpace(f.Value)
			}
		case keyBirthday:
			if out.BirthY == nil && out.BirthM == nil && out.BirthD == nil {
				out.BirthY, out.BirthM, out.BirthD = parseBirthday(f.Value)
			}
		}
	}
	return out
}

// Birthday shapes present in the staging mirror, in precedence order. The
// Bangumi wiki has no date type, so the field is free text; these six patterns
// cover 96% of the non-empty values and everything else is deliberately
// dropped rather than guessed (未知 sentinels, "女×2", prose).
var (
	reYMD   = regexp.MustCompile(`^(\d{4})年\s*(\d{1,2})月\s*(\d{1,2})日`)
	reYM    = regexp.MustCompile(`^(\d{4})年\s*(\d{1,2})月`)
	reY     = regexp.MustCompile(`^(\d{4})年`)
	reMD    = regexp.MustCompile(`^(\d{1,2})月\s*(\d{1,2})日`)
	reM     = regexp.MustCompile(`^(\d{1,2})月`)
	reISO   = regexp.MustCompile(`^(\d{4})-(\d{1,2})-(\d{1,2})`)
	reISOYM = regexp.MustCompile(`^(\d{4})-(\d{1,2})$`)
	reYear  = regexp.MustCompile(`^(\d{4})$`)
)

// parseBirthday reads a bangumi 生日 value into the fuzzy three-column date.
// Partial dates are the norm — "12月25日" (no year) and "1978年" (year only)
// are both legitimate and both representable, which is exactly why the model
// has three nullable columns instead of one date.
//
// A day without a month is NOT representable as anything meaningful, so an
// out-of-range month (「1978年13月4日」) drops the day with it rather than
// leaving a stray 4 behind.
func parseBirthday(raw string) (y, m, d *int16) {
	y, m, d = parseBirthdayParts(raw)
	if m == nil {
		d = nil
	}
	return y, m, d
}

func parseBirthdayParts(raw string) (y, m, d *int16) {
	v := strings.TrimSpace(raw)
	if v == "" {
		return nil, nil, nil
	}
	switch {
	case reYMD.MatchString(v):
		g := reYMD.FindStringSubmatch(v)
		return num(g[1]), month(g[2]), day(g[3])
	case reISO.MatchString(v):
		g := reISO.FindStringSubmatch(v)
		return num(g[1]), month(g[2]), day(g[3])
	case reYM.MatchString(v):
		g := reYM.FindStringSubmatch(v)
		return num(g[1]), month(g[2]), nil
	case reISOYM.MatchString(v):
		g := reISOYM.FindStringSubmatch(v)
		return num(g[1]), month(g[2]), nil
	case reY.MatchString(v):
		return num(reY.FindStringSubmatch(v)[1]), nil, nil
	case reYear.MatchString(v):
		return num(reYear.FindStringSubmatch(v)[1]), nil, nil
	case reMD.MatchString(v):
		g := reMD.FindStringSubmatch(v)
		return nil, month(g[1]), day(g[2])
	case reM.MatchString(v):
		return nil, month(reM.FindStringSubmatch(v)[1]), nil
	}
	return nil, nil, nil
}

// num parses a year. Years outside a plausible human range are junk that
// happened to match the shape (page ids, phone numbers).
func num(s string) *int16 {
	n, err := strconv.Atoi(s)
	if err != nil || n < 1850 || n > 2100 {
		return nil
	}
	v := int16(n)
	return &v
}

func month(s string) *int16 { return inRange(s, 1, 12) }
func day(s string) *int16   { return inRange(s, 1, 31) }

func inRange(s string, lo, hi int) *int16 {
	n, err := strconv.Atoi(s)
	if err != nil || n < lo || n > hi {
		return nil
	}
	v := int16(n)
	return &v
}

// normGender folds both sources' vocabularies onto the Gender* constants.
// Anything outside them — 未知, 非二元性别, the handful of prose values, and
// vndb's empty string — is UNKNOWN, not a value: it contributes neither an
// assertion nor a conflict.
func normGender(raw string) (int16, bool) {
	switch strings.TrimSpace(raw) {
	case "m", "男", "男性", "♂":
		return model.GenderMale, true
	case "f", "女", "女性", "♀":
		return model.GenderFemale, true
	}
	return 0, false
}
