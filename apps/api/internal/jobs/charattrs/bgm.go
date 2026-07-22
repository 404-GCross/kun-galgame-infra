package charattrs

import (
	"encoding/json"
	"strings"

	"gorm.io/datatypes"
)

// maxExtraValueBytes caps a single extra value; longer strings are truncated
// and counted (refs/proj/81). No real value approaches this today (the whole
// corpus has zero >2KB infobox values) — it is a guard against a pathological
// future dump, not a hot path.
const maxExtraValueBytes = 2048

// infobox mirrors the wiki-parser-go output shape stored in
// src_bangumi.character.infobox_parsed.
type infobox struct {
	Fields []infoField `json:"Fields"`
}

type infoField struct {
	Key   string     `json:"Key"`
	Value string     `json:"Value"`
	Array bool       `json:"Array"`
	Items []infoItem `json:"Items"`
}

type infoItem struct {
	Key   string `json:"Key"`
	Value string `json:"Value"`
}

// bgmResult is one character's Bangumi-derived proposal: the promoted attrs,
// the long-tail extra map (the "bgm" namespace payload), and the run counters.
type bgmResult struct {
	attrs      attrs
	extra      map[string]any // long-tail + preserved raw promotion strings
	outOfRange int
	truncated  int
}

// parseBGMInfobox walks one infobox: promotion keys → typed columns (with
// out-of-range / year / abnormal raw strings preserved into extra), exclusion
// keys dropped, everything else folded into the extra long-tail.
func parseBGMInfobox(raw datatypes.JSON) bgmResult {
	res := bgmResult{extra: map[string]any{}}
	if len(raw) == 0 {
		return res
	}
	var box infobox
	if err := json.Unmarshal(raw, &box); err != nil {
		return res
	}
	for _, f := range box.Fields {
		key := strings.TrimSpace(f.Key)
		if key == "" {
			continue
		}
		switch {
		case key == keyBirthday:
			b := parseBGMBirthday(f.Value)
			res.attrs.month, res.attrs.day = b.month, b.day
			if b.keepRaw {
				res.putExtra(key, f.Value)
			}
		case key == keyBlood:
			res.attrs.blood = parseBGMBlood(f.Value)
		case key == keyHeight:
			res.measure(key, f.Value, minHeight, maxHeight, &res.attrs.height)
		case key == keyWeight:
			res.measure(key, f.Value, minWeight, maxWeight, &res.attrs.weight)
		case key == keyBWH:
			w := parseBGMBWH(f.Value)
			res.attrs.bust, res.attrs.waist, res.attrs.hip = w.bust, w.waist, w.hip
			if w.cup != nil {
				res.attrs.cup = w.cup
			}
			if w.oor {
				res.outOfRange++
				res.putExtra(key, f.Value)
			}
		case isGenderKey(key):
			if g := bgmGender(f.Value); g != nil {
				res.attrs.gender = g
			}
		case isExcludedKey(key):
			// dropped: name/alias/citation/VA — carried by other waves.
		default:
			res.collectLongtail(key, f)
		}
	}
	return res
}

// measure runs a height/weight parse and either sets the column or, on an
// out-of-range number, preserves the raw string and counts it.
func (r *bgmResult) measure(key, raw string, lo, hi int16, dst **int16) {
	m := parseBGMMeasure(raw, lo, hi)
	if m.inRange {
		*dst = m.value
		return
	}
	if m.found { // a number was present but out of gate
		r.outOfRange++
		r.putExtra(key, raw)
	}
}

// collectLongtail folds one non-promotion, non-excluded field into extra: an
// Array field flattens its Items to a []string, a scalar keeps its value.
func (r *bgmResult) collectLongtail(key string, f infoField) {
	if f.Array && len(f.Items) > 0 {
		var vals []string
		for _, it := range f.Items {
			v := strings.TrimSpace(it.Value)
			if v == "" {
				continue
			}
			if k := strings.TrimSpace(it.Key); k != "" {
				v = k + ": " + v
			}
			vals = append(vals, r.truncate(v))
		}
		if len(vals) > 0 {
			r.extra[key] = vals
		}
		return
	}
	if v := strings.TrimSpace(f.Value); v != "" {
		r.putExtra(key, v)
	}
}

// putExtra stores a scalar extra value (truncating an oversized one).
func (r *bgmResult) putExtra(key, val string) {
	val = strings.TrimSpace(val)
	if val == "" {
		return
	}
	r.extra[key] = r.truncate(val)
}

func (r *bgmResult) truncate(s string) string {
	if len(s) <= maxExtraValueBytes {
		return s
	}
	r.truncated++
	// Cut on a rune boundary at/under the cap.
	b := []byte(s)[:maxExtraValueBytes]
	for len(b) > 0 && b[len(b)-1]&0xC0 == 0x80 {
		b = b[:len(b)-1]
	}
	return string(b)
}
