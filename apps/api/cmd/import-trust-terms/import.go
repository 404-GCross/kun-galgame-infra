package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"api/internal/platform/trust/model"
	"api/internal/platform/trust/norm"

	"bufio"

	"gorm.io/gorm"
)

// insertBatchSize is the CreateInBatches chunk size (the repo-wide convention
// for bulk term/label loads).
const insertBatchSize = 500

// importConfig is the resolved run configuration (the parsed CLI flags).
type importConfig struct {
	// site is the target scope: nil = a GLOBAL term (stored site NULL); a
	// non-nil pointer scopes every imported row to that catalog_site.
	site *string
	// kind is the enforcement intent written explicitly onto every row
	// (0=suspect / 1=banned) — an INTENT column, so never left to a DB default.
	kind int16
	// purpose is why the terms are listed, written explicitly onto every row.
	// It decides which evidence may later retire them (see model.TrustTerm).
	purpose int16
	// note is the operator memo flag. Empty = per-file default (the source
	// filename); non-empty = a fixed memo applied to every file's rows.
	note string
	// minRunes drops a term whose POST-Normalize rune count is below it.
	minRunes int
	// apply switches from dry-run (default) to a real batched INSERT.
	apply bool
}

// fileStats is the per-file (and, aggregated, the total) counters of one import
// pass. inserted is the accepted-for-insert count — a would-insert in dry-run,
// the actually-inserted count in apply mode (they are equal: dedup + the
// existing-set preload guarantee the batched INSERT never conflicts).
type fileStats struct {
	name          string
	read          int
	blankComment  int
	invalidUTF8   int
	shortFiltered int
	dupInBatch    int
	dupExisting   int
	inserted      int
	samples       []sample
}

// sample is one raw->norm illustration printed in dry-run (first 5 per file).
type sample struct {
	raw  string
	norm string
}

// add folds one file's counters into the running total (samples excluded — the
// total carries none).
func (t *fileStats) add(f fileStats) {
	t.read += f.read
	t.blankComment += f.blankComment
	t.invalidUTF8 += f.invalidUTF8
	t.shortFiltered += f.shortFiltered
	t.dupInBatch += f.dupInBatch
	t.dupExisting += f.dupExisting
	t.inserted += f.inserted
}

// runResult is the outcome of an import run: the per-file stats and their total.
type runResult struct {
	perFile []fileStats
	total   fileStats
}

// resolveNote returns the note pointer for one file: the fixed -note flag when
// set, otherwise the file's base name (the per-file source marker). The note is
// always non-empty (a filename is), so this never returns nil in practice.
func resolveNote(flagNote, filename string) *string {
	n := flagNote
	if n == "" {
		n = filepath.Base(filename)
	}
	if n == "" {
		return nil
	}
	return &n
}

// processFile runs the line pipeline for ONE file against the SHARED run state:
//
//	seen     — every distinct normalized term already encountered THIS run (all
//	           files import into the single -site scope, so a term repeated
//	           across files is an in-batch duplicate); mutated here.
//	existing — the preloaded set of active term_norms already in the DB for this
//	           scope; read-only.
//
// It never touches the DB. Accepted terms are returned as ready-to-insert
// TrustTerm rows (site/kind/note set, is_deprecated false).
func processFile(name string, r io.Reader, cfg importConfig, seen, existing map[string]struct{}) (fileStats, []model.TrustTerm, error) {
	stats := fileStats{name: name}
	note := resolveNote(cfg.note, name)
	var out []model.TrustTerm

	sc := bufio.NewScanner(r)
	// Word-list lines are short, but raise the token cap so a pathological long
	// line errors loudly rather than silently truncating.
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		stats.read++
		line := strings.TrimSpace(sc.Text())

		switch {
		case line == "":
			stats.blankComment++
			continue
		case !utf8.ValidString(line):
			stats.invalidUTF8++
			continue
		case strings.HasPrefix(line, "#"):
			stats.blankComment++
			continue
		}

		normed := norm.Normalize(line)
		if len(stats.samples) < 5 {
			stats.samples = append(stats.samples, sample{raw: line, norm: normed})
		}

		// A term whose normalized form is empty or below the rune floor is noise.
		if normed == "" || utf8.RuneCountInString(normed) < cfg.minRunes {
			stats.shortFiltered++
			continue
		}
		// In-batch dedup FIRST (spec pipeline order): every distinct candidate is
		// recorded in seen, so a repeat — within or across files — is a dup.
		if _, ok := seen[normed]; ok {
			stats.dupInBatch++
			continue
		}
		seen[normed] = struct{}{}
		// Then dedup against the DB's active terms in this scope (idempotency: a
		// second apply run finds every term already present here → 0 inserted).
		if _, ok := existing[normed]; ok {
			stats.dupExisting++
			continue
		}

		out = append(out, model.TrustTerm{
			Site:         cfg.site,
			TermNorm:     normed,
			Kind:         cfg.kind,
			Purpose:      cfg.purpose,
			Note:         note,
			IsDeprecated: false,
		})
		stats.inserted++
	}
	if err := sc.Err(); err != nil {
		return stats, nil, fmt.Errorf("scan %s: %w", name, err)
	}
	return stats, out, nil
}

