package llmsuggest

import (
	"context"
	"encoding/json"
	"sort"

	"gorm.io/datatypes"
	"gorm.io/gorm"
)

// RunSanity samples parse-OK subject infoboxes, runs the extraction prompt on
// them, and measures the field-KEY overlap against the deterministic parser's
// output — a free-ground-truth read on the extraction task's baseline quality
// (T3 sanity; pure diagnostic, nothing persisted).
func RunSanity(ctx context.Context, db *gorm.DB, c *Client, n int) (meanOverlap float64, sampled int, err error) {
	if n <= 0 {
		n = 100
	}
	var rows []struct {
		ID     int64          `gorm:"column:id"`
		Raw    string         `gorm:"column:infobox_raw"`
		Parsed datatypes.JSON `gorm:"column:infobox_parsed"`
	}
	// Deterministic spread across the id space.
	if err := db.Raw(`SELECT id, infobox_raw, infobox_parsed FROM src_bangumi.subject
		WHERE parse_error='' AND jsonb_typeof(infobox_parsed->'Fields')='array' AND id % 6000 = 0
		ORDER BY id LIMIT ?`, n).Scan(&rows).Error; err != nil {
		return 0, 0, err
	}
	var sum float64
	var cnt int
	for _, r := range rows {
		gold := parsedKeys(r.Parsed)
		if len(gold) == 0 {
			continue
		}
		content, jerr := extractResidue(ctx, c, r.Raw)
		if jerr != nil {
			continue
		}
		got := extractedKeys(content)
		hit := 0
		for k := range gold {
			if got[k] {
				hit++
			}
		}
		sum += float64(hit) / float64(len(gold))
		cnt++
	}
	if cnt > 0 {
		meanOverlap = sum / float64(cnt)
	}
	return meanOverlap, cnt, nil
}

func parsedKeys(j datatypes.JSON) map[string]bool {
	var box struct {
		Fields []struct{ Key string }
	}
	_ = json.Unmarshal(j, &box)
	out := map[string]bool{}
	for _, f := range box.Fields {
		if f.Key != "" {
			out[f.Key] = true
		}
	}
	return out
}

func extractedKeys(content []byte) map[string]bool {
	var box struct {
		Fields []struct{ Key string }
	}
	_ = json.Unmarshal(content, &box)
	out := map[string]bool{}
	for _, f := range box.Fields {
		if f.Key != "" {
			out[f.Key] = true
		}
	}
	return out
}

// LayerMetrics is same/different confusion + P/R for one gold source_rule (or
// the overall aggregate), reported both raw and with unsure-excluded.
type LayerMetrics struct {
	Layer              string
	N                  int
	TP, FP, FN, TN     int // same = positive
	Unsure             int
	Precision, Recall  float64
	Accuracy           float64
	AccuracyExclUnsure float64
}

// Calibrate computes per-layer + overall metrics from the persisted goldset
// verdicts — the trust baseline every future name task reads.
func Calibrate(db *gorm.DB, model, promptVersion string) ([]LayerMetrics, error) {
	var rows []NamePairJudgment
	if err := db.Where("task = ? AND model = ? AND prompt_version = ?", "goldset", model, promptVersion).
		Find(&rows).Error; err != nil {
		return nil, err
	}
	byLayer := map[string][]NamePairJudgment{}
	for _, r := range rows {
		byLayer[r.SourceRule] = append(byLayer[r.SourceRule], r)
		byLayer["__overall__"] = append(byLayer["__overall__"], r)
	}
	layers := make([]string, 0, len(byLayer))
	for l := range byLayer {
		layers = append(layers, l)
	}
	sort.Strings(layers)
	var out []LayerMetrics
	for _, l := range layers {
		out = append(out, computeMetrics(l, byLayer[l]))
	}
	return out, nil
}

func computeMetrics(layer string, rows []NamePairJudgment) LayerMetrics {
	m := LayerMetrics{Layer: layer, N: len(rows)}
	decided := 0
	correct, correctDecided := 0, 0
	for _, r := range rows {
		if r.Error != "" || r.Verdict == VerdictUnsure {
			m.Unsure++ // treat error rows as unsure for metrics
			continue
		}
		decided++
		goldSame := r.GoldLabel == VerdictSame
		predSame := r.Verdict == VerdictSame
		switch {
		case goldSame && predSame:
			m.TP++
		case !goldSame && predSame:
			m.FP++
		case goldSame && !predSame:
			m.FN++
		default:
			m.TN++
		}
		if goldSame == predSame {
			correct++
			correctDecided++
		}
	}
	if m.TP+m.FP > 0 {
		m.Precision = float64(m.TP) / float64(m.TP+m.FP)
	}
	if m.TP+m.FN > 0 {
		m.Recall = float64(m.TP) / float64(m.TP+m.FN)
	}
	if m.N > 0 {
		m.Accuracy = float64(correct) / float64(m.N)
	}
	if decided > 0 {
		m.AccuracyExclUnsure = float64(correctDecided) / float64(decided)
	}
	return m
}
