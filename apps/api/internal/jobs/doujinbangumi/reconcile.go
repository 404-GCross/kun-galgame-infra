// Package doujinbangumi anchors BODYLESS galgame-doujin catalog works to
// Bangumi type=4 (game) subjects by normalized-title match, writing graded
// catalog_external_ref rows (BGM 竖封轨 Phase 1, refs/proj/56a). It ONLY writes
// anchors — no images, no schema changes, no enrichment.
//
// Match logic (spec §匹配逻辑, pinned):
//   - LHS: bodyless galgame works (medium=galgame, site NULL/empty) — the same
//     "55 anchor group" — via their catalog_work_title.title_norm (≥4 chars),
//     plus min(catalog_release.released_y) when present.
//   - RHS: src_bangumi.subject WHERE type=4, on name_norm OR name_cn_norm (≥4).
//   - Key: title_norm == name_norm (or name_cn_norm). Both are the IDENTICAL
//     lower(normalize(col, NFKC)) STORED generated column, so equality on the
//     raw strings is byte-isomorphic — no Go-side folding.
//
// Grading (written via repository.InsertRefIfAbsent, which never re-grades):
//   - exact (rule:bgm-title-year, LinkKindExact): the work matches exactly ONE
//     subject (work-side unique) AND that subject is matched by exactly ONE work
//     (bangumi-side unique — the anti-squatting guard) AND the work has a
//     release year within ±1 of the subject's.
//   - probable (rule:bgm-title-only, LinkKindProbable): work-side + bangumi-side
//     unique, but no release year corroborates (either side missing) or the
//     years disagree — a confirm-bucket assertion awaiting human review.
//   - skip: a work already carrying a Bangumi anchor (idempotency); any
//     ambiguous match (a work hitting several subjects, or a subject hit by
//     several works).
//
// Idempotent: a second --apply writes zero (the works it anchored are now
// already-anchored and skipped; InsertRefIfAbsent's ON CONFLICT DO NOTHING is
// the write-time backstop). Dry-run is the DEFAULT — it reports the tier plan +
// samples and writes nothing.
package doujinbangumi

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"

	"api/internal/platform/catalog/model"
	"api/internal/platform/catalog/repository"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

// matched_by rule tags — every graded assertion stays traceable/revocable.
const (
	ruleTitleYear = "rule:bgm-title-year" // exact: title + corroborating year
	ruleTitleOnly = "rule:bgm-title-only" // probable: title only, year absent/disagrees
)

// maxSamples caps how many per-tier example matches a run collects for logging.
const maxSamples = 8

// Opts configures a run.
type Opts struct {
	// Apply=false is a dry-run forecast (no writes). DSN is REQUIRED and never
	// defaulted — a bare run cannot touch a live DB (the 55 discipline). Limit
	// caps the candidate works processed for writing (0 = all); a debugging aid
	// that never changes the bidirectional-uniqueness graph, which is always
	// computed over every candidate.
	Apply bool
	DSN   string
	Limit int
}

// Sample is one example match for dry-run logging / test assertions.
type Sample struct {
	WorkID      int64
	WorkName    string
	SubjectID   int64
	SubjectName string
	WorkYear    int
	SubjYear    int
}

// Stats reports a run's outcome. Exact/Probable are the DECIDED plan (identical
// in dry and apply); *Written are the rows actually inserted (apply only);
// Already counts InsertRefIfAbsent conflict no-ops (the backstop firing).
type Stats struct {
	CandidateWorks   int
	Type4Subjects    int
	AlreadyAnchored  int // skipped: work already carries a Bangumi anchor
	Matched          int // works with ≥1 title-equal subject (post idempotency skip)
	AmbiguousWork    int // skipped: work hit several subjects
	AmbiguousSubject int // skipped: subject hit by several works (squatting guard)
	Exact            int // decided exact
	Probable         int // decided probable
	ExactWritten     int // exact rows inserted (apply)
	ProbableWritten  int // probable rows inserted (apply)
	Already          int // InsertRefIfAbsent said the row already existed

	ExactSamples    []Sample
	ProbableSamples []Sample
}

