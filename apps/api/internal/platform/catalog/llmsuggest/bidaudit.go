package llmsuggest

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"gorm.io/gorm"
)

// bidAuditSystem judges whether two visual-novel title SETS denote the same game.
const bidAuditSystem = "You decide whether two visual-novel title sets refer to the SAME game. " +
	"You are given the titles a wiki uses for one game and the titles a Bangumi subject uses. " +
	"SAME = the same visual novel (localized or alternate titles, subtitles, kana/romaji, Chinese/Japanese renderings all count). " +
	"DIFFERENT = clearly different works — unrelated games, or a game versus its soundtrack, novel, or anime adaptation. " +
	"Use the release years as a strong signal when given. If unclear, answer \"unsure\". Keep the reason to one short clause."

// BidAuditLayers is the deterministic pre-classification tally.
type BidAuditLayers struct {
	Pass     int
	Boundary int
	Suspect  int
}

// bidPair is one bid-exact pair. PRIMARY names (main title fields) are kept
// apart from ALIASES on purpose: aliases can be smeared/mis-migrated (the
// atled/CLANNAD canary carries CLANNAD's whole alias set on a different game),
// so identity is judged on primaries; alias-only overlap is a smearing signal.
type bidPair struct {
	WorkID      int64
	GalgameID   int64
	BID         int64
	WikiPrimary []string
	WikiAlias   []string
	SubjPrimary []string
	SubjAlias   []string
	WikiYear    int
	SubjYear    int
	Layer       string
}

func (p bidPair) wikiAll() []string {
	return append(append([]string{}, p.WikiPrimary...), p.WikiAlias...)
}
func (p bidPair) subjAll() []string {
	return append(append([]string{}, p.SubjPrimary...), p.SubjAlias...)
}

// RunBidAudit audits every bid-exact pair (step 11 carry-over): a deterministic
// rule layer clears the obvious matches (pass), and the LLM adjudicates the
// boundary + suspect pairs. Detection only — no ref is ever re-graded.
func RunBidAudit(ctx context.Context, wikiDB, catalogDB *gorm.DB, c *Client, opts Options) (BidAuditLayers, int, error) {
	var layers BidAuditLayers
	pairs, err := loadBidPairs(wikiDB, catalogDB)
	if err != nil {
		return layers, 0, err
	}
	for i := range pairs {
		pairs[i].Layer = classifyBidPair(&pairs[i])
		switch pairs[i].Layer {
		case "pass":
			layers.Pass++
		case "suspect":
			layers.Suspect++
		default:
			layers.Boundary++
		}
	}
	if opts.DryRun {
		return layers, dryRunBidAudit(ctx, c, pairs, opts.Limit), nil
	}

	done, err := loadDoneHashes(catalogDB, "src_llm.bid_identity_verdict", opts.Model, PromptBidVerdictV1, "", "")
	if err != nil {
		return layers, 0, err
	}
	var work []bidPair
	for _, p := range pairs {
		if !done[hashInput("bid-audit", fmt.Sprint(p.WorkID), fmt.Sprint(p.BID))] {
			work = append(work, p)
		}
	}
	if opts.Limit > 0 && len(work) > opts.Limit {
		work = work[:opts.Limit]
	}

	var nErrs atomic.Int64
	runPool(ctx, work, opts.Concurrency, func(ctx context.Context, p bidPair) {
		row := BidIdentityVerdict{
			WorkID: p.WorkID, GalgameID: p.GalgameID, BID: p.BID, Layer: p.Layer,
			WikiNames: strings.Join(p.wikiAll(), " | "), SubjectNames: strings.Join(p.subjAll(), " | "),
			Model: opts.Model, PromptVersion: PromptBidVerdictV1,
			InputHash: hashInput("bid-audit", fmt.Sprint(p.WorkID), fmt.Sprint(p.BID)),
		}
		if p.Layer != "pass" { // only boundary + suspect reach the model
			v, jerr := judge(ctx, c, bidAuditSystem, bidAuditUser(p), 256)
			if jerr != nil {
				row.Error = truncate(jerr.Error(), 500)
				nErrs.Add(1)
			} else {
				row.Verdict, row.Reason, row.Confidence = v.Verdict, v.Reason, v.Confidence
			}
		}
		if e := catalogDB.Create(&row).Error; e != nil {
			slog.Error("persist bid verdict", "error", e, "work", p.WorkID, "bid", p.BID)
		}
	})
	_ = recordRun(catalogDB, "bid-audit", opts.Model, PromptBidVerdictV1,
		map[string]int{"pass": layers.Pass, "boundary": layers.Boundary, "suspect": layers.Suspect, "errors": int(nErrs.Load())},
		time.Now(), "")
	return layers, int(nErrs.Load()), nil
}

func bidAuditUser(p bidPair) string {
	wy, sy := "unknown", "unknown"
	if p.WikiYear > 0 {
		wy = fmt.Sprint(p.WikiYear)
	}
	if p.SubjYear > 0 {
		sy = fmt.Sprint(p.SubjYear)
	}
	return fmt.Sprintf("Wiki game titles: %s (year: %s)\nBangumi subject titles: %s (year: %s)",
		strings.Join(p.wikiAll(), " / "), wy, strings.Join(p.subjAll(), " / "), sy)
}

