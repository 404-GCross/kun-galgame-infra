package llmsuggest

import (
	"bufio"
	"encoding/json"
	"os"
	"sort"
	"strings"
	"unicode"

	"gorm.io/gorm"
)

// GoldPair is one labeled name pair in the calibration set. source_rule records
// the derivation layer so the calibration report can break metrics down per
// axis (the reviewer's nail #2: a single aggregate P/R hides per-axis weakness).
type GoldPair struct {
	A          string `json:"a"`
	B          string `json:"b"`
	Label      string `json:"label"` // "same" | "different"
	SourceRule string `json:"source_rule"`
}

// GoldSetStats reports the per-layer composition (nail #1: written to the report).
type GoldSetStats struct {
	Layers   map[string]int `json:"layers"`
	Positive int            `json:"positive"`
	Negative int            `json:"negative"`
	Total    int            `json:"total"`
	// Layer-b paren filtering audit.
	ParenFilteredRole    int `json:"paren_filtered_role"`
	ParenFilteredCircle  int `json:"paren_filtered_circle"`
	ParenFilteredPersona int `json:"paren_filtered_persona"`
}

// Row structs for the source queries.
type aRow struct {
	Name string `gorm:"column:name"`
	CN   string `gorm:"column:cn"`
}
type cRow struct {
	Name  string `gorm:"column:name"`
	Alias string `gorm:"column:alias"`
}
type egRow struct {
	Name    string `gorm:"column:name"`
	Betumei *int   `gorm:"column:betumei"`
}

// per-layer budgets — total lands in the 200-500 band, roughly balanced.
const (
	budgetLayerA        = 90 // bangumi CN↔JP
	budgetLayerATrivial = 18 // ≤20% of layer a may be pure-kanji trad/simp folds
	budgetLayerC        = 90 // bangumi 别名 JP↔JP 名义
	budgetLayerB        = 30 // EG paren (after filtering)
	budgetEasyNegEach   = 60 // per domain (bangumi, eg)
	budgetHardNegEach   = 40 // per domain
)

// roleDisambiguators are parenthetical role tags that are NOT aliases —
// "七瀬(声優)" means "the voice actor named 七瀬", not an alternate name.
var roleDisambiguators = map[string]bool{
	"声優": true, "声优": true, "歌手": true, "原画": true, "シナリオ": true,
	"音楽": true, "監督": true, "脚本": true, "作曲": true, "編曲": true,
	"作詞": true, "主題歌": true, "ボーカル": true, "vocal": true, "cv": true,
	"イラスト": true, "絵": true, "画": true, "文": true, "曲": true, "歌": true,
}

