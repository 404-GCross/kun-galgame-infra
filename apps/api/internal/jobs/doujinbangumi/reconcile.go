package doujinbangumi

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"

	"api/internal/infrastructure/database"
	"api/internal/platform/catalog/model"
	"api/internal/platform/catalog/repository"

	"gorm.io/gorm"
)

const (
	ruleTitleYear = "rule:bgm-title-year"
	ruleTitleOnly = "rule:bgm-title-only"
)

const maxSamples = 8

type Opts struct {
	Apply bool
	DSN   string
	Limit int
}

type Sample struct {
	WorkID      int64
	WorkName    string
	SubjectID   int64
	SubjectName string
	WorkYear    int
	SubjYear    int
}

type Stats struct {
	CandidateWorks   int
	Type4Subjects    int
	AlreadyAnchored  int
	Matched          int
	AmbiguousWork    int
	AmbiguousSubject int
	Exact            int
	Probable         int
	ExactWritten     int
	ProbableWritten  int
	Already          int

	ExactSamples    []Sample
	ProbableSamples []Sample
}

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

type graph struct {
	workOrder     []int64
	workName      map[int64]string
	workSubjects  map[int64]map[int64]struct{}
	subjWorkCount map[int64]int
	subjName      map[int64]string
	subjYear      map[int64]int
}

func buildGraph(cands []candTitle, subjects []subjectRow) *graph {
	rhs := make(map[string][]int64, len(subjects))
	subjName := make(map[int64]string, len(subjects))
	subjYear := make(map[int64]int, len(subjects))
	for _, s := range subjects {
		subjName[s.ID] = s.Name
		subjYear[s.ID] = s.Year
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

func (g *graph) decide(ctx context.Context, db *gorm.DB, reg registry, opts Opts, workYears map[int64]int, anchored map[int64]struct{}, stats *Stats) error {
	processed := 0
	var touched []int64
	for _, wid := range g.workOrder {
		if opts.Limit > 0 && processed >= opts.Limit {
			break
		}
		processed++

		if _, ok := anchored[wid]; ok {
			stats.AlreadyAnchored++
			continue
		}
		set := g.workSubjects[wid]
		if len(set) == 0 {
			continue
		}
		stats.Matched++
		if len(set) > 1 {
			stats.AmbiguousWork++
			continue
		}
		var sid int64
		for s := range set {
			sid = s
		}
		if g.subjWorkCount[sid] > 1 {
			stats.AmbiguousSubject++
			continue
		}

		wy := workYears[wid]
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
			touched = append(touched, wid)
		default:
			stats.ProbableWritten++
			touched = append(touched, wid)
		}
	}
	return repository.TouchWorks(ctx, db, touched)
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
	return database.OpenJob(dsn)
}

func absInt(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

func runeLen(s string) int {
	n := 0
	for range s {
		n++
	}
	return n
}
