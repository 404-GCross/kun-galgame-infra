package tagsafety

import (
	"context"
	"fmt"
	"log/slog"
	"sort"

	"api/internal/platform/catalog/model"

	"gorm.io/gorm"
)

// ApplyOpts configures the apply phase: verdict JSONL (+ optional hand-ruled
// lines) → planned writes. Dry by default.
type ApplyOpts struct {
	// DSN is REQUIRED even for a dry run: bucketing needs the canonical
	// vocabulary and the source map to decide whether a name is even writable,
	// so a dry run that skipped the DB would report a fantasy plan.
	DSN string
	// In is the classify verdict JSONL (REQUIRED).
	In string
	// Reviewed is an OPTIONAL hand-ruled JSONL ({source,name,class}) applied at
	// FULL trust — it overrides the model's verdict for the same (source,name)
	// and can introduce names the model never judged.
	Reviewed string
	// ReviewOut receives the lines a human must adjudicate (below threshold, and
	// every de-flag candidate).
	ReviewOut string
	// MinConfidence is the auto-apply floor (default 0.90).
	MinConfidence float64
	// Limit caps the number of write operations (0 = no cap) — the rehearsal
	// limiter every apply tool in this repo carries.
	Limit int
	// Run switches from DRY to writing.
	Run bool
}

// SourceName is one (source key, verbatim name) write target in catalog_work_tag.
type SourceName struct {
	Source string
	Name   string
}

// VocabRow is the current state of one canonical catalog_tag row — the state the
// planner needs to make a write a no-op instead of a churn UPDATE, and to fire
// the de-flag guard.
type VocabRow struct {
	ID     int64
	Sexual bool
	Tier   int16
}

// CatalogState is the DB-side context the planner reads. It is a plain value
// with no gorm handle, which is what makes the entire bucketing logic (the part
// that decides what gets written) provable in tests with NO database.
type CatalogState struct {
	// Vocab is canonical name → row.
	Vocab map[string]VocabRow
	// Mapped is verdictKey(sourceKey, sourceName) → canonical name, from
	// catalog_tag_source_map. An unmapped folksonomy name has no canonical row
	// and therefore no vocabulary write.
	Mapped map[string]string
}

// Plan is the decided (but not yet executed) set of writes. Every list is
// deduped and sorted, so a dry run and the following --run agree exactly, and
// two identical applies plan the same thing.
type Plan struct {
	// WorkTagSexual sets catalog_work_tag.sexual = true for every row of that
	// (source, verbatim name). This is the axis' real payload — it is what the
	// per-work read face projects.
	WorkTagSexual []SourceName
	// VocabSexual sets catalog_tag.sexual = true for these canonical names.
	VocabSexual []string
	// VocabHidden sets catalog_tag.tier = hidden for these canonical names.
	// Rows are NEVER deleted: a hidden canonical still maps its source names, so
	// the folksonomy stays reversible.
	VocabHidden []string
	// Review is everything a human must rule on.
	Review []Verdict
	// Counts is the report-mode breakdown.
	Counts PlanCounts
	// Truncated records that --limit cut the write lists short.
	Truncated bool
}

// PlanCounts is the per-class / per-bucket census (report mode, and the dry-run
// header).
type PlanCounts struct {
	Total          int
	ByClass        map[Class]int // every verdict, regardless of confidence
	Confident      map[Class]int // confidence ≥ threshold (or hand-ruled)
	BelowThreshold int
	Reviewed       int // hand-ruled lines consumed at full trust
	DeflagGuard    int // normal verdicts on a currently-sexual canonical → review, never auto-flipped
	UnmappedJunk   int // junk with no canonical row — nothing to hide, nothing to write
	AlreadySexual  int // canonical already sexual=true — no-op
	AlreadyHidden  int // canonical already tier=hidden — no-op
	// Buckets is the confidence histogram (0.0-0.5 / 0.5-0.7 / 0.7-0.9 / 0.9-1.0).
	Buckets map[string]int
}

