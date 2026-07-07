package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"gorm.io/gorm"
)

const (
	sourceBangumi int16 = 3
	entityWork    int16 = 5
	ruleWikiBID         = "rule:wiki-bid-typed"

	wikiGalgameBase = "https://www.kungal.com/galgame/" // wiki galgame id → public page
	bangumiBase     = "https://bgm.tv/subject/"
)

var tsvHeader = []string{
	"verdict_id", "galgame_id", "work_id", "bid", "layer", "verdict", "confidence",
	"reason", "wiki_names", "subject_names", "wiki_url", "bangumi_url", "decision", "notes",
}

// verdict is one bid-identity audit row. Explicit column tags where GORM's
// snake_case would mangle an all-caps run (BID → b_i_d).
type verdict struct {
	ID           int64
	WorkID       int64
	GalgameID    int64
	BID          int64 `gorm:"column:bid"`
	Layer        string
	Verdict      string
	Confidence   float64
	Reason       string
	WikiNames    string
	SubjectNames string
	Resolution   string
}

// subject is the enrichment source for a bid, from src_bangumi.subject.
type subject struct {
	ID            int64
	Name          string
	NameCN        string `gorm:"column:name_cn"`
	Summary       string
	InfoboxParsed []byte `gorm:"column:infobox_parsed"`
}

// --- export ---------------------------------------------------------------

func runExport(catalogDB *gorm.DB, out string, all, includeCleared bool) error {
	// Default set = the LLM-FLAGGED backlog (different/unsure + boundary). The
	// LLM-CLEARED-but-deterministic-flagged class (layer=suspect, verdict=same)
	// is opt-in: the step-12 canary (atled #2308 carrying CLANNAD's bid=13) is
	// a suspect+same — the LLM was fooled by smeared aliases into clearing it —
	// so it lives HERE, not in the different worklist. Reviewing this ~782-row
	// class is the deeper audit pass that catches LLM false-negatives.
	inner := "verdict IN ('different','unsure') OR layer = 'boundary'"
	if includeCleared {
		inner += " OR (layer = 'suspect' AND verdict = 'same')"
	}
	where := "(" + inner + ")"
	if !all {
		where += " AND resolution = ''"
	}
	var rows []verdict
	if err := catalogDB.Raw(`SELECT id, work_id, galgame_id, bid, layer, verdict, confidence, reason, wiki_names, subject_names, resolution
		FROM src_llm.bid_identity_verdict WHERE ` + where + `
		ORDER BY CASE verdict WHEN 'different' THEN 0 WHEN 'unsure' THEN 1 ELSE 2 END, confidence DESC, id`).Scan(&rows).Error; err != nil {
		return err
	}
	w, closeFn, err := openOut(out)
	if err != nil {
		return err
	}
	defer closeFn()
	bw := bufio.NewWriter(w)
	defer bw.Flush()
	fmt.Fprintln(bw, strings.Join(tsvHeader, "\t"))
	for _, r := range rows {
		fmt.Fprintln(bw, strings.Join([]string{
			itoa(r.ID), itoa(r.GalgameID), itoa(r.WorkID), itoa(r.BID), r.Layer, r.Verdict,
			strconv.FormatFloat(r.Confidence, 'f', 2, 64), clean(r.Reason), clean(r.WikiNames), clean(r.SubjectNames),
			wikiGalgameBase + itoa(r.GalgameID), bangumiBase + itoa(r.BID), "", "",
		}, "\t"))
	}
	fmt.Fprintf(os.Stderr, "exported %d worklist rows\n", len(rows))
	return nil
}

func runExportSmears(catalogDB *gorm.DB, out string) error {
	var rows []verdict
	if err := catalogDB.Raw(`SELECT id, work_id, galgame_id, bid, wiki_names, subject_names, resolution_note AS reason
		FROM src_llm.bid_identity_verdict WHERE resolution = 'smear' ORDER BY id`).Scan(&rows).Error; err != nil {
		return err
	}
	w, closeFn, err := openOut(out)
	if err != nil {
		return err
	}
	defer closeFn()
	bw := bufio.NewWriter(w)
	defer bw.Flush()
	fmt.Fprintln(bw, "galgame_id\twork_id\tbid\twiki_names\tsubject_names\tnote")
	for _, r := range rows {
		fmt.Fprintln(bw, strings.Join([]string{itoa(r.GalgameID), itoa(r.WorkID), itoa(r.BID), clean(r.WikiNames), clean(r.SubjectNames), clean(r.Reason)}, "\t"))
	}
	fmt.Fprintf(os.Stderr, "exported %d smear rows\n", len(rows))
	return nil
}

