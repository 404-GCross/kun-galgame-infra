package service

import (
	"math"
	"testing"
)

func i64(v int64) *int64 { return &v }

func TestReachBoostIsOneWay(t *testing.T) {
	if got := reachBoost(nil); got != 1 {
		t.Fatalf("reachBoost(nil) = %v, want exactly 1", got)
	}
	if got := reachBoost(i64(0)); got != 1 {
		t.Fatalf("reachBoost(0) = %v, want exactly 1", got)
	}
	if got := reachBoost(i64(-500)); got != 1 {
		t.Fatalf("reachBoost(-500) = %v, want exactly 1", got)
	}
}

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
	for _, huge := range []int64{10_000_000, 1 << 40, math.MaxInt64} {
		if got := reachBoost(i64(huge)); got != maxReachBoost {
			t.Errorf("reachBoost(%d) = %v, want the %v cap", huge, got, maxReachBoost)
		}
	}
}

func TestReachNeverOutranksSeverity(t *testing.T) {
	viralMild := rankPriority(1, i64(math.MaxInt64))
	unseenSevere := rankPriority(5, nil)
	if viralMild >= unseenSevere {
		t.Fatalf("viral mild (%.2f) outranks unseen severe (%.2f); the cap is too loose",
			viralMild, unseenSevere)
	}
}

func TestRepriceForReachIsMonotonic(t *testing.T) {
	base := float32(3)
	at100 := rankPriority(base, i64(100))

	grown := repriceForReach(at100, i64(100), i64(100_000))
	if grown <= at100 {
		t.Fatalf("reprice 100 → 100k gave %.3f, want > %.3f", grown, at100)
	}
	direct := rankPriority(base, i64(100_000))
	if math.Abs(float64(grown-direct)) > 0.001 {
		t.Fatalf("repriced %.4f != directly-ranked %.4f", grown, direct)
	}

	shrunk := repriceForReach(grown, i64(100_000), i64(5))
	if shrunk != grown {
		t.Fatalf("a lower reach demoted the item: %.3f → %.3f", grown, shrunk)
	}
	fromNil := repriceForReach(base, nil, i64(10_000))
	if math.Abs(float64(fromNil-rankPriority(base, i64(10_000)))) > 0.001 {
		t.Fatalf("first reach on a nil-reach item = %.4f, want %.4f",
			fromNil, rankPriority(base, i64(10_000)))
	}
}

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
