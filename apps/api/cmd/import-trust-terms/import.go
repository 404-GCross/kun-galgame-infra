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

const insertBatchSize = 500

type importConfig struct {
	site     *string
	kind     int16
	purpose  int16
	note     string
	minRunes int
	apply    bool
}

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

type sample struct {
	raw  string
	norm string
}

func (t *fileStats) add(f fileStats) {
	t.read += f.read
	t.blankComment += f.blankComment
	t.invalidUTF8 += f.invalidUTF8
	t.shortFiltered += f.shortFiltered
	t.dupInBatch += f.dupInBatch
	t.dupExisting += f.dupExisting
	t.inserted += f.inserted
}

type runResult struct {
	perFile []fileStats
	total   fileStats
}

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

func processFile(name string, r io.Reader, cfg importConfig, seen, existing map[string]struct{}) (fileStats, []model.TrustTerm, error) {
	stats := fileStats{name: name}
	note := resolveNote(cfg.note, name)
	var out []model.TrustTerm

	sc := bufio.NewScanner(r)
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

		if normed == "" || utf8.RuneCountInString(normed) < cfg.minRunes {
			stats.shortFiltered++
			continue
		}
		if _, ok := seen[normed]; ok {
			stats.dupInBatch++
			continue
		}
		seen[normed] = struct{}{}
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

func insertBatches(db *gorm.DB, terms []model.TrustTerm, batchSize int) error {
	if len(terms) == 0 {
		return nil
	}
	if err := db.CreateInBatches(terms, batchSize).Error; err != nil {
		return fmt.Errorf("insert terms: %w", err)
	}
	return nil
}

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