// --- apply ----------------------------------------------------------------

// Stats is the receipt tally.
type Stats struct {
	OK, Wrong, WrongRebid, Smear, Skipped, Already, Errors                                    int
	FieldsReverted, FieldsSkipped, AliasesRemoved, RefsRevoked, RejectionsWritten, ReEnriched int
}

func (s Stats) log(run bool) {
	mode := "DRY"
	if run {
		mode = "APPLIED"
	}
	fmt.Printf("[%s] ok=%d wrong=%d wrong:rebid=%d smear=%d skipped(no-decision)=%d already-resolved=%d errors=%d | fields_reverted=%d fields_skipped(hand-edited)=%d aliases_removed=%d refs_revoked=%d rejections=%d re_enriched=%d\n",
		mode, s.OK, s.Wrong, s.WrongRebid, s.Smear, s.Skipped, s.Already, s.Errors,
		s.FieldsReverted, s.FieldsSkipped, s.AliasesRemoved, s.RefsRevoked, s.RejectionsWritten, s.ReEnriched)
}

type receiptRow struct {
	verdictID int64
	decision  string
	notes     string
}

func runApply(catalogDB, wikiDB *gorm.DB, file string, run bool) (Stats, error) {
	var st Stats
	rows, err := parseReceipt(file)
	if err != nil {
		return st, err
	}
	for _, rr := range rows {
		if rr.decision == "" {
			st.Skipped++
			continue
		}
		var v verdict
		if err := catalogDB.Raw(`SELECT id, work_id, galgame_id, bid, resolution FROM src_llm.bid_identity_verdict WHERE id = ?`, rr.verdictID).Scan(&v).Error; err != nil {
			return st, err
		}
		if v.ID == 0 {
			fmt.Printf("  verdict %d not found — skipped\n", rr.verdictID)
			st.Errors++
			continue
		}
		if v.Resolution != "" {
			st.Already++ // idempotency: a resolved row is never re-run
			continue
		}
		if err := applyOne(catalogDB, wikiDB, v, rr, run, &st); err != nil {
			fmt.Printf("  verdict %d (%s): ERROR %v\n", v.ID, rr.decision, err)
			st.Errors++
			continue
		}
	}
	return st, nil
}

func applyOne(catalogDB, wikiDB *gorm.DB, v verdict, rr receiptRow, run bool, st *Stats) error {
	kind, newBID, err := parseDecision(rr.decision)
	if err != nil {
		return err
	}
	switch kind {
	case "ok":
		fmt.Printf("  verdict %d work=%d bid=%d → ok (LLM false positive; zero writes)\n", v.ID, v.WorkID, v.BID)
		st.OK++
	case "smear":
		fmt.Printf("  verdict %d work=%d bid=%d → smear (identity ok, aliases smeared; queued for --export-smears)\n", v.ID, v.WorkID, v.BID)
		st.Smear++
	case "wrong", "wrong-rebid":
		if err := rollbackWrong(catalogDB, wikiDB, v, run, st); err != nil {
			return err
		}
		if kind == "wrong-rebid" {
			if err := reAttach(catalogDB, wikiDB, v, newBID, run, st); err != nil {
				return err
			}
			st.WrongRebid++
		} else {
			st.Wrong++
		}
	}
	return recordResolution(catalogDB, v.ID, rr.decision, rr.notes, run)
}

