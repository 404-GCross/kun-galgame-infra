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

const defaultBatchSize = 20

type ClassifyOpts struct {
	DSN       string
	Sources   []string
	Vocab     bool
	Out       string
	MinUses   int
	Limit     int
	BatchSize int
}

type ClassifyStats struct {
	Pool        int
	Skipped     int
	Judged      int
	Errors      int
	Batches     int
	ClassCounts map[Class]int
}

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
			st.Errors += len(batch)
			slog.Warn("classify batch", "source", batch[0].Source, "n", len(batch), "err", err)
			continue
		}
		recs := make([]Verdict, 0, len(batch))
		for i, v := range verdicts {
			if v.Class == "" {
				st.Errors++
				continue
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

func loadCanonicalNames(ctx context.Context, db *gorm.DB) ([]string, error) {
	var names []string
	if err := db.WithContext(ctx).Raw(`SELECT name FROM catalog_tag`).Scan(&names).Error; err != nil {
		return nil, fmt.Errorf("load canonical names: %w", err)
	}
	return names, nil
}

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

func openGorm(dsn string) (*gorm.DB, error) {
	return database.OpenJob(dsn)
}
