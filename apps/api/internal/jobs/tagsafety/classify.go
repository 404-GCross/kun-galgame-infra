package tagsafety

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"

	"api/internal/infrastructure/database"

	"gorm.io/gorm"
)

// defaultBatchSize is the pinned names-per-call batch. Small enough that a
// glm-5.2 reply stays well inside max_tokens (a truncated batch reply is
// refused outright, costing the whole call), large enough that 12k bangumi names
// cost ~600 calls instead of 12,259.
const defaultBatchSize = 20

// ClassifyOpts configures the classify phase: vocabulary → LLM seam → JSONL.
type ClassifyOpts struct {
	// DSN is REQUIRED — the catalog DB holding catalog_work_tag / catalog_tag.
	// A bare run cannot touch a live DB.
	DSN string
	// Sources are the registry keys whose catalog_work_tag vocabulary is judged
	// (default bangumi + dlsite).
	Sources []string
	// Vocab additionally loads every catalog_tag canonical name as a
	// VocabSource row — the canonical layer's own sexual flag is
	// false-by-construction for bangumi-only mints and needs the same pass.
	Vocab bool
	// Out is the JSONL verdict path (REQUIRED). It is APPENDED to: names already
	// present are skipped, so an interrupted run resumes.
	Out string
	// MinUses gates the vocabulary by distinct-work usage (default 1 = all).
	MinUses int
	// Limit caps how many NEW names this run judges (0 = no cap). Every apply
	// tool in this repo carries a rehearsal limiter; this is classify's.
	Limit int
	// BatchSize overrides defaultBatchSize.
	BatchSize int
}

// ClassifyStats reports a classify run.
type ClassifyStats struct {
	Pool        int // names loaded from the DB after --min-uses
	Skipped     int // already present in --out (resume)
	Judged      int // verdicts written this run
	Errors      int // names the model dropped/garbled (re-asked on the next resume)
	Batches     int
	ClassCounts map[Class]int
}

// Classify loads the vocabulary, judges it through the Classifier seam and
// appends the verdicts to the output JSONL. cl is the seam (REQUIRED).
func Classify(ctx context.Context, cl Classifier, opts ClassifyOpts) (*ClassifyStats, error) {
	if opts.DSN == "" {
		return nil, fmt.Errorf("catalog DSN is required (--dsn)")
	}
	if opts.Out == "" {
		return nil, fmt.Errorf("output path is required (--out)")
	}
	if cl == nil {
		return nil, fmt.Errorf("a classifier is required (use MockClassifier for the offline rehearsal)")
	}
	if len(opts.Sources) == 0 {
		opts.Sources = []string{"bangumi", "dlsite"}
	}
	if opts.MinUses <= 0 {
		opts.MinUses = 1
	}
	if opts.BatchSize <= 0 {
		opts.BatchSize = defaultBatchSize
	}

	db, err := openGorm(opts.DSN)
	if err != nil {
		return nil, fmt.Errorf("connect catalog db: %w", err)
	}
	if sqlDB, e := db.DB(); e == nil {
		defer sqlDB.Close()
	}

	pool, err := loadPool(ctx, db, opts)
	if err != nil {
		return nil, err
	}
	done, err := loadDone(opts.Out)
	if err != nil {
		return nil, fmt.Errorf("read resume state from %s: %w", opts.Out, err)
	}

	st := &ClassifyStats{Pool: len(pool), ClassCounts: map[Class]int{}}
	todo := selectTodo(pool, done, opts.Limit, st)
	slog.Info("tag-safety classify", "pool", st.Pool, "skipped_done", st.Skipped,
		"todo", len(todo), "batch", opts.BatchSize)

	app, err := newVerdictAppender(opts.Out)
	if err != nil {
		return nil, fmt.Errorf("open output: %w", err)
	}
	defer app.Close()

	for _, batch := range batches(todo, opts.BatchSize) {
		if ctx.Err() != nil {
			break
		}
		st.Batches++
		verdicts, model, err := cl.ClassifyBatch(ctx, batch)
		if err != nil {
			// Record-then-continue: one bad batch never aborts the run, and the
			// names in it are re-asked by the next resume.
			st.Errors += len(batch)
			slog.Warn("classify batch", "source", batch[0].Source, "n", len(batch), "err", err)
			continue
		}
		recs := make([]Verdict, 0, len(batch))
		for i, v := range verdicts {
			if v.Class == "" {
				st.Errors++
				continue // model dropped this slot — leave it for the next resume
			}
			recs = append(recs, Verdict{
				Source: batch[i].Source, Name: batch[i].Name,
				Uses: batch[i].Uses, Votes: batch[i].Votes,
				Class: string(v.Class), Confidence: v.Confidence, Reason: v.Reason, Model: model,
			})
			st.ClassCounts[v.Class]++
			st.Judged++
		}
		if err := app.append(recs...); err != nil {
			return st, fmt.Errorf("append verdicts: %w", err)
		}
	}
	slog.Info("tag-safety classify done", "judged", st.Judged, "errors", st.Errors,
		"batches", st.Batches, "out", opts.Out)
	return st, nil
}