// rollbackWrong clears the wrong bid, safely reverts enrichment-filled columns,
// removes appended aliases, revokes the catalog bangumi ref and records the
// rejection (negative knowledge).
func rollbackWrong(catalogDB, wikiDB *gorm.DB, v verdict, run bool, st *Stats) error {
	subj, err := loadSubject(catalogDB, v.BID)
	if err != nil {
		return err
	}
	// wiki galgame current values.
	var g struct {
		NameZhCN  string `gorm:"column:name_zh_cn"`
		IntroZhCN string `gorm:"column:intro_zh_cn"`
	}
	if err := wikiDB.Raw(`SELECT coalesce(name_zh_cn,'') AS name_zh_cn, coalesce(intro_zh_cn,'') AS intro_zh_cn FROM galgame WHERE id = ?`, v.GalgameID).Scan(&g).Error; err != nil {
		return err
	}

	// Decide field-by-field under the safety condition (current == what
	// enrichment wrote; a hand-edit is never clobbered). Fill-empty means the
	// pre-enrichment value was empty, so a safe revert restores ''.
	type revert struct{ col, from string }
	var reverts []revert
	if subj.NameCN != "" && g.NameZhCN == subj.NameCN {
		reverts = append(reverts, revert{"name_zh_cn", g.NameZhCN})
	} else if g.NameZhCN != "" {
		fmt.Printf("    name_zh_cn: hand-edited (%q ≠ subject %q) — SKIPPED\n", g.NameZhCN, subj.NameCN)
		st.FieldsSkipped++
	}
	if strings.TrimSpace(subj.Summary) != "" && g.IntroZhCN == subj.Summary {
		reverts = append(reverts, revert{"intro_zh_cn", ""})
	} else if g.IntroZhCN != "" {
		fmt.Printf("    intro_zh_cn: hand-edited — SKIPPED\n")
		st.FieldsSkipped++
	}
	aliases := aliasCandidates(subj)

	fmt.Printf("  verdict %d work=%d bid=%d → wrong: clear galgame.bid, delete meta, revert %d field(s), remove ≤%d alias(es), revoke ref + reject\n",
		v.ID, v.WorkID, v.BID, len(reverts), len(aliases))
	for _, r := range reverts {
		fmt.Printf("    revert %s → ''\n", r.col)
	}
	st.FieldsReverted += len(reverts)

	if run {
		if err := wikiDB.Transaction(func(tx *gorm.DB) error {
			if err := tx.Exec(`UPDATE galgame SET bid = NULL WHERE id = ?`, v.GalgameID).Error; err != nil {
				return err
			}
			if err := tx.Exec(`DELETE FROM galgame_bangumi_meta WHERE galgame_id = ?`, v.GalgameID).Error; err != nil {
				return err
			}
			for _, r := range reverts {
				if err := revertScalar(tx, v.GalgameID, r.col); err != nil {
					return err
				}
			}
			removed, err := removeAliases(tx, v.GalgameID, aliases)
			if err != nil {
				return err
			}
			st.AliasesRemoved += removed
			return nil
		}); err != nil {
			return err
		}
	} else {
		st.AliasesRemoved += len(aliases) // dry upper bound
	}

	return revokeRef(catalogDB, v.WorkID, v.BID, "human worklist: wrong bid (step 20)", run, st)
}

// reAttach sets the corrected bid, fills the (now-empty) enriched columns from
// the new subject, and adds the new exact bangumi ref (only if type=4).
func reAttach(catalogDB, wikiDB *gorm.DB, v verdict, newBID int64, run bool, st *Stats) error {
	subj, err := loadSubject(catalogDB, newBID)
	if err != nil {
		return err
	}
	if subj.ID == 0 {
		return fmt.Errorf("corrected bid %d not in src_bangumi.subject", newBID)
	}
	type4, err := isType4(catalogDB, newBID)
	if err != nil {
		return err
	}
	fmt.Printf("    re-attach bid=%d (type4=%v): set galgame.bid, fill-empty enrich, %s\n", newBID, type4,
		map[bool]string{true: "add exact ref", false: "no ref (not a game)"}[type4])
	if !run {
		st.ReEnriched++
		return nil
	}
	if err := wikiDB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec(`UPDATE galgame SET bid = ? WHERE id = ?`, newBID, v.GalgameID).Error; err != nil {
			return err
		}
		return fillEmptyEnrich(tx, v.GalgameID, subj)
	}); err != nil {
		return err
	}
	st.ReEnriched++
	if type4 {
		if err := catalogDB.Exec(`INSERT INTO catalog_external_ref (entity_type, entity_id, source_id, external_id, link_kind, matched_by, created_at)
			VALUES (?, ?, ?, ?, 0, ?, now()) ON CONFLICT DO NOTHING`,
			entityWork, v.WorkID, sourceBangumi, itoa(newBID), ruleWikiBID).Error; err != nil {
			return err
		}
	}
	return nil
}