// Run reconciles the candidate set and, in apply mode, writes the graded
// anchors. Returns a loggable Stats. DSN is opened read-only in dry-run (no
// writes are issued) and read-write in apply.
func Run(ctx context.Context, opts Opts) (*Stats, error) {
	if opts.DSN == "" {
		return nil, fmt.Errorf("catalog DSN is required (--dsn); refusing to guess — pass the rehearsal copy locally, the live catalog only in the acceptance run")
	}
	db, err := openGorm(opts.DSN)
	if err != nil {
		return nil, fmt.Errorf("connect catalog db: %w", err)
	}
	if sqlDB, e := db.DB(); e == nil {
		defer sqlDB.Close()
	}

	reg, err := resolveRegistry(ctx, db)
	if err != nil {
		return nil, err
	}
	cands, err := loadCandidateTitles(ctx, db, reg)
	if err != nil {
		return nil, fmt.Errorf("load candidate titles: %w", err)
	}
	subjects, err := loadType4Subjects(ctx, db)
	if err != nil {
		return nil, fmt.Errorf("load type=4 subjects: %w", err)
	}
	workYears, err := loadWorkYears(ctx, db, reg)
	if err != nil {
		return nil, fmt.Errorf("load work years: %w", err)
	}
	anchored, err := loadAnchoredWorks(ctx, db, reg)
	if err != nil {
		return nil, fmt.Errorf("load already-anchored works: %w", err)
	}

	g := buildGraph(cands, subjects)
	stats := &Stats{CandidateWorks: len(g.workOrder), Type4Subjects: len(subjects)}

	if err := g.decide(ctx, db, reg, opts, workYears, anchored, stats); err != nil {
		return stats, err
	}

	slog.Info("doujin-bangumi reconcile",
		"apply", opts.Apply, "candidate_works", stats.CandidateWorks, "type4_subjects", stats.Type4Subjects,
		"already_anchored", stats.AlreadyAnchored, "matched", stats.Matched,
		"ambiguous_work", stats.AmbiguousWork, "ambiguous_subject", stats.AmbiguousSubject,
		"exact", stats.Exact, "probable", stats.Probable,
		"exact_written", stats.ExactWritten, "probable_written", stats.ProbableWritten, "already", stats.Already)
	logSamples("exact", stats.ExactSamples)
	logSamples("probable", stats.ProbableSamples)
	return stats, nil
}

// graph is the match graph over ALL candidate works: each work's distinct
// matched subject ids, and each subject's distinct matching-work count (the
// reverse direction of the bidirectional-uniqueness test). Built once over the
// full candidate set so --limit never distorts the uniqueness guards.
type graph struct {
	workOrder     []int64          // candidate work ids, ascending
	workName      map[int64]string // work id → display name (samples)
	workSubjects  map[int64]map[int64]struct{}
	subjWorkCount map[int64]int
	subjName      map[int64]string
	subjYear      map[int64]int
}

// buildGraph indexes the RHS by normalized name (both name_norm and
// name_cn_norm, ≥4 runes) and walks every candidate work, unioning the subject
// ids its titles hit and tallying the reverse per-subject work count.
func buildGraph(cands []candTitle, subjects []subjectRow) *graph {
	rhs := make(map[string][]int64, len(subjects))
	subjName := make(map[int64]string, len(subjects))
	subjYear := make(map[int64]int, len(subjects))
	for _, s := range subjects {
		subjName[s.ID] = s.Name
		subjYear[s.ID] = s.Year
		// A ≥4-char title can only equal a ≥4-char norm; the guard just avoids
		// indexing the empty/short norms that could never match anyway.
		if runeLen(s.NameNorm) >= minTitleLen {
			rhs[s.NameNorm] = append(rhs[s.NameNorm], s.ID)
		}
		if s.NameCNNorm != s.NameNorm && runeLen(s.NameCNNorm) >= minTitleLen {
			rhs[s.NameCNNorm] = append(rhs[s.NameCNNorm], s.ID)
		}
	}

	g := &graph{
		workName:      make(map[int64]string),
		workSubjects:  make(map[int64]map[int64]struct{}),
		subjWorkCount: make(map[int64]int),
		subjName:      subjName,
		subjYear:      subjYear,
	}
	titlesByWork := make(map[int64][]string)
	seen := make(map[int64]bool)
	for _, c := range cands {
		if !seen[c.WorkID] {
			seen[c.WorkID] = true
			g.workOrder = append(g.workOrder, c.WorkID)
		}
		g.workName[c.WorkID] = c.DisplayName
		titlesByWork[c.WorkID] = append(titlesByWork[c.WorkID], c.TitleNorm)
	}
	for _, wid := range g.workOrder {
		set := make(map[int64]struct{})
		for _, tn := range titlesByWork[wid] {
			for _, sid := range rhs[tn] {
				set[sid] = struct{}{}
			}
		}
		if len(set) == 0 {
			continue
		}
		g.workSubjects[wid] = set
		for sid := range set {
			g.subjWorkCount[sid]++
		}
	}
	return g
}

