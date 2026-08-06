// cmd/trust-term-prune retires Tier0 word-list terms by MEASURED PRECISION,
// using the pipeline's own shadow record as the evidence.
//
// Every scan stores the terms it matched (trust_scan_result.tier0_matched)
// alongside the AI verdict for the same text. Joining the two gives, per term,
// how often it fired and how often the content it fired on was actually judged
// abusive — that ratio is the term's precision, and it is the only honest basis
// for keeping or dropping a word. The counterpart tool cmd/import-trust-terms
// adds terms in bulk; this one is how they earn their place afterwards.
//
// Why this matters more than it sounds: a word list is the ONE tier that can
// block deterministically, and a bad term there is not a mild inefficiency — it
// silently suppresses legitimate speech at a rate nobody measures, because a
// false positive on the keyword tier produces no signal at all unless something
// like this goes looking for it.
//
// Usage:
//
//	go run ./cmd/trust-term-prune [flags]
//
// Flags:
//
//	-source          only consider terms whose note contains this substring
//	                 ("" = all); use it to retire one imported lexicon at a time.
//	-min-hits        observations a term needs before its precision is trusted (20).
//	-max-precision   deprecate evidenced terms scoring BELOW this (0.10 = 10%).
//	-drop-unevidenced  also deprecate terms with fewer than -min-hits matches —
//	                 "never demonstrated value", a weaker claim than "measured bad".
//
// Terms with purpose=compliance are NEVER retired by this tool. Their precision
// is measured against an abuse classifier that does not judge compliance
// content, so the number reads ~0% however well the term works; they are printed
// for human review instead.
//	-backup          write the full affected set to this JSON file before writing.
//	-apply           write; default is a dry-run report (nothing is changed).
//
// Deprecation is TERMINAL by design (the admin face offers no un-deprecate), so
// -apply refuses to run without -backup: the backup is what makes a wrong call
// recoverable via cmd/import-trust-terms.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"sort"
	"time"

	"api/internal/infrastructure/database"
	"api/internal/platform/trust/model"
	"api/internal/platform/trust/service"
	"api/pkg/config"
	"api/pkg/logger"

	"gorm.io/gorm"
)

// termStat is one term's observed record: how many scans it matched, and how
// many of those the classifier independently judged abusive.
type termStat struct {
	ID        int64   `json:"id"`
	Term      string  `json:"term"`
	Kind      int16   `json:"kind"`
	Purpose   int16   `json:"purpose"`
	Site      *string `json:"site,omitempty"`
	Note      *string `json:"note,omitempty"`
	Hits      int64   `json:"hits"`
	Flagged   int64   `json:"flagged"`
	Precision float64 `json:"precision"`
	// Reason records WHY this term was retired, so the backup file explains
	// itself months later without the operator having to reconstruct the flags.
	Reason string `json:"reason"`
}

func main() {
	source := flag.String("source", "", "only consider terms whose note contains this substring")
	minHits := flag.Int64("min-hits", 20, "matches required before a term's precision is trusted")
	maxPrecision := flag.Float64("max-precision", 0.10, "deprecate evidenced terms scoring below this")
	dropUnevidenced := flag.Bool("drop-unevidenced", false, "also deprecate terms below -min-hits matches")
	backup := flag.String("backup", "", "write the affected set to this JSON file (required with -apply)")
	apply := flag.Bool("apply", false, "write to the database (default: dry-run report)")
	flag.Parse()

	if *apply && *backup == "" {
		fmt.Fprintln(os.Stderr, "-apply requires -backup: deprecation is terminal and must be recoverable")
		os.Exit(2)
	}

	cfg, err := config.Load()
	if err != nil {
		slog.Error("failed to load config", "error", err)
		os.Exit(1)
	}
	logger.Init(cfg.Server.Env)
	slog.Info("connecting to trust database", "dbname", cfg.TrustDatabase.DBName)

	db, err := database.NewPostgresDB(cfg.TrustDatabase)
	if err != nil {
		slog.Error("failed to connect to database", "error", err)
		os.Exit(1)
	}
	defer db.Close()

	stats, corpus, err := collect(db.DB(), *source)
	if err != nil {
		slog.Error("collecting term statistics", "error", err)
		os.Exit(1)
	}

	doomed, review := classify(stats, *minHits, *maxPrecision, *dropUnevidenced)
	report(stats, doomed, review, corpus, *minHits, *maxPrecision)

	if len(doomed) == 0 {
		slog.Info("nothing to retire under this policy")
		return
	}
	if *backup != "" {
		if err := writeBackup(*backup, doomed); err != nil {
			slog.Error("writing backup", "error", err)
			os.Exit(1)
		}
		slog.Info("backup written", "path", *backup, "terms", len(doomed))
	}
	if !*apply {
		slog.Info("dry run — nothing written; re-run with -apply to retire these terms")
		return
	}
	if err := deprecate(db.DB(), doomed, *source, *minHits, *maxPrecision, *dropUnevidenced); err != nil {
		slog.Error("deprecating terms", "error", err)
		os.Exit(1)
	}
	slog.Info("terms retired", "count", len(doomed))
}

