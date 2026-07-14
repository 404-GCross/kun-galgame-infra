package main

import (
	"fmt"
	"io"
)

// Report is the running tally printed at the end of both a dry run and an apply.
// The dry-run numbers must reconcile with the reconnaissance baseline (9,396
// comments / 2,312 games / 1,225 likes); any delta is explained in the report.
type Report struct {
	GamesTotal      int
	ThreadsToCreate int
	ThreadsExisting int
	PostsToInsert   int
	PostsExisting   int // already mapped (or reconciled from a crashed run)

	ReactionsToInsert int
	ReactionsExisting int // (post, user) reaction already present -> conflict-skipped
	ReactionsSkipped  int // like whose target post is absent (should be 0)

	TrustSeedAbsent  int // authors with no community_trust row -> inserted
	TrustSeedPresent int // authors already holding a trust row -> untouched

	MapRowsToWrite int

	// Anomalies (charter: dangling parent, orphan like, source status=1, over-length).
	DanglingParents int
	OrphanLikes     int
	Status1Rows     int
	OverLenRows     int
}

func (r *Report) print(w io.Writer, apply bool) {
	mode := "DRY RUN (no writes)"
	if apply {
		mode = "APPLY"
	}
	fmt.Fprintf(w, "\n==================== import-kungal-comments · %s ====================\n", mode)
	fmt.Fprintf(w, "games (distinct galgame_id with >=1 comment) : %d\n", r.GamesTotal)
	fmt.Fprintf(w, "threads   to create / already present        : %d / %d\n", r.ThreadsToCreate, r.ThreadsExisting)
	fmt.Fprintf(w, "posts     to insert / already present        : %d / %d\n", r.PostsToInsert, r.PostsExisting)
	fmt.Fprintf(w, "reactions to insert / present / orphan       : %d / %d / %d\n", r.ReactionsToInsert, r.ReactionsExisting, r.ReactionsSkipped)
	fmt.Fprintf(w, "trust     to seed (absent) / already present : %d / %d\n", r.TrustSeedAbsent, r.TrustSeedPresent)
	fmt.Fprintf(w, "map rows  to write                           : %d\n", r.MapRowsToWrite)
	fmt.Fprintf(w, "-------------------- anomalies --------------------\n")
	fmt.Fprintf(w, "dangling parent pointers (degraded to root)  : %d\n", r.DanglingParents)
	fmt.Fprintf(w, "orphan likes (target post missing)           : %d\n", r.OrphanLikes)
	fmt.Fprintf(w, "source status=1 rows (imported as hidden)    : %d\n", r.Status1Rows)
	fmt.Fprintf(w, "content over %d runes (imported verbatim)   : %d\n", maxContentRunes, r.OverLenRows)
	fmt.Fprintf(w, "=====================================================================\n")
}