// loadExisting preloads the active term_norms already registered for the target
// scope (site NULL for global, else the exact site) into a set — the dedup
// backstop that makes the tool idempotent.
func loadExisting(db *gorm.DB, site *string) (map[string]struct{}, error) {
	q := db.Model(&model.TrustTerm{}).Where("is_deprecated = false")
	if site == nil {
		q = q.Where("site IS NULL")
	} else {
		q = q.Where("site = ?", *site)
	}
	var norms []string
	if err := q.Pluck("term_norm", &norms).Error; err != nil {
		return nil, fmt.Errorf("preload existing terms: %w", err)
	}
	set := make(map[string]struct{}, len(norms))
	for _, n := range norms {
		set[n] = struct{}{}
	}
	return set, nil
}

// insertBatches writes the accepted terms in 500-row batches. Dedup + the
// existing-set preload guarantee no active-uniqueness conflict, so this is a
// plain CreateInBatches (no ON CONFLICT clause).
func insertBatches(db *gorm.DB, terms []model.TrustTerm, batchSize int) error {
	if len(terms) == 0 {
		return nil
	}
	if err := db.CreateInBatches(terms, batchSize).Error; err != nil {
		return fmt.Errorf("insert terms: %w", err)
	}
	return nil
}

// run is the whole import: preload the existing-scope set, stream every file
// through the pipeline (printing per-file stats), optionally write the accepted
// rows, then print the total. Dry-run (cfg.apply == false) reads but never
// writes.
func run(db *gorm.DB, out io.Writer, cfg importConfig, files []string) (runResult, error) {
	existing, err := loadExisting(db, cfg.site)
	if err != nil {
		return runResult{}, err
	}

	seen := make(map[string]struct{})
	var toInsert []model.TrustTerm
	res := runResult{total: fileStats{name: "TOTAL"}}

	for _, f := range files {
		fh, err := os.Open(f)
		if err != nil {
			return res, fmt.Errorf("open %s: %w", f, err)
		}
		stats, terms, perr := processFile(f, fh, cfg, seen, existing)
		fh.Close()
		if perr != nil {
			return res, perr
		}
		toInsert = append(toInsert, terms...)
		res.perFile = append(res.perFile, stats)
		res.total.add(stats)
		printStats(out, stats, cfg.apply, true)
	}

	if cfg.apply {
		if err := insertBatches(db, toInsert, insertBatchSize); err != nil {
			return res, err
		}
	}

	printStats(out, res.total, cfg.apply, false)
	return res, nil
}

// printStats renders one stats block. withSamples prints the first-5 raw->norm
// preview (dry-run only, per-file only — never for the total).
func printStats(w io.Writer, s fileStats, apply, withSamples bool) {
	insertedLabel := "would-insert"
	if apply {
		insertedLabel = "inserted"
	}
	label := "file: " + s.name
	if s.name == "TOTAL" {
		label = "TOTAL"
	}
	fmt.Fprintln(w, label)
	fmt.Fprintf(w, "  read           %8d\n", s.read)
	fmt.Fprintf(w, "  blank+comment  %8d\n", s.blankComment)
	fmt.Fprintf(w, "  invalid-utf8   %8d\n", s.invalidUTF8)
	fmt.Fprintf(w, "  short-filtered %8d\n", s.shortFiltered)
	fmt.Fprintf(w, "  dup-in-batch   %8d\n", s.dupInBatch)
	fmt.Fprintf(w, "  dup-existing   %8d\n", s.dupExisting)
	fmt.Fprintf(w, "  %-13s  %8d\n", insertedLabel, s.inserted)
	if withSamples && !apply && len(s.samples) > 0 {
		fmt.Fprintln(w, "  samples (raw -> norm):")
		for _, sm := range s.samples {
			fmt.Fprintf(w, "    %q -> %q\n", sm.raw, sm.norm)
		}
	}
}