// ApplyStats reports an apply run: the plan sizes (identical in dry and run)
// plus the rows actually touched (run only).
type ApplyStats struct {
	Plan             Plan
	WorkTagRows      int64 // catalog_work_tag rows flipped to sexual=true
	VocabSexualRows  int64
	VocabHiddenRows  int64
	Errors           int
	ReviewFileWrites int
}

// Apply loads the verdicts, builds the plan against the live catalog state and
// (with --run) executes it. Dry by default — a dry run decides EVERYTHING and
// writes only the review file.
func Apply(ctx context.Context, opts ApplyOpts) (*ApplyStats, error) {
	if opts.DSN == "" {
		return nil, fmt.Errorf("catalog DSN is required (--dsn)")
	}
	if opts.In == "" {
		return nil, fmt.Errorf("verdict JSONL is required (--in)")
	}
	if opts.MinConfidence <= 0 {
		opts.MinConfidence = 0.90
	}
	verdicts, err := readVerdicts(opts.In)
	if err != nil {
		return nil, fmt.Errorf("read verdicts: %w", err)
	}
	var reviewed []ReviewedLine
	if opts.Reviewed != "" {
		if reviewed, err = readReviewed(opts.Reviewed); err != nil {
			return nil, fmt.Errorf("read reviewed: %w", err)
		}
	}

	db, err := openGorm(opts.DSN)
	if err != nil {
		return nil, fmt.Errorf("connect catalog db: %w", err)
	}
	if sqlDB, e := db.DB(); e == nil {
		defer sqlDB.Close()
	}
	sourceIDs, err := resolveSources(ctx, db, verdictSourceKeys(verdicts, reviewed))
	if err != nil {
		return nil, err
	}
	state, err := loadCatalogState(ctx, db, sourceIDs)
	if err != nil {
		return nil, err
	}

	plan := BuildPlan(verdicts, reviewed, state, PlanOpts{MinConfidence: opts.MinConfidence, Limit: opts.Limit})
	st := &ApplyStats{Plan: plan}

	if opts.ReviewOut != "" {
		if err := writeVerdicts(opts.ReviewOut, plan.Review); err != nil {
			return nil, fmt.Errorf("write review file: %w", err)
		}
		st.ReviewFileWrites = len(plan.Review)
	}
	if err := executePlan(ctx, &gormWriter{db: db, sources: sourceIDs}, plan, opts.Run, st); err != nil {
		return st, err
	}
	slog.Info("tag-safety apply done", "work_tag_rows", st.WorkTagRows,
		"vocab_sexual_rows", st.VocabSexualRows, "vocab_hidden_rows", st.VocabHiddenRows,
		"errors", st.Errors)
	return st, nil
}

// PlanOpts are the pure-planning knobs.
type PlanOpts struct {
	MinConfidence float64
	Limit         int
}