// classifyBidPair is the deterministic rule layer. Identity is judged on
// PRIMARY names; aliases are corroboration, never proof:
//   - pass: the PRIMARY name sets overlap (equal or ≥4-rune containment);
//   - suspect: primaries do NOT overlap AND (aliases overlap — the smearing
//     signature — OR both years present and different);
//   - boundary: everything else → the model decides.
func classifyBidPair(p *bidPair) string {
	if overlaps(normSet(p.WikiPrimary), normSet(p.SubjPrimary)) {
		return "pass"
	}
	if overlaps(normSet(p.wikiAll()), normSet(p.subjAll())) {
		return "suspect" // aliases match but primary titles don't → likely wrong-game / smeared
	}
	if p.WikiYear > 0 && p.SubjYear > 0 && p.WikiYear != p.SubjYear {
		return "suspect"
	}
	return "boundary"
}

// overlaps reports whether any two normalized names are equal or one contains
// the other with ≥4 runes (short fragments never count — they falsely link
// unrelated titles).
func overlaps(a, b map[string]bool) bool {
	for w := range a {
		for s := range b {
			if w == s {
				return true
			}
			if len([]rune(w)) >= 4 && strings.Contains(s, w) {
				return true
			}
			if len([]rune(s)) >= 4 && strings.Contains(w, s) {
				return true
			}
		}
	}
	return false
}

func normSet(names []string) map[string]bool {
	out := map[string]bool{}
	for _, n := range names {
		n = strings.ToLower(strings.Join(strings.Fields(n), ""))
		if n != "" {
			out[n] = true
		}
	}
	return out
}

// loadBidPairs assembles every bid-exact pair with both name sets + years.
func loadBidPairs(wikiDB, catalogDB *gorm.DB) ([]bidPair, error) {
	// (work, bid, galgame_id) from the catalog exact refs.
	var base []struct {
		WorkID    int64 `gorm:"column:work_id"`
		BID       int64 `gorm:"column:bid"`
		GalgameID int64 `gorm:"column:galgame_id"`
	}
	if err := catalogDB.Raw(`
		SELECT r.entity_id AS work_id, r.external_id::bigint AS bid, w.product_work_id AS galgame_id
		FROM catalog_external_ref r JOIN catalog_work w ON w.id = r.entity_id
		WHERE r.source_id = 3 AND r.link_kind = 0 AND w.site = 'galgame_wiki'
		ORDER BY r.entity_id`).Scan(&base).Error; err != nil {
		return nil, err
	}
	galIDs := make([]int64, 0, len(base))
	bids := make([]int64, 0, len(base))
	for _, b := range base {
		galIDs = append(galIDs, b.GalgameID)
		bids = append(bids, b.BID)
	}

	wikiPrimary, wikiAlias, wikiYear, err := loadWikiNameSets(wikiDB, galIDs)
	if err != nil {
		return nil, err
	}
	subjPrimary, subjAlias, subjYear, err := loadSubjectNameSets(catalogDB, bids)
	if err != nil {
		return nil, err
	}

	out := make([]bidPair, 0, len(base))
	for _, b := range base {
		out = append(out, bidPair{
			WorkID: b.WorkID, GalgameID: b.GalgameID, BID: b.BID,
			WikiPrimary: wikiPrimary[b.GalgameID], WikiAlias: wikiAlias[b.GalgameID],
			SubjPrimary: subjPrimary[b.BID], SubjAlias: subjAlias[b.BID],
			WikiYear: wikiYear[b.GalgameID], SubjYear: subjYear[b.BID],
		})
	}
	return out, nil
}

// loadWikiNameSets returns primary (main title fields), alias, and year maps.
func loadWikiNameSets(wikiDB *gorm.DB, galIDs []int64) (primary, alias map[int64][]string, years map[int64]int, err error) {
	primary, alias, years = map[int64][]string{}, map[int64][]string{}, map[int64]int{}
	var grows []struct {
		ID   int64  `gorm:"column:id"`
		Ja   string `gorm:"column:name_ja_jp"`
		Zh   string `gorm:"column:name_zh_cn"`
		En   string `gorm:"column:name_en_us"`
		Tw   string `gorm:"column:name_zh_tw"`
		Year *int   `gorm:"column:year"`
	}
	if err = wikiDB.Raw(`SELECT id, name_ja_jp, name_zh_cn, name_en_us, name_zh_tw,
		CASE WHEN release_date IS NOT NULL THEN EXTRACT(YEAR FROM release_date)::int END AS year
		FROM galgame WHERE id IN ?`, galIDs).Scan(&grows).Error; err != nil {
		return
	}
	for _, g := range grows {
		primary[g.ID] = nonEmpty(g.Ja, g.Zh, g.En, g.Tw)
		if g.Year != nil {
			years[g.ID] = *g.Year
		}
	}
	var arows []struct {
		GalgameID int64  `gorm:"column:galgame_id"`
		Name      string `gorm:"column:name"`
	}
	if err = wikiDB.Raw(`SELECT galgame_id, name FROM galgame_alias WHERE galgame_id IN ? AND btrim(coalesce(name,''))<>''`, galIDs).
		Scan(&arows).Error; err != nil {
		return
	}
	for _, a := range arows {
		alias[a.GalgameID] = append(alias[a.GalgameID], a.Name)
	}
	return
}

