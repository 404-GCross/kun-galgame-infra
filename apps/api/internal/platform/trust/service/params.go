package service

import "time"

// v0 tunables (章程 rulings 6/7/8). Constants for P0; a config/policy table
// arrives with the accuracy-feedback work in P1+.
const (
	// rateLimitWindow / rateLimitMax bound a reporter's report count (SQL
	// window count, no Redis — 章程 ruling 6).
	rateLimitWindow = time.Hour
	rateLimitMax    = 10

	// aggregateThreshold is the accumulated report weight that opens a review
	// item (§5.3). A staff single report bypasses it (章程 ruling 7).
	aggregateThreshold float32 = 3.0

	// foldWindow is the negative-knowledge window: a new report on a subject
	// dismissed within this window folds onto the old item, never reopening
	// (invariant 10, 章程 ruling 8).
	foldWindow = 30 * 24 * time.Hour

	// newAccountAge is the reputation cut-off: accounts younger than this weigh
	// half (章程 ruling 7).
	newAccountAge = 7 * 24 * time.Hour
)

// callbackBackoff is the exponential retry schedule between delivery attempts
// (章程 ruling 9): after the k-th failure wait callbackBackoff[k-1]; after
// callbackMaxAttempts failures the disposition is dead-lettered.
var callbackBackoff = []time.Duration{
	1 * time.Minute,
	5 * time.Minute,
	25 * time.Minute,
	2 * time.Hour,
	10 * time.Hour,
}

const callbackMaxAttempts = 5
