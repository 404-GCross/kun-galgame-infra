// apply-series-name-overrides loads reviewed display names for the derived
// series lane (wave 185) into catalog_series_name_override, where the builder
// consults them (see internal/jobs/derivedseries: an override wins only while
// its member-hash still matches, so this tool snapshots the CURRENT membership
// as the hash at load time).
//
// It writes the override table ONLY. Nothing a reader sees changes until the
// next build-derived-series --apply pass — the builder stays the lane's single
// writer.
//
// Input is jsonl, one {"external_id": "comp:123", "display_name": "..."} per
// line. Every row is validated before it counts:
//
//   - the series must exist in the derived lane right now;
//   - the name must be non-empty and, NFKC-case-folded, a substring of at least
//     one CURRENT member title. The reviewer (an LLM wave) is allowed to pick
//     and trim a name, never to invent one — this check is what makes that a
//     property of the table rather than a hope about the prompt.
//
// Dry-run is the DEFAULT; --limit N caps how many rows apply writes (the
// rehearsal discipline: first run --apply --limit 20, eyeball, then the rest).
//
//	go run ./cmd/apply-series-name-overrides --input names.jsonl \
//	    --reviewed-by "wave185-fable" \
//	    --dsn "host=localhost port=5432 user=postgres dbname=kun_catalog sslmode=disable"
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"api/internal/jobs/derivedseries"

	"golang.org/x/text/unicode/norm"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

type inputRow struct {
	ExternalID  string `json:"external_id"`
	DisplayName string `json:"display_name"`
}

func main() {
	apply := flag.Bool("apply", false, "write overrides (default: dry run)")
	dsn := flag.String("dsn", "", "catalog DSN — REQUIRED")
	input := flag.String("input", "", "jsonl of {external_id, display_name} — REQUIRED")
	reviewedBy := flag.String("reviewed-by", "", "provenance tag stored on each row — REQUIRED")
	limit := flag.Int("limit", 0, "apply at most N rows (0 = no cap); the rehearsal knob")
	flag.Parse()
	if *dsn == "" || *input == "" || *reviewedBy == "" {
		slog.Error("--dsn, --input and --reviewed-by are all required")
		os.Exit(2)
	}

	db, err := gorm.Open(postgres.Open(*dsn), &gorm.Config{Logger: gormlogger.Default.LogMode(gormlogger.Silent)})
	if err != nil {
		slog.Error("connect catalog db", "error", err)
		os.Exit(1)
	}
	ctx := context.Background()

	var srcID int16
	if err := db.WithContext(ctx).Raw(`SELECT id FROM catalog_source WHERE key = 'derived'`).
		Scan(&srcID).Error; err != nil || srcID == 0 {
		slog.Error("catalog_source has no derived row", "error", err)
		os.Exit(1)
	}

	f, err := os.Open(*input)
	if err != nil {
		slog.Error("open input", "error", err)
		os.Exit(1)
	}
	defer f.Close()

	var written, skippedMissing, skippedUnverified, skippedDup, malformed int
	seen := map[string]bool{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var row inputRow
		if err := json.Unmarshal([]byte(line), &row); err != nil ||
			row.ExternalID == "" || strings.TrimSpace(row.DisplayName) == "" {
			malformed++
			continue
		}
		if seen[row.ExternalID] {
			skippedDup++
			continue
		}
		seen[row.ExternalID] = true
		name := strings.TrimSpace(row.DisplayName)

		// Current membership — both the hash snapshot and the honesty check
		// read the same rows, so the override is verified against exactly the
		// state it will be keyed to.
		var members []struct {
			WorkID int64  `gorm:"column:work_id"`
			Title  string `gorm:"column:display_name"`
		}
		if err := db.WithContext(ctx).Raw(`
			SELECT m.work_id, w.display_name
			FROM catalog_series_member m
			JOIN catalog_series s ON s.id = m.series_id
			JOIN catalog_work w ON w.id = m.work_id
			WHERE s.source_id = ? AND s.external_id = ?`, srcID, row.ExternalID).
			Scan(&members).Error; err != nil {
			slog.Error("load members", "external_id", row.ExternalID, "error", err)
			os.Exit(1)
		}
		if len(members) == 0 {
			skippedMissing++
			continue
		}
		folded := func(s string) string { return strings.ToLower(norm.NFKC.String(s)) }
		verified := false
		ids := make([]int64, 0, len(members))
		for _, m := range members {
			ids = append(ids, m.WorkID)
			if strings.Contains(folded(m.Title), folded(name)) {
				verified = true
			}
		}
		if !verified {
			skippedUnverified++
			slog.Warn("name not extracted from any member title — refused",
				"external_id", row.ExternalID, "name", name)
			continue
		}

		if *limit > 0 && written >= *limit {
			break
		}
		written++
		if !*apply {
			continue
		}
		if err := db.WithContext(ctx).Exec(`
			INSERT INTO catalog_series_name_override
			    (source_id, external_id, member_hash, display_name, reviewed_by)
			VALUES (?, ?, ?, ?, ?)
			ON CONFLICT (source_id, external_id) DO UPDATE SET
			    member_hash = EXCLUDED.member_hash,
			    display_name = EXCLUDED.display_name,
			    reviewed_by = EXCLUDED.reviewed_by,
			    updated_at = now()`,
			srcID, row.ExternalID, derivedseries.MemberHash(ids), name, *reviewedBy).Error; err != nil {
			slog.Error("upsert override", "external_id", row.ExternalID, "error", err)
			os.Exit(1)
		}
	}
	if err := sc.Err(); err != nil {
		slog.Error("read input", "error", err)
		os.Exit(1)
	}
	slog.Info("apply-series-name-overrides done", "apply", *apply,
		"written", written, "skipped_missing_series", skippedMissing,
		"skipped_unverified_name", skippedUnverified, "skipped_duplicate", skippedDup,
		"malformed", malformed)
	if !*apply {
		fmt.Println("DRY RUN — nothing written; re-run with --apply (names take effect on the next build-derived-series --apply)")
	}
}