// loadSubjectNameSets returns primary (name + name_cn), alias (别名 items), and
// year maps.
func loadSubjectNameSets(catalogDB *gorm.DB, bids []int64) (primary, alias map[int64][]string, years map[int64]int, err error) {
	primary, alias, years = map[int64][]string{}, map[int64][]string{}, map[int64]int{}
	var srows []struct {
		ID   int64  `gorm:"column:id"`
		Name string `gorm:"column:name"`
		CN   string `gorm:"column:name_cn"`
		Date string `gorm:"column:date"`
	}
	if err = catalogDB.Raw(`SELECT id, name, name_cn, date FROM src_bangumi.subject WHERE id IN ?`, bids).
		Scan(&srows).Error; err != nil {
		return
	}
	for _, s := range srows {
		primary[s.ID] = nonEmpty(s.Name, s.CN)
		years[s.ID] = yearOf(s.Date)
	}
	var brows []struct {
		ID    int64  `gorm:"column:id"`
		Alias string `gorm:"column:alias"`
	}
	if err = catalogDB.Raw(`
		WITH src AS MATERIALIZED (
			SELECT id, infobox_parsed->'Fields' AS fields FROM src_bangumi.subject
			WHERE id IN ? AND parse_error='' AND jsonb_typeof(infobox_parsed->'Fields')='array')
		SELECT s.id, it->>'Value' AS alias
		FROM src s, jsonb_array_elements(s.fields) f,
		     jsonb_array_elements(CASE WHEN jsonb_typeof(f->'Items')='array' THEN f->'Items' ELSE '[]'::jsonb END) it
		WHERE f->>'Key'='别名' AND btrim(coalesce(it->>'Value',''))<>''`, bids).Scan(&brows).Error; err != nil {
		return
	}
	for _, b := range brows {
		alias[b.ID] = append(alias[b.ID], b.Alias)
	}
	return
}

// yearOf parses the leading 4 digits of a Bangumi date ("2004-04-28", "") into
// a plausible year, or 0.
func yearOf(date string) int {
	if len(date) < 4 {
		return 0
	}
	y := 0
	for i := 0; i < 4; i++ {
		if date[i] < '0' || date[i] > '9' {
			return 0
		}
		y = y*10 + int(date[i]-'0')
	}
	if y < 1900 || y > 2100 {
		return 0
	}
	return y
}

func nonEmpty(vals ...string) []string {
	var out []string
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			out = append(out, v)
		}
	}
	return out
}

func dryRunBidAudit(ctx context.Context, c *Client, pairs []bidPair, limit int) int {
	if limit <= 0 {
		limit = 5
	}
	// Show a mix: prefer boundary/suspect so the dry run exercises the model.
	sort.SliceStable(pairs, func(i, j int) bool {
		return layerRank(pairs[i].Layer) < layerRank(pairs[j].Layer)
	})
	shown := 0
	for _, p := range pairs {
		if shown >= limit {
			break
		}
		if p.Layer == "pass" {
			continue
		}
		v, err := judge(ctx, c, bidAuditSystem, bidAuditUser(p), 256)
		if err != nil {
			fmt.Printf("[dry] work=%d bid=%d layer=%s ERROR: %v\n", p.WorkID, p.BID, p.Layer, err)
		} else {
			fmt.Printf("[dry] work=%d bid=%d layer=%s → %s conf=%.2f | wiki=%q subj=%q | %s\n",
				p.WorkID, p.BID, p.Layer, v.Verdict, v.Confidence,
				strings.Join(p.wikiAll(), "/"), strings.Join(p.subjAll(), "/"), v.Reason)
		}
		shown++
	}
	return 0
}

func layerRank(l string) int {
	switch l {
	case "suspect":
		return 0
	case "boundary":
		return 1
	default:
		return 2
	}
}

// CanaryVerdict returns the persisted layer + verdict for one (galgame, bid)
// pair — the pipeline-health probe (the atled/CLANNAD pair must NOT pass and
// must NOT be judged same).
func CanaryVerdict(catalogDB *gorm.DB, galgameID, bid int64, model string) (layer, verdict string, found bool) {
	var row BidIdentityVerdict
	err := catalogDB.Where("galgame_id = ? AND bid = ? AND model = ? AND prompt_version = ?",
		galgameID, bid, model, PromptBidVerdictV1).First(&row).Error
	if err != nil {
		return "", "", false
	}
	return row.Layer, row.Verdict, true
}
