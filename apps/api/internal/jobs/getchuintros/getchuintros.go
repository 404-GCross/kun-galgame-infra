// Package getchuintros projects the Getchu crawler's work synopses onto
// catalog works as Japanese intros (refs/proj/167 §9).
//
// WHY THIS FACET. 2,562 published works carry no Japanese intro, and no
// upstream already ingested closes them at scale: Bangumi's summaries were
// taken in wave 166, VNDB's descriptions are English, DLsite's are for the
// releases it happens to sell. Getchu carries a synopsis for 12,565 items, and
// 682 of them land on a published work that has nothing in Japanese today.
//
// IT COMPOUNDS. Of those 682, 224 have no Chinese intro either. The resident
// intro-mt schedule translates Japanese into Chinese nightly, so a work this
// lane fills is picked up without a second wave — one supply lane closes part
// of both gaps. The other 458 already read in Chinese and gain the Japanese
// face alone.
//
// MATCHING IS FREE. Unlike getchuchars, nothing is matched here: wave 167
// minted EXACT Getchu release anchors through VNDB extlinks, so the link from a
// work to its Getchu page already exists and is first-party. The only choice
// this lane makes is which of a work's several Getchu releases to read from —
// see pickStory.
//
// FILL-MISSING, NEVER OVERWRITE. A row is written only when the work has NO
// Japanese intro from ANY source. That keeps the lane off curated first-party
// text and off the Bangumi/DLsite intros already landed, and makes a second
// --apply a zero-write no-op.
package getchuintros

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"api/internal/jobs/workpop"
	"api/internal/platform/catalog/model"
	"api/internal/platform/catalog/repository"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	gormlogger "gorm.io/gorm/logger"
)

// langJa — Getchu is a Japanese storefront and publishes nothing else, so the
// language is a property of the source, not something to detect. (The 164
// lesson about never guessing a language tag applies to mixed-language dumps
// like Bangumi; it does not turn a single-language upstream into a guess.)
const langJa = "ja"

// maxSamples caps the per-category example rows a run collects for logging.
const maxSamples = 8

// previewRunes is how many runes of a synopsis a Sample carries.
const previewRunes = 40

// Opts configures a run. Both DSNs are explicit and never defaulted: this job
// reads a staging database and writes the live catalog, and a bare invocation
// must not be able to guess either.
type Opts struct {
	DSN        string // catalog — REQUIRED
	GetchuDSN  string // the crawler's staging database — REQUIRED
	Apply      bool
	Limit      int // max WORKS to process (0 = all)
	Offset     int
	Population workpop.Population // empty = all
}

// Sample is one example decision, for dry-run logging and test assertions.
type Sample struct {
	WorkID   int64
	GetchuID string
	Preview  string
}

// Stats reports a run. Planned is the decision (identical in dry and apply);
// Written counts rows actually inserted.
type Stats struct {
	Works     int // works in the population carrying an exact Getchu anchor
	NoStory   int // every anchored Getchu item is unfetched, gone, or storyless
	SkipHasJa int // the work already reads in Japanese (any source)
	Planned   int // decided writes
	Written   int // rows inserted (apply)
	Conflict  int // ON CONFLICT said the row was already there
	Errors    int

	PlanSamples    []Sample
	NoStorySamples []Sample
}

func (s Stats) String() string {
	return fmt.Sprintf("works=%d no_story=%d skip_has_ja=%d planned=%d written=%d conflict=%d errors=%d",
		s.Works, s.NoStory, s.SkipHasJa, s.Planned, s.Written, s.Conflict, s.Errors)
}

// Run resolves the candidates and forecasts (dry) or writes (apply) the
// fill-missing Japanese intro rows.
func Run(ctx context.Context, opts Opts) (*Stats, error) {
	if opts.DSN == "" || opts.GetchuDSN == "" {
		return nil, fmt.Errorf("--dsn and --getchu-dsn are both REQUIRED; refusing to guess either")
	}
	db, err := open(opts.DSN)
	if err != nil {
		return nil, fmt.Errorf("connect catalog: %w", err)
	}
	defer closeDB(db)
	gdb, err := open(opts.GetchuDSN)
	if err != nil {
		return nil, fmt.Errorf("connect getchu staging: %w", err)
	}
	defer closeDB(gdb)

	var source int16
	if err := db.WithContext(ctx).
		Raw(`SELECT id FROM catalog_source WHERE key = 'getchu'`).Scan(&source).Error; err != nil {
		return nil, err
	}
	if source == 0 {
		return nil, fmt.Errorf("catalog_source has no getchu row — seed it first (refs/proj/167)")
	}

	anchors, err := loadAnchors(ctx, db, source, opts.Population, opts.Limit, opts.Offset)
	if err != nil {
		return nil, err
	}
	stories, err := loadStories(ctx, gdb)
	if err != nil {
		return nil, err
	}
	cands := pickStory(anchors, stories)

	workIDs := make([]int64, 0, len(cands))
	for _, c := range cands {
		workIDs = append(workIDs, c.WorkID)
	}
	exist, err := preloadExistingLangs(ctx, db, workIDs)
	if err != nil {
		return nil, fmt.Errorf("preload existing intro langs: %w", err)
	}

	r := &runner{db: db, source: source, exist: exist, stats: &Stats{Works: len(cands)}}
	slog.Info("getchu-intros candidates", "works", len(cands), "stories", len(stories),
		"apply", opts.Apply, "population", opts.Population, "offset", opts.Offset, "limit", opts.Limit)
	for _, c := range cands {
		if ctx.Err() != nil {
			break
		}
		r.enrich(ctx, c, opts.Apply)
	}
	if err := r.touch(ctx); err != nil {
		return nil, fmt.Errorf("touch works: %w", err)
	}

	slog.Info("getchu-intros done", "apply", opts.Apply, "result", r.stats.String())
	logSamples("planned", r.stats.PlanSamples)
	logSamples("no_story", r.stats.NoStorySamples)
	return r.stats, nil
}

