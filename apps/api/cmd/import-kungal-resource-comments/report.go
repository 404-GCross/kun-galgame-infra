package main

import (
	"fmt"
	"io"
)

type SourceReport struct {
	Name            string
	EntitiesTotal   int
	ThreadsToCreate int
	ThreadsExisting int
	PostsToInsert   int
	PostsExisting   int
	MapRowsToWrite  int

	DanglingParents int
	SelfTargets     int
	OverLenRows     int
	MaxRunes        int
}

type Report struct {
	Sources          []*SourceReport
	TrustSeedAbsent  int
	TrustSeedPresent int
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