// BuildGoldSet derives the layered calibration set from the local Bangumi
// (catalog) and erogamespace (eg) sources and writes it as JSONL. Selection is
// deterministic (ordered by id, first-N per layer) so the artifact is
// reproducible against the same local dumps.
func BuildGoldSet(catalog, eg *gorm.DB, path string) (GoldSetStats, error) {
	stats := GoldSetStats{Layers: map[string]int{}}
	var pairs []GoldPair
	seen := map[string]bool{} // dedup by unordered normalized key

	add := func(a, b, label, rule string) bool {
		a, b = strings.TrimSpace(a), strings.TrimSpace(b)
		if a == "" || b == "" || a == b {
			return false
		}
		k := pairKey(a, b)
		if seen[k] {
			return false
		}
		seen[k] = true
		pairs = append(pairs, GoldPair{A: a, B: b, Label: label, SourceRule: rule})
		stats.Layers[rule]++
		return true
	}

	// --- Layer a: Bangumi 简体中文名 ↔ JP name (CN↔JP) ---
	var aRows []aRow
	if err := catalog.Raw(`
		WITH src AS MATERIALIZED (
			SELECT id, name, infobox_parsed->'Fields' AS fields FROM src_bangumi.person
			WHERE parse_error='' AND jsonb_typeof(infobox_parsed->'Fields')='array')
		SELECT s.name, f->>'Value' AS cn
		FROM src s, jsonb_array_elements(s.fields) f
		WHERE f->>'Key'='简体中文名' AND btrim(coalesce(f->>'Value',''))<>'' AND f->>'Value' <> s.name
		ORDER BY s.id`).Scan(&aRows).Error; err != nil {
		return stats, err
	}
	nonTrivial, trivial := 0, 0
	for _, r := range aRows {
		if nonTrivial+trivial >= budgetLayerA {
			break
		}
		// "trivial" proxy for 繁简折叠: JP name is pure-CJK (no kana/latin), so
		// the CN name is almost always just a trad→simp fold. Cap these ≤20%.
		if isPureCJK(r.Name) {
			if trivial >= budgetLayerATrivial {
				continue
			}
			if add(r.Name, r.CN, VerdictSame, "bangumi-cn-name-trivial") {
				trivial++
			}
			continue
		}
		if nonTrivial >= budgetLayerA-budgetLayerATrivial {
			continue
		}
		if add(r.Name, r.CN, VerdictSame, "bangumi-cn-name") {
			nonTrivial++
		}
	}

	// --- Layer c: Bangumi 别名 array ↔ JP name (JP↔JP 名义) ---
	var cRows []cRow
	if err := catalog.Raw(`
		WITH src AS MATERIALIZED (
			SELECT id, name, infobox_parsed->'Fields' AS fields FROM src_bangumi.person
			WHERE parse_error='' AND jsonb_typeof(infobox_parsed->'Fields')='array')
		SELECT s.name, it->>'Value' AS alias
		FROM src s, jsonb_array_elements(s.fields) f,
		     jsonb_array_elements(CASE WHEN jsonb_typeof(f->'Items')='array' THEN f->'Items' ELSE '[]'::jsonb END) it
		WHERE f->>'Key'='别名' AND btrim(coalesce(it->>'Value',''))<>'' AND it->>'Value' <> s.name
		ORDER BY s.id`).Scan(&cRows).Error; err != nil {
		return stats, err
	}
	for _, r := range cRows {
		if stats.Layers["bangumi-alias"] >= budgetLayerC {
			break
		}
		add(r.Name, r.Alias, VerdictSame, "bangumi-alias")
	}

	// --- Layer b: EG creaters name "A(B)" paren split (with semantic filter) ---
	var egNames []egRow
	if err := eg.Raw(`SELECT raw->>'name' AS name,
		CASE WHEN jsonb_typeof(raw->'betumei')='number' THEN (raw->>'betumei')::int END AS betumei
		FROM creaters WHERE btrim(coalesce(raw->>'name',''))<>'' ORDER BY id`).Scan(&egNames).Error; err != nil {
		return stats, err
	}
	// Scan ALL paren rows so the filter tally is complete (reviewer nail #1b),
	// admitting up to the budget.
	for _, r := range egNames {
		main, inner, ok := splitParen(r.Name)
		if !ok {
			continue
		}
		for _, alias := range strings.Split(inner, "、") {
			alias = strings.TrimSpace(alias)
			switch {
			case alias == "":
				continue
			case roleDisambiguators[alias]:
				stats.ParenFilteredRole++
				continue
			case strings.HasPrefix(strings.ToLower(alias), "circle"):
				stats.ParenFilteredCircle++
				continue
			case strings.ContainsRune(alias, '≠'):
				stats.ParenFilteredPersona++
				continue
			}
			if stats.Layers["eg-paren"] < budgetLayerB {
				add(main, alias, VerdictSame, "eg-paren")
			}
		}
	}

	// --- betumei layer: the 5 EG integer alias links (truth is fine, just few) ---
	if err := addBetumei(eg, add); err != nil {
		return stats, err
	}

	// --- Negatives ---
	// Easy: deterministic cross-person / cross-creater pairs.
	bangumiNames := distinctNames(cRows, aRows)
	addEasyNegatives(bangumiNames, "cross-person-bangumi", budgetEasyNegEach, add)
	egPlain := plainEGNames(egNames)
	addEasyNegatives(egPlain, "cross-creater-eg", budgetEasyNegEach, add)
	// Hard: same leading-char (surname) but different person.
	addHardNegatives(bangumiNames, "same-surname-bangumi", budgetHardNegEach, add)
	addHardNegatives(egPlain, "same-surname-eg", budgetHardNegEach, add)

	for _, p := range pairs {
		if p.Label == VerdictSame {
			stats.Positive++
		} else {
			stats.Negative++
		}
	}
	stats.Total = len(pairs)
	return stats, writeJSONL(path, pairs)
}

// addBetumei adds the (name, betumei-target.name) same-person pairs.
func addBetumei(eg *gorm.DB, add func(a, b, label, rule string) bool) error {
	var rows []struct {
		Name       string `gorm:"column:name"`
		TargetName string `gorm:"column:target_name"`
	}
	if err := eg.Raw(`
		SELECT a.raw->>'name' AS name, b.raw->>'name' AS target_name
		FROM creaters a JOIN creaters b ON (a.raw->>'betumei')::int = b.id
		WHERE jsonb_typeof(a.raw->'betumei')='number' AND (a.raw->>'betumei')::int <> 0
		ORDER BY a.id`).Scan(&rows).Error; err != nil {
		return err
	}
	for _, r := range rows {
		add(r.Name, r.TargetName, VerdictSame, "eg-betumei")
	}
	return nil
}

