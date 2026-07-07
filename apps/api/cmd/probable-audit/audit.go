package main

import (
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"

	"api/internal/platform/catalog/model"

	"gorm.io/gorm"
)

// Catalog source ids (catalog_source seed) and the two probable rule tags —
// the same values catalogsync writes; redeclared here because they live in an
// unexported package.
const (
	sourceVNDB    int16 = 2
	sourceBangumi int16 = 3
	sourceEG      int16 = 5

	ruleRosetta   = "rule:eg-vndb-rosetta"
	ruleTitleYear = "rule:title-year-strict"

	wikiGalgameBase = "https://www.kungal.com/galgame/"
	bangumiBase     = "https://bgm.tv/subject/"
	egGameBase      = "https://erogamescape.dyndns.org/~ap2/ero/toukei_kaiseki/game.php?game="
)

// Corroboration states (the vndb axis): does the external entity's OWN vndb id
// agree with the work's exact vndb anchor?
const (
	corrobAgree      = "corroborated" // both present, equal
	corrobContradict = "contradicted" // both present, DIFFERENT — mechanically suspect
	corrobExtNoVNDB  = "ext-no-vndb"  // work has vndb, external entity records none
	corrobWorkNoVNDB = "work-no-vndb" // work has no vndb anchor to check against
)

// auditor holds the three database handles (wiki + catalog + eg, mirroring the
// reconciler) and, once loaded, every probable work-ref with its cross-check
// signals computed.
type auditor struct {
	wiki    *gorm.DB
	catalog *gorm.DB
	eg      *gorm.DB
	refs    []probableRef
}

// probableRef is one probable work→external ref plus its mechanical signals.
type probableRef struct {
	WorkID     int64
	SourceID   int16
	ExternalID string
	Rule       string

	// Cross-check signals.
	WorkVNDB    string // the work's exact vndb anchor
	ExtVNDB     string // the external entity's own vndb id (EG column / bangumi infobox)
	Corrob      string // one of corrob*
	AlsoRosetta bool   // title-strict only: the SAME work also carries an eg-rosetta ref
	WorkYear    int
	ExtYear     int

	// Human-review context.
	ProductWorkID int64
	WorkTitle     string
	ExtTitle      string
}

// stratum is the sampling/reporting bucket a ref belongs to.
func (r probableRef) stratum() string {
	switch {
	case r.Corrob == corrobContradict:
		return "contradiction"
	case r.Rule == ruleRosetta:
		return "rosetta"
	case r.AlsoRosetta:
		return "ts-rosetta-corrob" // title-strict work independently anchored in EG
	default:
		return "ts-bangumi-only" // title-strict, no independent cross-source signal
	}
}

func (r probableRef) key() string {
	return strconv.FormatInt(int64(r.SourceID), 10) + ":" + r.ExternalID + ":" + strconv.FormatInt(r.WorkID, 10)
}

func (r probableRef) extURL() string {
	if r.SourceID == sourceEG {
		return egGameBase + r.ExternalID
	}
	return bangumiBase + r.ExternalID
}

func (r probableRef) yearBucket() string { return yearBucket(r.WorkYear, r.ExtYear) }