// decide grades every candidate work and, in apply mode, writes the anchor.
func (g *graph) decide(ctx context.Context, db *gorm.DB, reg registry, opts Opts, workYears map[int64]int, anchored map[int64]struct{}, stats *Stats) error {
	processed := 0
	for _, wid := range g.workOrder {
		if opts.Limit > 0 && processed >= opts.Limit {
			break
		}
		processed++

		if _, ok := anchored[wid]; ok {
			stats.AlreadyAnchored++ // idempotency: never re-anchor a Bangumi-anchored work
			continue
		}
		set := g.workSubjects[wid]
		if len(set) == 0 {
			continue // no title match — silent absence
		}
		stats.Matched++
		if len(set) > 1 {
			stats.AmbiguousWork++ // work hit several subjects — never write a bad anchor
			continue
		}
		var sid int64
		for s := range set {
			sid = s
		}
		if g.subjWorkCount[sid] > 1 {
			stats.AmbiguousSubject++ // subject hit by several works — anti-squatting
			continue
		}

		// 1↔1 pair: grade by year corroboration.
		wy := workYears[wid] // 0 = no dated release
		by := g.subjYear[sid]
		exact := wy != 0 && by != 0 && absInt(wy-by) <= 1

		tier := model.LinkKindProbable
		rule := ruleTitleOnly
		if exact {
			tier = model.LinkKindExact
			rule = ruleTitleYear
			stats.Exact++
		} else {
			stats.Probable++
		}
		stats.collectSample(exact, Sample{
			WorkID: wid, WorkName: g.workName[wid], SubjectID: sid,
			SubjectName: g.subjName[sid], WorkYear: wy, SubjYear: by,
		})

		if !opts.Apply {
			continue
		}
		written, err := repository.InsertRefIfAbsent(db.WithContext(ctx), model.CatalogExternalRef{
			EntityType: model.EntityTypeWork,
			EntityID:   wid,
			SourceID:   reg.bangumiSource,
			ExternalID: strconv.FormatInt(sid, 10),
			LinkKind:   tier,
			MatchedBy:  rule,
		})
		if err != nil {
			return fmt.Errorf("insert ref work=%d subject=%d: %w", wid, sid, err)
		}
		switch {
		case !written:
			stats.Already++
		case exact:
			stats.ExactWritten++
		default:
			stats.ProbableWritten++
		}
	}
	return nil
}

func (s *Stats) collectSample(exact bool, sample Sample) {
	if exact {
		if len(s.ExactSamples) < maxSamples {
			s.ExactSamples = append(s.ExactSamples, sample)
		}
		return
	}
	if len(s.ProbableSamples) < maxSamples {
		s.ProbableSamples = append(s.ProbableSamples, sample)
	}
}

func logSamples(tier string, samples []Sample) {
	for _, s := range samples {
		slog.Info("doujin-bangumi sample", "tier", tier,
			"work_id", s.WorkID, "work", s.WorkName, "subject_id", s.SubjectID, "subject", s.SubjectName,
			"work_year", s.WorkYear, "subject_year", s.SubjYear)
	}
}

func openGorm(dsn string) (*gorm.DB, error) {
	return gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: gormlogger.Default.LogMode(gormlogger.Silent)})
}

func absInt(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// runeLen is the character length of s (Postgres length() semantics), the
// isomorphic counterpart to the SQL-side `length(title_norm) >= 4` filter.
func runeLen(s string) int {
	n := 0
	for range s {
		n++
	}
	return n
}