// BuildPlan is the whole bucketing decision, expressed as a pure function of the
// verdicts, the hand-ruled overrides and a snapshot of the catalog state. Rules:
//
//   - a hand-ruled line (--reviewed) REPLACES the model verdict for the same
//     (source, name) and applies at full trust; it may also introduce a name the
//     model never judged;
//   - confidence < threshold → the review file, no write;
//   - sexual + confident → catalog_work_tag.sexual for that source's rows, and
//     the mapped canonical's catalog_tag.sexual;
//   - junk + confident → catalog_tag.tier = hidden, MAPPED names only (an
//     unmapped junk name is not displayed downstream, so there is nothing to
//     write and nothing to delete);
//   - normal + confident on a canonical that is CURRENTLY sexual=true → the
//     review file, never an auto-flip. De-flagging is human-only: a false
//     negative here un-hides adult content site-wide, which is the asymmetric
//     failure this whole wave exists to avoid.
func BuildPlan(verdicts []Verdict, reviewed []ReviewedLine, state CatalogState, opts PlanOpts) Plan {
	if opts.MinConfidence <= 0 {
		opts.MinConfidence = 0.90
	}
	plan := Plan{Counts: PlanCounts{
		ByClass:   map[Class]int{},
		Confident: map[Class]int{},
		Buckets:   map[string]int{},
	}}

	effective := mergeReviewed(verdicts, reviewed, &plan.Counts)

	workSeen := map[string]struct{}{}
	vocabSexSeen := map[string]struct{}{}
	vocabHidSeen := map[string]struct{}{}

	for _, v := range effective {
		cls := Class(v.Class)
		plan.Counts.Total++
		plan.Counts.ByClass[cls]++
		plan.Counts.Buckets[confidenceBucket(v.Confidence)]++

		if v.Confidence < opts.MinConfidence {
			plan.Counts.BelowThreshold++
			plan.Review = append(plan.Review, withNote(v, "below-confidence"))
			continue
		}
		plan.Counts.Confident[cls]++

		canonical, hasCanonical := canonicalName(v, state)
		row := state.Vocab[canonical]

		switch cls {
		case ClassSexual:
			if v.Source != VocabSource {
				if _, dup := workSeen[v.key()]; !dup {
					workSeen[v.key()] = struct{}{}
					plan.WorkTagSexual = append(plan.WorkTagSexual, SourceName{Source: v.Source, Name: v.Name})
				}
			}
			if hasCanonical {
				switch {
				case row.Sexual:
					plan.Counts.AlreadySexual++
				default:
					if _, dup := vocabSexSeen[canonical]; !dup {
						vocabSexSeen[canonical] = struct{}{}
						plan.VocabSexual = append(plan.VocabSexual, canonical)
					}
				}
			}
		case ClassJunk:
			if !hasCanonical {
				plan.Counts.UnmappedJunk++
				continue
			}
			if row.Tier == model.TagTierHidden {
				plan.Counts.AlreadyHidden++
				continue
			}
			if _, dup := vocabHidSeen[canonical]; !dup {
				vocabHidSeen[canonical] = struct{}{}
				plan.VocabHidden = append(plan.VocabHidden, canonical)
			}
		case ClassNormal:
			// The de-flag guard. A confident "normal" over a canonical currently
			// flagged sexual is exactly the case a human must rule on.
			if hasCanonical && row.Sexual {
				plan.Counts.DeflagGuard++
				plan.Review = append(plan.Review, withNote(v, "deflag-candidate: catalog_tag.sexual=true but classified normal — human ruling required"))
			}
		}
	}

	sort.Slice(plan.WorkTagSexual, func(i, j int) bool {
		if plan.WorkTagSexual[i].Source != plan.WorkTagSexual[j].Source {
			return plan.WorkTagSexual[i].Source < plan.WorkTagSexual[j].Source
		}
		return plan.WorkTagSexual[i].Name < plan.WorkTagSexual[j].Name
	})
	sort.Strings(plan.VocabSexual)
	sort.Strings(plan.VocabHidden)
	sort.Slice(plan.Review, func(i, j int) bool {
		if plan.Review[i].Source != plan.Review[j].Source {
			return plan.Review[i].Source < plan.Review[j].Source
		}
		return plan.Review[i].Name < plan.Review[j].Name
	})

	applyLimit(&plan, opts.Limit)
	return plan
}

// applyLimit caps the TOTAL number of write operations, walking the lists in a
// fixed order (work-tag → vocab sexual → vocab hidden) so a --limit rehearsal is
// a deterministic prefix of the full plan rather than an arbitrary sample. The
// review file is never truncated: a limiter must not hide work from the human.
func applyLimit(plan *Plan, limit int) {
	if limit <= 0 {
		return
	}
	budget := limit
	take := func(n int) int {
		if n > budget {
			n = budget
		}
		budget -= n
		return n
	}
	total := len(plan.WorkTagSexual) + len(plan.VocabSexual) + len(plan.VocabHidden)
	plan.WorkTagSexual = plan.WorkTagSexual[:take(len(plan.WorkTagSexual))]
	plan.VocabSexual = plan.VocabSexual[:take(len(plan.VocabSexual))]
	plan.VocabHidden = plan.VocabHidden[:take(len(plan.VocabHidden))]
	plan.Truncated = total > limit
}