// --- lower-level helpers ---------------------------------------------------

func revertScalar(tx *gorm.DB, galgameID int64, col string) error {
	if err := tx.Exec(`UPDATE galgame SET `+col+` = '' WHERE id = ?`, galgameID).Error; err != nil {
		return err
	}
	// Keep Approach-B's invariant (live == latest snapshot).
	return tx.Exec(`UPDATE galgame_revision
		SET snapshot = jsonb_set(snapshot, ?::text[], to_jsonb(''::text), true)
		WHERE galgame_id = ? AND revision = (SELECT max(revision) FROM galgame_revision WHERE galgame_id = ?)`,
		"{"+col+"}", galgameID, galgameID).Error
}

// removeAliases deletes the enrichment-appended aliases still present, then
// rebuilds the latest snapshot's aliases array from the remaining rows.
func removeAliases(tx *gorm.DB, galgameID int64, candidates []string) (int, error) {
	removed := 0
	for _, name := range candidates {
		res := tx.Exec(`DELETE FROM galgame_alias WHERE galgame_id = ? AND name = ?`, galgameID, name)
		if res.Error != nil {
			return removed, res.Error
		}
		removed += int(res.RowsAffected)
	}
	if removed == 0 {
		return 0, nil
	}
	return removed, tx.Exec(`UPDATE galgame_revision
		SET snapshot = jsonb_set(snapshot, '{aliases}',
		        COALESCE((SELECT jsonb_agg(name ORDER BY name) FROM galgame_alias WHERE galgame_id = ?), '[]'::jsonb), true)
		WHERE galgame_id = ? AND revision = (SELECT max(revision) FROM galgame_revision WHERE galgame_id = ?)`,
		galgameID, galgameID, galgameID).Error
}

// fillEmptyEnrich mirrors bangumienrich's fill-only-when-empty for one game
// (used by the wrong:<bid> re-attach). Snapshot patched to stay in sync.
func fillEmptyEnrich(tx *gorm.DB, galgameID int64, subj subject) error {
	fill := func(col, val string) error {
		if strings.TrimSpace(val) == "" {
			return nil
		}
		var cur string
		if err := tx.Raw(`SELECT coalesce(`+col+`,'') FROM galgame WHERE id = ?`, galgameID).Scan(&cur).Error; err != nil {
			return err
		}
		if cur != "" {
			return nil // fill-empty only
		}
		if err := tx.Exec(`UPDATE galgame SET `+col+` = ? WHERE id = ?`, val, galgameID).Error; err != nil {
			return err
		}
		return tx.Exec(`UPDATE galgame_revision SET snapshot = jsonb_set(snapshot, ?::text[], to_jsonb(?::text), true)
			WHERE galgame_id = ? AND revision = (SELECT max(revision) FROM galgame_revision WHERE galgame_id = ?)`,
			"{"+col+"}", val, galgameID, galgameID).Error
	}
	if err := fill("name_zh_cn", subj.NameCN); err != nil {
		return err
	}
	if err := fill("intro_zh_cn", subj.Summary); err != nil {
		return err
	}
	for _, name := range aliasCandidates(subj) {
		if err := tx.Exec(`INSERT INTO galgame_alias (galgame_id, name, created, updated) VALUES (?, ?, now(), now()) ON CONFLICT (galgame_id, name) DO NOTHING`, galgameID, name).Error; err != nil {
			return err
		}
	}
	return nil
}

// revokeRef mirrors AdminQueueService.RejectRef's contract (delete the exact
// ref + record catalog_match_rejection) but tolerates a missing ref (still
// records the negative knowledge).
func revokeRef(catalogDB *gorm.DB, workID, bid int64, reason string, run bool, st *Stats) error {
	if !run {
		st.RefsRevoked++
		st.RejectionsWritten++
		return nil
	}
	return catalogDB.Transaction(func(tx *gorm.DB) error {
		res := tx.Exec(`DELETE FROM catalog_external_ref WHERE entity_type = ? AND entity_id = ? AND source_id = ? AND external_id = ?`,
			entityWork, workID, sourceBangumi, itoa(bid))
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected > 0 {
			st.RefsRevoked++
		}
		if err := tx.Exec(`INSERT INTO catalog_match_rejection (entity_type, entity_id, source_id, external_id, reason, rejected_at)
			VALUES (?, ?, ?, ?, ?, now()) ON CONFLICT DO NOTHING`, entityWork, workID, sourceBangumi, itoa(bid), reason).Error; err != nil {
			return err
		}
		st.RejectionsWritten++
		return nil
	})
}

