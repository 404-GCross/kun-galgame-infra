// Package workaliases backfills BODYLESS works' alias titles (step 95,
// refs/proj/95):
//
//   - bgm lane: bodyless works with an EXACT bgm work anchor whose subject
//     infobox carries a 别名 field → catalog_work_title kind=alias, lang=”
//     (zh/ja mixed, no reliable per-string language). The wiki-parser field
//     shape: Array=true → strings in Items[].Value, else in Value.
//   - dlsite-kana lane: bodyless galgame works with a dlsite RELEASE anchor
//     but NO kind=search_hint row (the B1-minted tail the step-14/55 imports
//     predate) → mirror work_name_kana as kind=search_hint, lang='ja' (the
//     existing 156,918-row convention).
//
// CLAIMED works are excluded on principle: their alias surface lives on the
// wiki face (galgame_alias, vndb-synced + step-10 bgm append) — the vndb lane
// is closed entirely as negative knowledge (62,220/62,222 vndb anchors sit on
// claimed works; the bodyless remainder is 2).
//
// Aliases are static facts — writes are ON CONFLICT DO NOTHING (no refresh
// semantics needed); an alias whose (work_id, title) already exists under ANY
// kind is skipped (a duplicate of the official/search-hint row is noise).
package workaliases

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"api/internal/infrastructure/database"
	"api/internal/platform/catalog/repository"

	"gorm.io/gorm"
)

// maxAliasLen rejects degenerate strings (surveyed: 0 over 500 in the supply).
const maxAliasLen = 500

// Opts configures a run. Source selects the lane: "bgm" | "dlsite-kana" | "all".
type Opts struct {
	Apply     bool
	DSN       string // catalog DB (hosts src_bangumi) — REQUIRED
	DlsiteDSN string // dlsite mirror — REQUIRED for the dlsite-kana/all lanes
	Source    string
}

// Stats reports a run. Planned counters are identical in dry and apply.
type Stats struct {
	BgmWorks      int // bodyless works whose subject carries >=1 usable alias
	BgmPlanned    int
	BgmSkippedDup int // (work_id, title) already present under any kind
	BgmWritten    int
	BgmConflict   int // lost the unique-key race (identical row already there)

	KanaWorks   int // candidate works lacking a kind=3 row
	KanaNoKana  int // mirror row has no work_name_kana
	KanaPlanned int
	KanaWritten int

	Errors int
}

// Run executes the backfill.
func Run(ctx context.Context, opts Opts) (*Stats, error) {
	if opts.DSN == "" {
		return nil, fmt.Errorf("catalog DSN is required (--dsn)")
	}
	if opts.Source == "" {
		opts.Source = "all"
	}
	if (opts.Source == "dlsite-kana" || opts.Source == "all") && opts.DlsiteDSN == "" {
		return nil, fmt.Errorf("dlsite mirror DSN is required for the dlsite-kana lane (--dlsite-dsn)")
	}
	db, err := openGorm(opts.DSN)
	if err != nil {
		return nil, fmt.Errorf("connect catalog db: %w", err)
	}
	defer closeGorm(db)

	st := &Stats{}
	if opts.Source == "bgm" || opts.Source == "all" {
		if err := runBgm(ctx, db, opts, st); err != nil {
			return nil, err
		}
	}
	if opts.Source == "dlsite-kana" || opts.Source == "all" {
		if err := runKana(ctx, db, opts, st); err != nil {
			return nil, err
		}
	}
	slog.Info("workaliases done", "apply", opts.Apply,
		"bgm_works", st.BgmWorks, "bgm_planned", st.BgmPlanned, "bgm_skipped_dup", st.BgmSkippedDup,
		"bgm_written", st.BgmWritten, "bgm_conflict", st.BgmConflict,
		"kana_works", st.KanaWorks, "kana_no_kana", st.KanaNoKana,
		"kana_planned", st.KanaPlanned, "kana_written", st.KanaWritten, "errors", st.Errors)
	return st, nil
}

// infobox mirrors the wiki-parser output shape (only what this lane reads).
type infobox struct {
	Fields []struct {
		Key   string `json:"Key"`
		Array bool   `json:"Array"`
		Value string `json:"Value"`
		Items []struct {
			Value string `json:"Value"`
		} `json:"Items"`
	} `json:"Fields"`
}