// mergeReviewed folds the hand-ruled lines over the model verdicts. A ruled
// (source,name) takes the human's class at confidence 1.0; a ruled name with no
// model verdict is appended. Input order is preserved for the model verdicts so
// the plan is stable.
func mergeReviewed(verdicts []Verdict, reviewed []ReviewedLine, counts *PlanCounts) []Verdict {
	byKey := make(map[string]ReviewedLine, len(reviewed))
	for _, r := range reviewed {
		byKey[verdictKey(r.Source, r.Name)] = r
	}
	out := make([]Verdict, 0, len(verdicts)+len(reviewed))
	used := make(map[string]struct{}, len(reviewed))
	for _, v := range verdicts {
		if r, ok := byKey[v.key()]; ok {
			used[v.key()] = struct{}{}
			counts.Reviewed++
			v.Class = r.Class
			v.Confidence = 1
			v.Reason = firstNonEmpty(r.Reason, v.Reason)
			v.Model = "human"
		}
		out = append(out, v)
	}
	for _, r := range reviewed {
		k := verdictKey(r.Source, r.Name)
		if _, hit := used[k]; hit {
			continue
		}
		counts.Reviewed++
		out = append(out, Verdict{Source: r.Source, Name: r.Name, Class: r.Class, Confidence: 1, Reason: r.Reason, Model: "human"})
	}
	return out
}

// canonicalName resolves a verdict onto its catalog_tag row: a VocabSource
// verdict names the canonical directly, a folksonomy verdict goes through
// catalog_tag_source_map. A name that resolves to nothing has no vocabulary
// write.
func canonicalName(v Verdict, state CatalogState) (string, bool) {
	name := v.Name
	if v.Source != VocabSource {
		mapped, ok := state.Mapped[v.key()]
		if !ok {
			return "", false
		}
		name = mapped
	}
	if _, ok := state.Vocab[name]; !ok {
		return "", false
	}
	return name, true
}

