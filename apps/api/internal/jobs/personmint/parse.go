package personmint

import (
	"encoding/json"
	"regexp"
	"strconv"
	"strings"

	"api/internal/platform/catalog/model"
)

const (
	keyGender   = "性别"
	keyBirthday = "生日"
)

type bgmFacts struct {
	Gender                 string
	BirthY, BirthM, BirthD *int16
}

type field struct {
	Key   string `json:"Key"`
	Value string `json:"Value"`
}

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

func normGender(raw string) (int16, bool) {
	switch strings.TrimSpace(raw) {
	case "m", "男", "男性", "♂":
		return model.GenderMale, true
	case "f", "女", "女性", "♀":
		return model.GenderFemale, true
	}
	return 0, false
}