// loadAll reads every probable work-ref and computes its signals. Idempotent
// and read-only.
func (a *auditor) loadAll() error {
	if a.refs != nil {
		return nil
	}
	// (1) probable work-refs.
	var rows []struct {
		EntityID   int64  `gorm:"column:entity_id"`
		SourceID   int16  `gorm:"column:source_id"`
		ExternalID string `gorm:"column:external_id"`
		MatchedBy  string `gorm:"column:matched_by"`
	}
	if err := a.catalog.Raw(`SELECT entity_id, source_id, external_id, matched_by
		FROM catalog_external_ref WHERE entity_type = ? AND link_kind = ?`,
		model.EntityTypeWork, model.LinkKindProbable).Scan(&rows).Error; err != nil {
		return err
	}

	// (2) work vndb anchors + (3) also-rosetta set + work ids in play.
	workVNDB := map[int64]string{}
	var vrows []struct {
		EntityID   int64  `gorm:"column:entity_id"`
		ExternalID string `gorm:"column:external_id"`
	}
	if err := a.catalog.Raw(`SELECT entity_id, external_id FROM catalog_external_ref
		WHERE entity_type = ? AND source_id = ? AND link_kind = ?`,
		model.EntityTypeWork, sourceVNDB, model.LinkKindExact).Scan(&vrows).Error; err != nil {
		return err
	}
	for _, v := range vrows {
		workVNDB[v.EntityID] = v.ExternalID
	}
	alsoRosetta := map[int64]bool{}
	for _, r := range rows {
		if r.SourceID == sourceEG && r.MatchedBy == ruleRosetta {
			alsoRosetta[r.EntityID] = true
		}
	}

	// (4) catalog_work → product_work_id + display_name.
	type workMeta struct {
		Product int64
		Title   string
	}
	works := map[int64]workMeta{}
	var wrows []struct {
		ID      int64  `gorm:"column:id"`
		Product *int64 `gorm:"column:product_work_id"`
		Name    string `gorm:"column:display_name"`
	}
	if err := a.catalog.Raw(`SELECT id, product_work_id, display_name FROM catalog_work
		WHERE id IN (SELECT DISTINCT entity_id FROM catalog_external_ref WHERE entity_type = ? AND link_kind = ?)`,
		model.EntityTypeWork, model.LinkKindProbable).Scan(&wrows).Error; err != nil {
		return err
	}
	for _, w := range wrows {
		m := workMeta{Title: w.Name}
		if w.Product != nil {
			m.Product = *w.Product
		}
		works[w.ID] = m
	}

	// (5) wiki galgame year (for rosetta's year-diff signal).
	wikiYear := map[int64]int{}
	var grows []struct {
		ID   int64 `gorm:"column:id"`
		Year *int  `gorm:"column:year"`
	}
	if err := a.wiki.Raw(`SELECT id, EXTRACT(YEAR FROM release_date)::int AS year FROM galgame`).
		Scan(&grows).Error; err != nil {
		return err
	}
	for _, g := range grows {
		if g.Year != nil {
			wikiYear[g.ID] = *g.Year
		}
	}

	// (6) EG games: vndb + year + name (rosetta side).
	egVNDB := map[int64]string{}
	egYear := map[int64]int{}
	egName := map[int64]string{}
	if a.eg != nil {
		var erows []struct {
			ID      int64  `gorm:"column:id"`
			VNDB    string `gorm:"column:vndb"`
			Sellday string `gorm:"column:sellday"`
			Name    string `gorm:"column:gamename"`
		}
		if err := a.eg.Raw(`SELECT id, coalesce(vndb,'') AS vndb, coalesce(sellday,'') AS sellday, coalesce(gamename,'') AS gamename FROM games`).
			Scan(&erows).Error; err != nil {
			return err
		}
		for _, e := range erows {
			egVNDB[e.ID] = e.VNDB
			egYear[e.ID] = yearPrefix(e.Sellday)
			egName[e.ID] = e.Name
		}
	}

	// (7) bangumi subjects: name + year + infobox vndb (title-strict side).
	bgmName := map[int64]string{}
	bgmYear := map[int64]int{}
	bgmVNDB := map[int64]string{}
	var srows []struct {
		ID   int64  `gorm:"column:id"`
		Name string `gorm:"column:name"`
		Date string `gorm:"column:date"`
		VNDB string `gorm:"column:vndb"`
	}
	// The infobox vndb link is extracted via a lateral scan of the parsed
	// Fields; bangumi game subjects rarely carry one (a documented finding),
	// so most rows come back with an empty vndb.
	if err := a.catalog.Raw(`
		SELECT s.id, s.name, s.date,
		       coalesce((SELECT f->>'Value' FROM jsonb_array_elements(s.infobox_parsed->'Fields') f
		                 WHERE (f->>'Key') ~* 'vndb' AND jsonb_typeof(f->'Value')='string' LIMIT 1), '') AS vndb
		FROM src_bangumi.subject s
		WHERE s.type = 4 AND jsonb_typeof(s.infobox_parsed->'Fields') = 'array'
		  AND s.id IN (SELECT external_id::bigint FROM catalog_external_ref
		               WHERE entity_type = ? AND source_id = ? AND link_kind = ?)`,
		model.EntityTypeWork, sourceBangumi, model.LinkKindProbable).Scan(&srows).Error; err != nil {
		return err
	}
	for _, s := range srows {
		bgmName[s.ID] = s.Name
		bgmYear[s.ID] = yearPrefix(s.Date)
		bgmVNDB[s.ID] = extractVNDB(s.VNDB)
	}

	// Assemble.
	a.refs = make([]probableRef, 0, len(rows))
	for _, r := range rows {
		pr := probableRef{
			WorkID: r.EntityID, SourceID: r.SourceID, ExternalID: r.ExternalID, Rule: r.MatchedBy,
			WorkVNDB: workVNDB[r.EntityID], AlsoRosetta: alsoRosetta[r.EntityID],
		}
		wm := works[r.EntityID]
		pr.ProductWorkID, pr.WorkTitle = wm.Product, wm.Title
		pr.WorkYear = wikiYear[wm.Product]
		if r.SourceID == sourceEG {
			id, _ := strconv.ParseInt(r.ExternalID, 10, 64)
			pr.ExtVNDB, pr.ExtYear, pr.ExtTitle = egVNDB[id], egYear[id], egName[id]
		} else {
			id, _ := strconv.ParseInt(r.ExternalID, 10, 64)
			pr.ExtVNDB, pr.ExtYear, pr.ExtTitle = bgmVNDB[id], bgmYear[id], bgmName[id]
		}
		pr.Corrob = corrobState(pr.WorkVNDB, pr.ExtVNDB)
		a.refs = append(a.refs, pr)
	}
	return nil
}