// collect joins every active term against its observed match record. Terms that
// never fired come back with zero counts rather than being omitted — "never
// fired" is itself a finding, and the caller decides what to do with it.
// It also returns the corpus size (scans carrying an evaluated tier0 record),
// without which none of the ratios can be read honestly.
func collect(db *gorm.DB, source string) ([]termStat, int64, error) {
	var corpus int64
	if err := db.Model(&model.TrustScanResult{}).
		Where("tier0_matched IS NOT NULL").Count(&corpus).Error; err != nil {
		return nil, 0, err
	}

	// The per-term aggregate is computed in the database: unnesting the jsonb
	// match arrays here would mean streaming every scan row into this process.
	const q = `
		WITH matched AS (
		    SELECT coalesce(flagged, false) AS flagged,
		           jsonb_array_elements_text(tier0_matched) AS term
		      FROM trust_scan_result
		     WHERE tier0_matched IS NOT NULL
		       AND jsonb_array_length(tier0_matched) > 0
		), per_term AS (
		    SELECT term,
		           count(*) AS hits,
		           count(*) FILTER (WHERE flagged) AS flagged
		      FROM matched GROUP BY term
		)
		SELECT t.id, t.term_norm AS term, t.kind, t.purpose, t.site, t.note,
		       coalesce(p.hits, 0) AS hits,
		       coalesce(p.flagged, 0) AS flagged
		  FROM trust_term t
		  LEFT JOIN per_term p ON p.term = t.term_norm
		 WHERE t.is_deprecated = false
		   AND (? = '' OR coalesce(t.note, '') LIKE '%' || ? || '%')
		 ORDER BY hits DESC, t.term_norm ASC`

	var stats []termStat
	if err := db.Raw(q, source, source).Scan(&stats).Error; err != nil {
		return nil, 0, err
	}
	for i := range stats {
		if stats[i].Hits > 0 {
			stats[i].Precision = float64(stats[i].Flagged) / float64(stats[i].Hits)
		}
	}
	return stats, corpus, nil
}

// classify applies the retirement policy, separating the two claims it can make:
// a term that fired enough to be judged and scored badly ("measured"), and a
// term that never fired at all ("unevidenced"). They are different assertions —
// the first is proof of harm, the second only absence of value — so the second
// is opt-in.
// A COMPLIANCE term is never auto-retired, whatever its precision reads. The
// number the policy tests is agreement with the ABUSE classifier, and a
// compliance term answers a question that classifier never asks — so it scores
// ~0% precision while working perfectly. Judging it by that number would empty
// the compliance lexicon and produce a report that looked fully evidence-based
// while doing it. Such terms go to a review bucket for a human instead.
func classify(stats []termStat, minHits int64, maxPrecision float64, dropUnevidenced bool) (doomed, review []termStat) {
	for _, s := range stats {
		var reason string
		switch {
		case s.Hits >= minHits && s.Precision < maxPrecision:
			reason = fmt.Sprintf("measured: %d hits, %d flagged, precision %.4f < %.4f",
				s.Hits, s.Flagged, s.Precision, maxPrecision)
		case s.Hits < minHits && dropUnevidenced:
			reason = fmt.Sprintf("unevidenced: %d hits (< %d), never demonstrated value",
				s.Hits, minHits)
		default:
			continue
		}
		s.Reason = reason
		if s.Purpose == model.TermPurposeCompliance {
			review = append(review, s)
			continue
		}
		doomed = append(doomed, s)
	}
	return doomed, review
}