// addEasyNegatives pairs name[i] with name[i+len/2] — two clearly different
// people, deterministic.
func addEasyNegatives(names []string, rule string, budget int, add func(a, b, label, rule string) bool) {
	n := len(names)
	if n < 2 {
		return
	}
	added := 0
	for i := 0; i < n && added < budget; i++ {
		j := (i + n/2) % n
		if i == j {
			continue
		}
		if add(names[i], names[j], VerdictDifferent, rule) {
			added++
		}
	}
}

// addHardNegatives pairs two different names sharing the same leading rune
// (surname) but differing in the remainder — the "same surname, different
// person" trap.
func addHardNegatives(names []string, rule string, budget int, add func(a, b, label, rule string) bool) {
	byHead := map[rune][]string{}
	for _, nm := range names {
		r := []rune(nm)
		// Group by the leading rune only when it is a Han ideograph — a real
		// Japanese surname character. This keeps "same surname, different
		// person" meaningful and rejects symbol/latin-prefixed group names
		// (&CAST, .LIVE) that merely share leading punctuation.
		if len(r) < 2 || !unicode.Is(unicode.Han, r[0]) {
			continue
		}
		byHead[r[0]] = append(byHead[r[0]], nm)
	}
	heads := make([]rune, 0, len(byHead))
	for h := range byHead {
		heads = append(heads, h)
	}
	sort.Slice(heads, func(i, j int) bool { return heads[i] < heads[j] })
	added := 0
	for _, h := range heads {
		g := byHead[h]
		for i := 0; i+1 < len(g) && added < budget; i += 2 {
			if !strings.HasPrefix(g[i+1], g[i]) && !strings.HasPrefix(g[i], g[i+1]) {
				if add(g[i], g[i+1], VerdictDifferent, rule) {
					added++
				}
			}
		}
		if added >= budget {
			break
		}
	}
}

func distinctNames(cRows []cRow, aRows []aRow) []string {
	seen := map[string]bool{}
	var out []string
	push := func(s string) {
		s = strings.TrimSpace(s)
		if s != "" && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	for _, r := range aRows {
		push(r.Name)
	}
	for _, r := range cRows {
		push(r.Name)
	}
	return out
}

func plainEGNames(egNames []egRow) []string {
	seen := map[string]bool{}
	var out []string
	for _, r := range egNames {
		nm := r.Name
		if m, _, ok := splitParen(nm); ok {
			nm = m
		}
		nm = strings.TrimSpace(nm)
		if nm != "" && !seen[nm] {
			seen[nm] = true
			out = append(out, nm)
		}
	}
	return out
}

// splitParen splits "A(B)" / "A（B）" into (A, B). Returns ok=false when there
// is no trailing parenthetical.
func splitParen(name string) (main, inner string, ok bool) {
	name = strings.TrimSpace(name)
	var openR, closeR rune
	if strings.HasSuffix(name, ")") {
		openR, closeR = '(', ')'
	} else if strings.HasSuffix(name, "）") {
		openR, closeR = '（', '）'
	} else {
		return "", "", false
	}
	i := strings.LastIndex(name, string(openR))
	if i <= 0 {
		return "", "", false
	}
	main = strings.TrimSpace(name[:i])
	inner = strings.TrimSpace(strings.TrimSuffix(name[i+len(string(openR)):], string(closeR)))
	if main == "" || inner == "" {
		return "", "", false
	}
	return main, inner, true
}

// isPureCJK reports whether s is entirely CJK ideographs (no kana, latin,
// digits) — the marker of a name whose CN rendering is a mere trad→simp fold.
func isPureCJK(s string) bool {
	has := false
	for _, r := range s {
		if unicode.Is(unicode.Han, r) {
			has = true
			continue
		}
		if unicode.IsSpace(r) {
			continue
		}
		return false // kana / latin / digit / punct
	}
	return has
}

func pairKey(a, b string) string {
	a, b = strings.ToLower(a), strings.ToLower(b)
	if a > b {
		a, b = b, a
	}
	return a + "\x00" + b
}

func writeJSONL(path string, pairs []GoldPair) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	w := bufio.NewWriter(f)
	enc := json.NewEncoder(w)
	for _, p := range pairs {
		if err := enc.Encode(p); err != nil {
			return err
		}
	}
	return w.Flush()
}

// LoadGoldSet reads a goldset JSONL file.
func LoadGoldSet(path string) ([]GoldPair, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []GoldPair
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var p GoldPair
		if err := json.Unmarshal([]byte(line), &p); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, sc.Err()
}