// corrobState classifies the vndb agreement between a work and an external entity.
func corrobState(workVNDB, extVNDB string) string {
	wv, ev := normVNDB(workVNDB), normVNDB(extVNDB)
	switch {
	case wv == "":
		return corrobWorkNoVNDB
	case ev == "":
		return corrobExtNoVNDB
	case wv == ev:
		return corrobAgree
	default:
		return corrobContradict
	}
}

// yearBucket buckets |a-b|; a missing year on either side is its own bucket.
func yearBucket(a, b int) string {
	if a == 0 || b == 0 {
		return "missing"
	}
	d := a - b
	if d < 0 {
		d = -d
	}
	switch d {
	case 0:
		return "0"
	case 1:
		return "1"
	default:
		return ">=2"
	}
}

func normVNDB(v string) string { return strings.ToLower(strings.TrimSpace(v)) }

// extractVNDB pulls a "v<digits>" id out of a raw infobox value (a bare id, a
// vndb.org URL, or "r<digits>" release form → empty, we key on vn ids only).
func extractVNDB(raw string) string {
	raw = strings.ToLower(strings.TrimSpace(raw))
	if i := strings.Index(raw, "vndb.org/"); i >= 0 {
		raw = raw[i+len("vndb.org/"):]
	}
	raw = strings.TrimSpace(raw)
	if !strings.HasPrefix(raw, "v") {
		return ""
	}
	digits := raw[1:]
	for i := 0; i < len(digits); i++ {
		if digits[i] < '0' || digits[i] > '9' {
			digits = digits[:i]
			break
		}
	}
	if digits == "" {
		return ""
	}
	return "v" + digits
}

// yearPrefix parses the leading 4-digit year of a date string, or 0.
func yearPrefix(date string) int {
	if len(date) < 4 {
		return 0
	}
	y := 0
	for i := range 4 {
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

// runScan prints the rule × corroboration and rule × year matrices, the
// title-strict cross-source split, and the full contradiction list (TSV).
func (a *auditor) runScan(w io.Writer) error {
	if err := a.loadAll(); err != nil {
		return err
	}

	byRuleCorrob := map[string]map[string]int{}
	byRuleYear := map[string]map[string]int{}
	tsCross := map[bool]int{}
	var contradictions []probableRef
	for _, r := range a.refs {
		ruleShort := ruleLabel(r.Rule)
		add2(byRuleCorrob, ruleShort, r.Corrob)
		add2(byRuleYear, ruleShort, r.yearBucket())
		if r.Rule == ruleTitleYear {
			tsCross[r.AlsoRosetta]++
		}
		if r.Corrob == corrobContradict {
			contradictions = append(contradictions, r)
		}
	}

	fmt.Fprintf(w, "# probable work-refs: %d total\n\n", len(a.refs))
	fmt.Fprintln(w, "## rule × vndb-corroboration")
	printMatrix(w, byRuleCorrob, []string{corrobAgree, corrobContradict, corrobExtNoVNDB, corrobWorkNoVNDB})
	fmt.Fprintln(w, "\n## rule × year-diff (|work − external|)")
	printMatrix(w, byRuleYear, []string{"0", "1", ">=2", "missing"})
	fmt.Fprintf(w, "\n## title-strict cross-source: also has an eg-rosetta ref? yes=%d no=%d\n",
		tsCross[true], tsCross[false])

	fmt.Fprintf(w, "\n## contradiction list (%d rows — vndb disagrees; census these)\n", len(contradictions))
	if len(contradictions) > 0 {
		sort.Slice(contradictions, func(i, j int) bool { return contradictions[i].key() < contradictions[j].key() })
		fmt.Fprintln(w, strings.Join(tsvHeader, "\t"))
		for _, r := range contradictions {
			fmt.Fprintln(w, r.tsvRow())
		}
	}
	return nil
}

func ruleLabel(matchedBy string) string {
	switch matchedBy {
	case ruleRosetta:
		return "rosetta"
	case ruleTitleYear:
		return "title-strict"
	default:
		return matchedBy
	}
}

func add2(m map[string]map[string]int, a, b string) {
	if m[a] == nil {
		m[a] = map[string]int{}
	}
	m[a][b]++
}

func printMatrix(w io.Writer, m map[string]map[string]int, cols []string) {
	fmt.Fprintf(w, "%-14s", "rule")
	for _, c := range cols {
		fmt.Fprintf(w, "\t%12s", c)
	}
	fmt.Fprintln(w)
	rules := make([]string, 0, len(m))
	for k := range m {
		rules = append(rules, k)
	}
	sort.Strings(rules)
	for _, rk := range rules {
		fmt.Fprintf(w, "%-14s", rk)
		for _, c := range cols {
			fmt.Fprintf(w, "\t%12d", m[rk][c])
		}
		fmt.Fprintln(w)
	}
}