// selectTodo drops names already judged (resume) and applies the rehearsal
// limiter. The pool arrives sorted, so a limited run always takes the same
// prefix and successive runs walk the vocabulary deterministically.
func selectTodo(pool []NameInput, done map[string]struct{}, limit int, st *ClassifyStats) []NameInput {
	todo := make([]NameInput, 0, len(pool))
	for _, n := range pool {
		if _, hit := done[verdictKey(n.Source, n.Name)]; hit {
			st.Skipped++
			continue
		}
		if limit > 0 && len(todo) >= limit {
			break
		}
		todo = append(todo, n)
	}
	return todo
}

// batches splits todo into per-source chunks: a batch never mixes sources, so
// the prompt can state which source it is judging (dlsite genres are curated
// official taxonomy, bangumi tags are raw folksonomy — the context matters).
func batches(todo []NameInput, size int) [][]NameInput {
	var out [][]NameInput
	for i := 0; i < len(todo); {
		j := i
		for j < len(todo) && j-i < size && todo[j].Source == todo[i].Source {
			j++
		}
		out = append(out, todo[i:j])
		i = j
	}
	return out
}

// loadPool reads the vocabulary: per requested source, distinct catalog_work_tag
// names with uses = distinct works and votes = summed count; plus (with --vocab)
// every catalog_tag canonical name under the VocabSource pseudo key.
func loadPool(ctx context.Context, db *gorm.DB, opts ClassifyOpts) ([]NameInput, error) {
	ids, err := resolveSources(ctx, db, opts.Sources)
	if err != nil {
		return nil, err
	}
	var pool []NameInput
	for _, key := range opts.Sources {
		rows, err := loadWorkTagVocab(ctx, db, ids[key], opts.MinUses)
		if err != nil {
			return nil, err
		}
		for _, r := range rows {
			pool = append(pool, NameInput{Source: key, Name: r.Name, Uses: r.Uses, Votes: r.Votes})
		}
	}
	if opts.Vocab {
		names, err := loadCanonicalNames(ctx, db)
		if err != nil {
			return nil, err
		}
		for _, n := range names {
			pool = append(pool, NameInput{Source: VocabSource, Name: n})
		}
	}
	// Deterministic order: by source, then by descending usage (the names that
	// matter most are judged first under a --limit), then name for stability.
	sort.SliceStable(pool, func(i, j int) bool {
		if pool[i].Source != pool[j].Source {
			return pool[i].Source < pool[j].Source
		}
		if pool[i].Uses != pool[j].Uses {
			return pool[i].Uses > pool[j].Uses
		}
		return pool[i].Name < pool[j].Name
	})
	return pool, nil
}

type vocabRow struct {
	Name  string `gorm:"column:name"`
	Uses  int    `gorm:"column:uses"`
	Votes int    `gorm:"column:votes"`
}

// loadWorkTagVocab reads one source's verbatim folksonomy: distinct name with
// uses = number of distinct works carrying it and votes = summed folksonomy
// count (bangumi's per-work vote tally; 0/1 for dlsite's official genres).
func loadWorkTagVocab(ctx context.Context, db *gorm.DB, sourceID int16, minUses int) ([]vocabRow, error) {
	var rows []vocabRow
	if err := db.WithContext(ctx).Raw(`
		SELECT name,
		       COUNT(DISTINCT work_id) AS uses,
		       COALESCE(SUM(count), 0)  AS votes
		FROM catalog_work_tag
		WHERE source_id = ? AND name <> ''
		GROUP BY name
		HAVING COUNT(DISTINCT work_id) >= ?`, sourceID, minUses).Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("load work tag vocab (source %d): %w", sourceID, err)
	}
	out := rows[:0]
	for _, r := range rows {
		if r.Name = strings.TrimSpace(r.Name); r.Name != "" {
			out = append(out, r)
		}
	}
	return out, nil
}

// loadCanonicalNames reads the whole canonical vocabulary (catalog_tag.name).
func loadCanonicalNames(ctx context.Context, db *gorm.DB) ([]string, error) {
	var names []string
	if err := db.WithContext(ctx).Raw(`SELECT name FROM catalog_tag`).Scan(&names).Error; err != nil {
		return nil, fmt.Errorf("load canonical names: %w", err)
	}
	return names, nil
}

// resolveSources maps registry keys → ids at runtime (never hardcoded ids — the
// tagcanon discipline: a rehearsal / prod DB with different seeds still works).
// VocabSource is not a registry key and is rejected here.
func resolveSources(ctx context.Context, db *gorm.DB, keys []string) (map[string]int16, error) {
	out := make(map[string]int16, len(keys))
	for _, key := range keys {
		if key == VocabSource {
			return nil, fmt.Errorf("%q is the canonical-vocabulary pseudo source, not a --sources value (use --vocab)", VocabSource)
		}
		var id int16
		if err := db.WithContext(ctx).Raw(`SELECT id FROM catalog_source WHERE key = ?`, key).Scan(&id).Error; err != nil {
			return nil, fmt.Errorf("resolve source %q: %w", key, err)
		}
		if id == 0 {
			return nil, fmt.Errorf("source %q not in the registry (catalog_source)", key)
		}
		out[key] = id
	}
	return out, nil
}

// openGorm opens a silent-logger gorm handle (shared by classify/apply).
func openGorm(dsn string) (*gorm.DB, error) {
	return database.OpenJob(dsn)
}
