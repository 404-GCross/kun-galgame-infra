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

// Scan pipeline tunables (step 03). The scoring worker mirrors the callback
// worker's ticker + FOR UPDATE SKIP LOCKED shape.
const (
	// maxScanTextRunes caps the stored/scanned text (anti-abuse). Excess is
	// truncated at intake and the truncation is recorded on the response.
	// Counted in runes (not bytes) so multibyte CJK text is not cut mid-glyph.
	maxScanTextRunes = 8000

	// scanBatchSize is how many pending rows one scoring pass claims.
	scanBatchSize = 20
	// scanInterval is the scoring worker tick (doc 18 §6 low-volume shadow cadence).
	scanInterval = 60 * time.Second
)

// termCacheTTL is how long the Tier0 matcher serves its in-memory snapshot of
// active terms before reloading from the DB (step 05). Admin create/deprecate
// invalidates the snapshot in-process immediately; across instances staleness
// is bounded by this TTL (accepted — doc 18 §6). The hot check path never
// touches the DB except on a refresh.
const termCacheTTL = 60 * time.Second