// report prints the decision's basis before anything is written. A pruning run
// that only says "retired 46,000 terms" is unreviewable; the operator needs the
// corpus size, the survivors, and the worst offenders by volume to judge whether
// the policy did what they meant.
func report(stats, doomed, review []termStat, corpus int64, minHits int64, maxPrecision float64) {
	var fired, totalHits, totalFlagged int64
	for _, s := range stats {
		if s.Hits > 0 {
			fired++
			totalHits += s.Hits
			totalFlagged += s.Flagged
		}
	}

	fmt.Printf("\nCorpus:      %d scans with an evaluated Tier0 record\n", corpus)
	fmt.Printf("Active terms: %d considered, %d ever fired, %d never fired\n",
		len(stats), fired, int64(len(stats))-fired)
	if totalHits > 0 {
		fmt.Printf("Aggregate:   %d matches, %d on flagged content (precision %.4f)\n",
			totalHits, totalFlagged, float64(totalFlagged)/float64(totalHits))
	}
	fmt.Printf("Policy:      deprecate if hits >= %d and precision < %.4f\n\n", minHits, maxPrecision)

	// Highest-volume terms first: at scale the damage a word list does is
	// dominated by its loudest few entries, not by the long tail.
	loud := make([]termStat, len(stats))
	copy(loud, stats)
	sort.Slice(loud, func(i, j int) bool { return loud[i].Hits > loud[j].Hits })
	fmt.Println("Loudest terms (hits / flagged / precision):")
	for i, s := range loud {
		if i >= 20 || s.Hits == 0 {
			break
		}
		verdict := "KEEP"
		if s.Hits >= minHits && s.Precision < maxPrecision {
			verdict = "RETIRE"
		}
		fmt.Printf("  %-24s %7d %7d  %6.2f%%  %s\n",
			s.Term, s.Hits, s.Flagged, s.Precision*100, verdict)
	}
	if len(review) > 0 {
		fmt.Printf("\nCompliance terms failing the policy (NOT retired — a human decides):\n")
		for _, s := range review {
			fmt.Printf("  %-24s %7d %7d  %6.2f%%  %s\n",
				s.Term, s.Hits, s.Flagged, s.Precision*100, noteOf(s))
		}
		fmt.Println("  (the abuse classifier does not judge compliance content; this number" +
			"\n   cannot convict these terms, only nominate them for review)")
	}

	fmt.Printf("\nWould retire %d of %d terms.\n\n", len(doomed), len(stats))
}

func noteOf(s termStat) string {
	if s.Note == nil {
		return ""
	}
	return *s.Note
}

func writeBackup(path string, doomed []termStat) error {
	b, err := json.MarshalIndent(doomed, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}

// deprecate flips the batch in one transaction and appends a SINGLE audit row
// describing the policy. One row per term would bury the audit log under tens of
// thousands of mechanically identical entries; what a future reader needs is the
// rule that was applied and when, which the policy_ref carries.
func deprecate(db *gorm.DB, doomed []termStat, source string, minHits int64, maxPrecision float64, dropUnevidenced bool) error {
	ids := make([]int64, len(doomed))
	for i, s := range doomed {
		ids[i] = s.ID
	}
	policy := fmt.Sprintf(
		"trust-term-prune source=%q min_hits=%d max_precision=%.4f drop_unevidenced=%t terms=%d at=%s",
		source, minHits, maxPrecision, dropUnevidenced, len(ids), time.Now().UTC().Format(time.RFC3339))

	return db.Transaction(func(tx *gorm.DB) error {
		// Batched: a single IN () with tens of thousands of parameters exceeds
		// what the driver will bind.
		const batch = 1000
		for start := 0; start < len(ids); start += batch {
			end := min(start+batch, len(ids))
			if err := tx.Model(&model.TrustTerm{}).
				Where("id IN ?", ids[start:end]).
				Update("is_deprecated", true).Error; err != nil {
				return err
			}
		}
		return service.AppendAudit(tx, service.AuditEntry{
			// ActorID stays NULL: no operator acted on an individual term — the
			// policy did, and the policy is recorded in policy_ref.
			Action:    "terms_pruned_by_precision",
			PolicyRef: &policy,
		})
	})
}