// aliasesFrom extracts the trimmed, deduplicated 别名 strings of one subject.
func aliasesFrom(raw []byte) []string {
	if len(raw) == 0 {
		return nil
	}
	var ib infobox
	if err := json.Unmarshal(raw, &ib); err != nil {
		return nil // malformed rows are skipped, never fatal
	}
	seen := map[string]struct{}{}
	var out []string
	add := func(s string) {
		s = strings.TrimSpace(s)
		if s == "" || len(s) > maxAliasLen {
			return
		}
		if _, dup := seen[s]; dup {
			return
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	for _, f := range ib.Fields {
		if f.Key != "别名" {
			continue
		}
		if f.Array {
			for _, it := range f.Items {
				add(it.Value)
			}
			continue
		}
		add(f.Value)
	}
	return out
}

// runBgm: bodyless works × exact bgm anchors × infobox 别名 → kind=1 rows.
func runBgm(ctx context.Context, db *gorm.DB, opts Opts, st *Stats) error {
	var rows []struct {
		WorkID  int64  `gorm:"column:work_id"`
		Infobox []byte `gorm:"column:infobox_parsed"`
	}
	if err := db.WithContext(ctx).Raw(`
		SELECT DISTINCT ON (w.id) w.id AS work_id, s.infobox_parsed
		FROM catalog_work w
		JOIN catalog_external_ref r ON r.entity_type = 5 AND r.entity_id = w.id
			AND r.source_id = 3 AND r.link_kind = 0
		JOIN src_bangumi.subject s ON s.id = r.external_id::bigint
		WHERE coalesce(w.site,'') = '' AND w.deleted_at IS NULL
		  AND s.infobox_parsed IS NOT NULL
		ORDER BY w.id, r.external_id`).Scan(&rows).Error; err != nil {
		return fmt.Errorf("load bgm candidates: %w", err)
	}

	type cand struct {
		workID int64
		alias  string
	}
	var cands []cand
	workIDs := map[int64]struct{}{}
	for _, r := range rows {
		aliases := aliasesFrom(r.Infobox)
		if len(aliases) == 0 {
			continue
		}
		st.BgmWorks++
		workIDs[r.WorkID] = struct{}{}
		for _, a := range aliases {
			cands = append(cands, cand{r.WorkID, a})
		}
	}

	// Existing (work_id, title) pairs under ANY kind — the dedup set.
	existing := map[string]struct{}{}
	ids := make([]int64, 0, len(workIDs))
	for id := range workIDs {
		ids = append(ids, id)
	}
	for _, chunk := range chunkInt64(ids, 10000) {
		var t []struct {
			WorkID int64  `gorm:"column:work_id"`
			Title  string `gorm:"column:title"`
		}
		if err := db.WithContext(ctx).Raw(`SELECT work_id, title FROM catalog_work_title
			WHERE work_id IN ?`, chunk).Scan(&t).Error; err != nil {
			return fmt.Errorf("load existing titles: %w", err)
		}
		for _, r := range t {
			existing[titleKey(r.WorkID, r.Title)] = struct{}{}
		}
	}

	// touched collects works that actually gained a title row, so the lane can
	// bump their catalog_work.updated_at once and put them on the public changes
	// feed. Dups, conflicts and dry-runs contribute nothing.
	var touched []int64
	for _, c := range cands {
		if _, dup := existing[titleKey(c.workID, c.alias)]; dup {
			st.BgmSkippedDup++
			continue
		}
		st.BgmPlanned++
		if !opts.Apply {
			continue
		}
		res := db.WithContext(ctx).Exec(`INSERT INTO catalog_work_title (work_id, lang, title, kind)
			VALUES (?, '', ?, 1) ON CONFLICT DO NOTHING`, c.workID, c.alias)
		if res.Error != nil {
			st.Errors++
			slog.Warn("alias insert", "work", c.workID, "err", res.Error)
			continue
		}
		if res.RowsAffected == 1 {
			st.BgmWritten++
			touched = append(touched, c.workID)
		} else {
			st.BgmConflict++
		}
	}
	return repository.TouchWorks(ctx, db, touched)
}

// runKana: anchored bodyless galgame works lacking a kind=3 row × mirror
// work_name_kana → kind=3 rows (the step-14/55 convention).
func runKana(ctx context.Context, db *gorm.DB, opts Opts, st *Stats) error {
	var rows []struct {
		WorkID int64  `gorm:"column:work_id"`
		Workno string `gorm:"column:workno"`
	}
	if err := db.WithContext(ctx).Raw(`
		SELECT DISTINCT ON (rel.work_id) rel.work_id, r.external_id AS workno
		FROM catalog_external_ref r
		JOIN catalog_release rel ON rel.id = r.entity_id
		JOIN catalog_work w ON w.id = rel.work_id AND coalesce(w.site,'') = '' AND w.deleted_at IS NULL
		JOIN catalog_medium m ON m.id = w.medium_id AND m.key = 'galgame'
		WHERE r.entity_type = 6 AND r.source_id = 4 AND r.link_kind = 0
		  AND NOT EXISTS (SELECT 1 FROM catalog_work_title t WHERE t.work_id = rel.work_id AND t.kind = 3)
		ORDER BY rel.work_id, r.external_id`).Scan(&rows).Error; err != nil {
		return fmt.Errorf("load kana candidates: %w", err)
	}
	st.KanaWorks = len(rows)
	if len(rows) == 0 {
		return nil
	}

	dl, err := openGorm(opts.DlsiteDSN)
	if err != nil {
		return fmt.Errorf("connect dlsite mirror: %w", err)
	}
	defer closeGorm(dl)

	worknos := make([]string, 0, len(rows))
	for _, r := range rows {
		worknos = append(worknos, r.Workno)
	}
	kana := map[string]string{}
	for _, chunk := range chunkStr(worknos, 10000) {
		var t []struct {
			Workno string `gorm:"column:workno"`
			Kana   string `gorm:"column:kana"`
		}
		if err := dl.WithContext(ctx).Raw(`SELECT workno,
			coalesce(product_json->>'work_name_kana', info_json->>'work_name_kana', '') AS kana
			FROM works WHERE workno IN ?`, chunk).Scan(&t).Error; err != nil {
			return fmt.Errorf("load mirror kana: %w", err)
		}
		for _, r := range t {
			if k := strings.TrimSpace(r.Kana); k != "" && len(k) <= maxAliasLen {
				kana[r.Workno] = k
			}
		}
	}

	var touched []int64 // works that really gained a kana title (see runBgm)
	for _, r := range rows {
		k, ok := kana[r.Workno]
		if !ok {
			st.KanaNoKana++
			continue
		}
		st.KanaPlanned++
		if !opts.Apply {
			continue
		}
		res := db.WithContext(ctx).Exec(`INSERT INTO catalog_work_title (work_id, lang, title, kind)
			VALUES (?, 'ja', ?, 3) ON CONFLICT DO NOTHING`, r.WorkID, k)
		if res.Error != nil {
			st.Errors++
			slog.Warn("kana insert", "work", r.WorkID, "err", res.Error)
			continue
		}
		if res.RowsAffected == 1 {
			st.KanaWritten++
			touched = append(touched, r.WorkID)
		}
	}
	return repository.TouchWorks(ctx, db, touched)
}

func titleKey(workID int64, title string) string {
	return fmt.Sprintf("%d\x00%s", workID, title)
}

func chunkInt64(in []int64, size int) [][]int64 {
	var out [][]int64
	for len(in) > size {
		out = append(out, in[:size])
		in = in[size:]
	}
	if len(in) > 0 {
		out = append(out, in)
	}
	return out
}

func chunkStr(in []string, size int) [][]string {
	var out [][]string
	for len(in) > size {
		out = append(out, in[:size])
		in = in[size:]
	}
	if len(in) > 0 {
		out = append(out, in)
	}
	return out
}

func openGorm(dsn string) (*gorm.DB, error) {
	return database.OpenJob(dsn)
}

func closeGorm(db *gorm.DB) {
	if sqlDB, err := db.DB(); err == nil {
		sqlDB.Close()
	}
}