func withNote(v Verdict, note string) Verdict {
	v.Note = note
	return v
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// confidenceBucket labels a confidence for the report histogram.
func confidenceBucket(c float64) string {
	switch {
	case c >= 0.90:
		return "0.90-1.00"
	case c >= 0.70:
		return "0.70-0.90"
	case c >= 0.50:
		return "0.50-0.70"
	default:
		return "0.00-0.50"
	}
}

// verdictSourceKeys collects the distinct REGISTRY source keys referenced by the
// input (VocabSource excluded — it is not a registry key), so apply resolves
// exactly the ids it needs.
func verdictSourceKeys(verdicts []Verdict, reviewed []ReviewedLine) []string {
	seen := map[string]struct{}{}
	add := func(s string) {
		if s != "" && s != VocabSource {
			seen[s] = struct{}{}
		}
	}
	for _, v := range verdicts {
		add(v.Source)
	}
	for _, r := range reviewed {
		add(r.Source)
	}
	out := make([]string, 0, len(seen))
	for s := range seen {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

// ── execution ────────────────────────────────────────────────────────────────

// writer is the seam the plan executes through, so the dry/run gating and the
// per-bucket write routing are provable with a recording fake and NO database.
type writer interface {
	setWorkTagSexual(ctx context.Context, source, name string) (int64, error)
	setTagSexual(ctx context.Context, name string) (int64, error)
	setTagHidden(ctx context.Context, name string) (int64, error)
}

// executePlan is the SINGLE dry/run gate: with run=false it touches the writer
// zero times, which is what makes "dry decides everything, writes nothing" a
// property a test can prove rather than a claim. Past that gate it counts rows
// and records (never aborts on) per-item errors — one unwritable name must not
// strand the rest of a long apply.
func executePlan(ctx context.Context, w writer, plan Plan, run bool, st *ApplyStats) error {
	if !run {
		slog.Info("tag-safety apply (dry)", "work_tag_sexual", len(plan.WorkTagSexual),
			"vocab_sexual", len(plan.VocabSexual), "vocab_hidden", len(plan.VocabHidden),
			"review", len(plan.Review), "truncated", plan.Truncated)
		return nil
	}
	for _, t := range plan.WorkTagSexual {
		n, err := w.setWorkTagSexual(ctx, t.Source, t.Name)
		if err != nil {
			st.Errors++
			slog.Warn("set work tag sexual", "source", t.Source, "name", t.Name, "err", err)
			continue
		}
		st.WorkTagRows += n
	}
	for _, name := range plan.VocabSexual {
		n, err := w.setTagSexual(ctx, name)
		if err != nil {
			st.Errors++
			slog.Warn("set canonical sexual", "name", name, "err", err)
			continue
		}
		st.VocabSexualRows += n
	}
	for _, name := range plan.VocabHidden {
		n, err := w.setTagHidden(ctx, name)
		if err != nil {
			st.Errors++
			slog.Warn("set canonical hidden", "name", name, "err", err)
			continue
		}
		st.VocabHiddenRows += n
	}
	return nil
}

// gormWriter is the live implementation. Every statement carries its own
// "already there" predicate (sexual = false / tier <> hidden) so a second
// identical apply writes ZERO rows — the idempotence guarantee, and the reason
// the reported row counts are a real measure of what changed.
type gormWriter struct {
	db      *gorm.DB
	sources map[string]int16
}

func (g *gormWriter) setWorkTagSexual(ctx context.Context, source, name string) (int64, error) {
	id, ok := g.sources[source]
	if !ok {
		return 0, fmt.Errorf("source %q not resolved", source)
	}
	res := g.db.WithContext(ctx).Exec(
		`UPDATE catalog_work_tag SET sexual = true WHERE source_id = ? AND name = ? AND sexual = false`, id, name)
	return res.RowsAffected, res.Error
}

func (g *gormWriter) setTagSexual(ctx context.Context, name string) (int64, error) {
	res := g.db.WithContext(ctx).Exec(
		`UPDATE catalog_tag SET sexual = true, updated_at = now() WHERE name = ? AND sexual = false`, name)
	return res.RowsAffected, res.Error
}

func (g *gormWriter) setTagHidden(ctx context.Context, name string) (int64, error) {
	res := g.db.WithContext(ctx).Exec(
		`UPDATE catalog_tag SET tier = ?, updated_at = now() WHERE name = ? AND tier <> ?`,
		model.TagTierHidden, name, model.TagTierHidden)
	return res.RowsAffected, res.Error
}

// loadCatalogState snapshots the canonical vocabulary and the source map for the
// requested sources.
func loadCatalogState(ctx context.Context, db *gorm.DB, sources map[string]int16) (CatalogState, error) {
	st := CatalogState{Vocab: map[string]VocabRow{}, Mapped: map[string]string{}}

	var tags []struct {
		ID     int64  `gorm:"column:id"`
		Name   string `gorm:"column:name"`
		Tier   int16  `gorm:"column:tier"`
		Sexual bool   `gorm:"column:sexual"`
	}
	if err := db.WithContext(ctx).Raw(`SELECT id, name, tier, sexual FROM catalog_tag`).Scan(&tags).Error; err != nil {
		return st, fmt.Errorf("load canonical vocabulary: %w", err)
	}
	byID := make(map[int64]string, len(tags))
	for _, t := range tags {
		st.Vocab[t.Name] = VocabRow{ID: t.ID, Sexual: t.Sexual, Tier: t.Tier}
		byID[t.ID] = t.Name
	}

	idToKey := make(map[int16]string, len(sources))
	for k, id := range sources {
		idToKey[id] = k
	}
	var maps []struct {
		SourceID   int16  `gorm:"column:source_id"`
		SourceName string `gorm:"column:source_name"`
		TagID      int64  `gorm:"column:tag_id"`
	}
	if err := db.WithContext(ctx).Raw(`SELECT source_id, source_name, tag_id FROM catalog_tag_source_map`).Scan(&maps).Error; err != nil {
		return st, fmt.Errorf("load tag source map: %w", err)
	}
	for _, m := range maps {
		key, ok := idToKey[m.SourceID]
		if !ok {
			continue // a source this run does not touch
		}
		if name, ok := byID[m.TagID]; ok {
			st.Mapped[verdictKey(key, m.SourceName)] = name
		}
	}
	return st, nil
}
