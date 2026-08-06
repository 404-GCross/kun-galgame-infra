package service

import "math"

// Queue priority (doc 18 §4.2: "priority ≈ reach × severity ÷ cost").
//
// Severity is whatever the driving source knows — a reporter's reason severity,
// a forward's declared severity, a classifier's confidence — and each source
// scales its own into the same 0-5 band so the sources sort comparably. This
// file owns the OTHER factor: reach.
//
// Why reach belongs in the ordering at all: a violation seen by 3 people and one
// seen by 30,000 carry the same severity but not remotely the same urgency, and
// a strictly severity-ordered queue makes a reviewer work them in an order that
// has nothing to do with how much harm is still accruing. Reviewer time is the
// scarce resource in this system; reach is what tells you where to spend it.
const (
	// maxReachBoost caps the multiplier. Reach must be able to break ties and
	// lift a wide-blast-radius item over a narrow one — it must NOT be able to
	// let a popular-but-mild item outrank a severe one indefinitely, which is
	// what an uncapped multiplier eventually does. 4× ≈ one full severity band.
	maxReachBoost float32 = 4.0

	// reachDecades is how many powers of ten of reach it takes to earn one point
	// of boost. Reach spans orders of magnitude (3 views vs 300,000), so it is
	// read logarithmically: linear reach would let a single viral item dominate
	// the queue no matter what else was in it.
	reachDecades float64 = 2.0
)

// reachBoost turns a reach snapshot into a priority multiplier in [1, 4].
//
// A nil reach (the product does not report it — the pre-existing behaviour of
// every caller) and a zero reach both yield exactly 1.0, so this can only ever
// RAISE an item's priority. That one-way property is what makes it safe to ship
// ahead of the products that will populate the field: until they do, every
// priority in the inbox is byte-for-byte what it is today.
//
//	reach:      0     10    100   1k    10k   100k   1M+
//	multiplier: 1.00  1.52  2.00  2.50  3.00  3.50   4.00 (capped)
func reachBoost(reach *int64) float32 {
	if reach == nil || *reach <= 0 {
		return 1
	}
	// log10(1+reach) rather than log10(reach) so a reach of 1 scores 0 boost
	// instead of the negative-infinity a bare log would give.
	boost := 1 + float32(math.Log10(1+float64(*reach))/reachDecades)
	if boost > maxReachBoost {
		return maxReachBoost
	}
	return boost
}

// rankPriority combines a source's own 0-5 severity signal with the reach
// snapshot. Every source that opens a review item goes through here, so the
// inbox has exactly ONE ordering model rather than one per source.
func rankPriority(base float32, reach *int64) float32 {
	return base * reachBoost(reach)
}

// repriceForReach re-ranks an ALREADY OPEN item whose subject has since reached
// a wider audience — the case that matters most in practice, because a post
// forwarded at 50 views and sitting in the queue while it climbs to 50,000 is
// exactly the item a reviewer most needs pulled to the top.
//
// It recovers the source's base severity by dividing the current priority by the
// boost already baked into it, then re-multiplies by the new one. That works for
// ANY source without knowing which one opened the item, because every path
// produces priority as base × boost. Division is safe: reachBoost is never < 1.
//
// It only ever returns a value ≥ current. A reach snapshot that arrives lower
// than the one on file (products may report approximations, and caches go
// backwards) must not demote an item a reviewer has already seen rise.
func repriceForReach(current float32, oldReach, newReach *int64) float32 {
	repriced := (current / reachBoost(oldReach)) * reachBoost(newReach)
	if repriced < current {
		return current
	}
	return repriced
}

// maxReach returns the larger of two reach snapshots, treating nil as "unknown"
// rather than zero — so a product that reports reach once and then stops does
// not silently reset the item's ranking.
func maxReach(a, b *int64) *int64 {
	if a == nil {
		return b
	}
	if b == nil || *a >= *b {
		return a
	}
	return b
}
