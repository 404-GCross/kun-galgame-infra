package main

import (
	"fmt"
	"io"
)

// SourceReport is the per-section tally. The dry-run numbers must reconcile with
// the reconnaissance baseline (rating 144 / website 70 / toolset 59 locally);
// any delta is explained in the execution report.
type SourceReport struct {
	Name            string
	EntitiesTotal   int // distinct anchor entities with >=1 comment
	ThreadsToCreate int
	ThreadsExisting int
	PostsToInsert   int
	PostsExisting   int // already mapped (or reconciled from a crashed run)
	MapRowsToWrite  int

	// Anomalies.
	DanglingParents int // tree parent pointer missing -> degraded to root
	SelfTargets     int // rating self-directed target_user_id (expected ~30)
	OverLenRows     int // content over the section ceiling (imported verbatim)
	MaxRunes        int // the section ceiling used for OverLenRows
}

// Report is the running tally printed at the end of both a dry run and an apply.
// Trust seeding is global (one row per user across all three sections; the PK is
// the global user id), so it lives outside the per-source breakdown.
type Report struct {
	Sources          []*SourceReport
	TrustSeedAbsent  int // authors with no community_trust row -> inserted
	TrustSeedPresent int // authors already holding a trust row -> untouched
}

func (r *Report) source(name string) *SourceReport {
	for _, s := range r.Sources {
		if s.Name == name {
			return s
		}
	}
	s := &SourceReport{Name: name}
	r.Sources = append(r.Sources, s)
	return s
}

func (r *Report) print(w io.Writer, apply bool) {
	mode := "DRY RUN (no writes)"
	if apply {
		mode = "APPLY"
	}
	fmt.Fprintf(w, "\n============== import-kungal-resource-comments · %s ==============\n", mode)
	for _, s := range r.Sources {
		fmt.Fprintf(w, "── %s ──\n", s.Name)
		fmt.Fprintf(w, "  entities (distinct anchor with >=1 comment) : %d\n", s.EntitiesTotal)
		fmt.Fprintf(w, "  threads to create / already present         : %d / %d\n", s.ThreadsToCreate, s.ThreadsExisting)
		fmt.Fprintf(w, "  posts   to insert / already present         : %d / %d\n", s.PostsToInsert, s.PostsExisting)
		fmt.Fprintf(w, "  map rows to write                           : %d\n", s.MapRowsToWrite)
		fmt.Fprintf(w, "  anomalies: dangling=%d self-target=%d over-%drunes=%d\n",
			s.DanglingParents, s.SelfTargets, s.MaxRunes, s.OverLenRows)
	}
	fmt.Fprintf(w, "── trust (global, all sources) ──\n")
	fmt.Fprintf(w, "  authors to seed (absent) / already present  : %d / %d\n", r.TrustSeedAbsent, r.TrustSeedPresent)
	fmt.Fprintf(w, "=================================================================\n")
}
