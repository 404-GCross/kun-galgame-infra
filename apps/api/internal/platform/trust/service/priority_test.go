package service

import (
	"math"
	"testing"
)

func i64(v int64) *int64 { return &v }

// TestReachBoostIsOneWay pins the property the whole feature rests on: reach can
// only ever RAISE a priority. An unreported reach (every caller today) and a
// zero reach must both be exactly 1.0, so shipping this ahead of the products
// that populate the field leaves the live queue's ordering untouched.
func TestReachBoostIsOneWay(t *testing.T) {
	if got := reachBoost(nil); got != 1 {
		t.Fatalf("reachBoost(nil) = %v, want exactly 1", got)
	}
	if got := reachBoost(i64(0)); got != 1 {
		t.Fatalf("reachBoost(0) = %v, want exactly 1", got)
	}
	// A negative reach is a product bug, not a demotion signal.
	if got := reachBoost(i64(-500)); got != 1 {
		t.Fatalf("reachBoost(-500) = %v, want exactly 1", got)
	}
}

// TestReachBoostBandsAndCap walks the documented curve and pins the ceiling. The
// cap is the load-bearing part: uncapped, a viral-but-mild item would eventually
// outrank every severe one in the queue.
func TestReachBoostBandsAndCap(t *testing.T) {
	for _, tc := range []struct {
		reach int64
		want  float32
	}{
		{1, 1.15}, {10, 1.52}, {100, 2.00}, {1_000, 2.50},
		{10_000, 3.00}, {100_000, 3.50}, {1_000_000, 4.00},
	} {
		got := reachBoost(i64(tc.reach))
		if math.Abs(float64(got-tc.want)) > 0.01 {
			t.Errorf("reachBoost(%d) = %.3f, want ~%.2f", tc.reach, got, tc.want)
		}
	}
	// Far past the cap: still exactly the cap, never more.
	for _, huge := range []int64{10_000_000, 1 << 40, math.MaxInt64} {
		if got := reachBoost(i64(huge)); got != maxReachBoost {
			t.Errorf("reachBoost(%d) = %v, want the %v cap", huge, got, maxReachBoost)
		}
	}
}

// TestReachNeverOutranksSeverity is the ordering guarantee stated in the cap's
// comment, asserted rather than assumed: the most-viral minimum-severity item
// must still sort below a maximum-severity item that nobody has seen.
func TestReachNeverOutranksSeverity(t *testing.T) {
	viralMild := rankPriority(1, i64(math.MaxInt64))
	unseenSevere := rankPriority(5, nil)
	if viralMild >= unseenSevere {
		t.Fatalf("viral mild (%.2f) outranks unseen severe (%.2f); the cap is too loose",
			viralMild, unseenSevere)
	}
}

// TestRepriceForReachIsMonotonic: an open item whose subject keeps accruing
// views climbs, and a reach snapshot that arrives LOWER than the one on file
// never demotes it — products report approximations and caches go backwards.
func TestRepriceForReachIsMonotonic(t *testing.T) {
	base := float32(3)
	at100 := rankPriority(base, i64(100))

	grown := repriceForReach(at100, i64(100), i64(100_000))
	if grown <= at100 {
		t.Fatalf("reprice 100 → 100k gave %.3f, want > %.3f", grown, at100)
	}
	// Recovering the base by dividing out the old boost must be exact enough that
	// repricing is equivalent to having opened at the new reach in the first place.
	direct := rankPriority(base, i64(100_000))
	if math.Abs(float64(grown-direct)) > 0.001 {
		t.Fatalf("repriced %.4f != directly-ranked %.4f", grown, direct)
	}

	shrunk := repriceForReach(grown, i64(100_000), i64(5))
	if shrunk != grown {
		t.Fatalf("a lower reach demoted the item: %.3f → %.3f", grown, shrunk)
	}
	// First-ever reach on an item opened without one.
	fromNil := repriceForReach(base, nil, i64(10_000))
	if math.Abs(float64(fromNil-rankPriority(base, i64(10_000)))) > 0.001 {
		t.Fatalf("first reach on a nil-reach item = %.4f, want %.4f",
			fromNil, rankPriority(base, i64(10_000)))
	}
}

// TestMaxReachTreatsNilAsUnknown: nil means "not reported", NOT zero. A product
// that reports reach once and then stops must not reset the item's ranking.
func TestMaxReachTreatsNilAsUnknown(t *testing.T) {
	if got := maxReach(nil, nil); got != nil {
		t.Fatalf("maxReach(nil,nil) = %v, want nil", got)
	}
	if got := maxReach(i64(700), nil); got == nil || *got != 700 {
		t.Fatalf("maxReach(700,nil) = %v, want 700 (nil must not erase a known reach)", got)
	}
	if got := maxReach(nil, i64(700)); got == nil || *got != 700 {
		t.Fatalf("maxReach(nil,700) = %v, want 700", got)
	}
	if got := maxReach(i64(900), i64(300)); got == nil || *got != 900 {
		t.Fatalf("maxReach(900,300) = %v, want 900", got)
	}
	if got := maxReach(i64(300), i64(900)); got == nil || *got != 900 {
		t.Fatalf("maxReach(300,900) = %v, want 900", got)
	}
}