func recordResolution(catalogDB *gorm.DB, verdictID int64, decision, notes string, run bool) error {
	if !run {
		return nil
	}
	return catalogDB.Exec(`UPDATE src_llm.bid_identity_verdict SET resolution = ?, resolution_note = ?, resolved_at = now() WHERE id = ?`,
		decision, notes, verdictID).Error
}

func loadSubject(catalogDB *gorm.DB, bid int64) (subject, error) {
	var s subject
	err := catalogDB.Raw(`SELECT id, name, coalesce(name_cn,'') AS name_cn, coalesce(summary,'') AS summary, infobox_parsed
		FROM src_bangumi.subject WHERE id = ?`, bid).Scan(&s).Error
	return s, err
}

func isType4(catalogDB *gorm.DB, bid int64) (bool, error) {
	var typ int
	if err := catalogDB.Raw(`SELECT type FROM src_bangumi.subject WHERE id = ?`, bid).Scan(&typ).Error; err != nil {
		return false, err
	}
	return typ == 4, nil
}

func aliasCandidates(s subject) []string {
	var out []string
	if len(s.InfoboxParsed) > 0 {
		var box struct {
			Fields []struct {
				Key   string `json:"Key"`
				Items []struct {
					Value string `json:"Value"`
				} `json:"Items"`
			} `json:"Fields"`
		}
		if json.Unmarshal(s.InfoboxParsed, &box) == nil {
			for _, f := range box.Fields {
				if f.Key != "别名" {
					continue
				}
				for _, item := range f.Items {
					if v := strings.TrimSpace(item.Value); v != "" {
						out = append(out, v)
					}
				}
			}
		}
	}
	if s.NameCN != "" && s.NameCN != s.Name {
		out = append(out, s.NameCN)
	}
	return out
}

func parseDecision(d string) (kind string, newBID int64, err error) {
	d = strings.TrimSpace(d)
	switch {
	case d == "ok":
		return "ok", 0, nil
	case d == "wrong":
		return "wrong", 0, nil
	case d == "smear":
		return "smear", 0, nil
	case strings.HasPrefix(d, "wrong:"):
		n, e := strconv.ParseInt(strings.TrimPrefix(d, "wrong:"), 10, 64)
		if e != nil {
			return "", 0, fmt.Errorf("bad corrected bid in %q", d)
		}
		return "wrong-rebid", n, nil
	}
	return "", 0, fmt.Errorf("unknown decision %q (want ok / wrong / wrong:<bid> / smear)", d)
}

func parseReceipt(file string) ([]receiptRow, error) {
	f, err := os.Open(file)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	var header []string
	var out []receiptRow
	for sc.Scan() {
		line := sc.Text()
		cols := strings.Split(line, "\t")
		if header == nil {
			header = cols
			continue
		}
		idx := func(name string) int {
			for i, h := range header {
				if h == name {
					return i
				}
			}
			return -1
		}
		vi, di, ni := idx("verdict_id"), idx("decision"), idx("notes")
		if vi < 0 || di < 0 || vi >= len(cols) {
			continue
		}
		id, err := strconv.ParseInt(strings.TrimSpace(cols[vi]), 10, 64)
		if err != nil {
			continue
		}
		notes := ""
		if ni >= 0 && ni < len(cols) {
			notes = strings.TrimSpace(cols[ni])
		}
		dec := ""
		if di < len(cols) {
			dec = strings.TrimSpace(cols[di])
		}
		out = append(out, receiptRow{verdictID: id, decision: dec, notes: notes})
	}
	return out, sc.Err()
}

func openOut(out string) (io.Writer, func(), error) {
	if out == "" {
		return os.Stdout, func() {}, nil
	}
	f, err := os.Create(out)
	if err != nil {
		return nil, nil, err
	}
	return f, func() { f.Close() }, nil
}

func itoa(v int64) string { return strconv.FormatInt(v, 10) }

// clean strips tabs/newlines so a value stays inside one TSV cell.
func clean(s string) string {
	return strings.NewReplacer("\t", " ", "\n", " ", "\r", " ").Replace(s)
}