// candidate is one work paired with the synopsis this lane would write.
// Story is empty when no anchored Getchu item had one — counted, not filtered,
// so the run reports the reach of the crawl rather than hiding it.
type candidate struct {
	WorkID   int64
	GetchuID string
	Story    string
}

// pickStory collapses a work's Getchu anchors to at most one synopsis.
//
// A work can carry several Getchu releases and the crawl is uneven — one page
// fetched with a story, another 'gone' (404) or fetched before Getchu carried a
// story block. Choosing in SQL (DISTINCT ON, lowest id) would have picked the
// empty one and reported the work as unreachable; choosing here means the work
// is only counted no_story when NONE of its anchors has text. Among several
// that do have text the lowest Getchu id wins — anchors arrive ordered, so the
// choice is deterministic and re-runs are stable.
func pickStory(anchors []anchorRow, stories map[string]string) []candidate {
	var out []candidate
	for i := 0; i < len(anchors); {
		work := anchors[i].WorkID
		c := candidate{WorkID: work, GetchuID: anchors[i].GetchuID}
		for ; i < len(anchors) && anchors[i].WorkID == work; i++ {
			if c.Story != "" {
				continue
			}
			if s := strings.TrimSpace(stories[anchors[i].GetchuID]); s != "" {
				c.GetchuID, c.Story = anchors[i].GetchuID, s
			}
		}
		out = append(out, c)
	}
	return out
}

// runner carries per-run dependencies and stats (serial, plain ints).
type runner struct {
	db     *gorm.DB
	source int16
	exist  map[int64]map[string]bool // work → intro langs already present (any source)
	stats  *Stats
	// touched collects works that actually gained a row, so the run bumps their
	// catalog_work.updated_at once at the end and the public changes feed learns
	// they are worth re-pulling. Skips, conflicts and dry-runs contribute
	// nothing, so a second --apply moves no watermark.
	touched []int64
}

func (r *runner) touch(ctx context.Context) error {
	return repository.TouchWorks(ctx, r.db, r.touched)
}

func (r *runner) enrich(ctx context.Context, c candidate, apply bool) {
	if c.Story == "" {
		r.stats.NoStory++
		r.collect(&r.stats.NoStorySamples, c)
		return
	}
	if r.exist[c.WorkID][langJa] {
		r.stats.SkipHasJa++
		return
	}

	r.stats.Planned++
	r.collect(&r.stats.PlanSamples, c)
	if !apply {
		return
	}

	res := r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "work_id"}, {Name: "lang"}, {Name: "source_id"}},
		DoNothing: true,
	}).Create(&model.CatalogWorkIntro{
		WorkID: c.WorkID, Lang: langJa, Intro: c.Story, SourceID: r.source,
	})
	switch {
	case res.Error != nil:
		r.stats.Errors++
		slog.Warn("write work intro", "work", c.WorkID, "getchu", c.GetchuID, "err", res.Error)
	case res.RowsAffected == 0:
		r.stats.Conflict++
	default:
		r.stats.Written++
		r.touched = append(r.touched, c.WorkID)
		set := r.exist[c.WorkID]
		if set == nil {
			set = map[string]bool{}
			r.exist[c.WorkID] = set
		}
		set[langJa] = true
	}
}

func (r *runner) collect(dst *[]Sample, c candidate) {
	if len(*dst) >= maxSamples {
		return
	}
	*dst = append(*dst, Sample{WorkID: c.WorkID, GetchuID: c.GetchuID, Preview: preview(c.Story)})
}

func preview(s string) string {
	s = strings.NewReplacer("\n", " ", "\r", " ").Replace(s)
	runes := []rune(s)
	if len(runes) <= previewRunes {
		return s
	}
	return string(runes[:previewRunes]) + "…"
}

func logSamples(category string, samples []Sample) {
	for _, s := range samples {
		slog.Info("getchu-intros sample", "category", category,
			"work_id", s.WorkID, "getchu_id", s.GetchuID, "preview", s.Preview)
	}
}

func open(dsn string) (*gorm.DB, error) {
	return gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: gormlogger.Default.LogMode(gormlogger.Silent)})
}

func closeDB(db *gorm.DB) {
	if sqlDB, err := db.DB(); err == nil {
		_ = sqlDB.Close()
	}
}
