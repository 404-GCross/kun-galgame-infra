package service

import "math"

const (
	maxReachBoost float32 = 4.0

	reachDecades float64 = 2.0
)

func reachBoost(reach *int64) float32 {
	if reach == nil || *reach <= 0 {
		return 1
	}
	boost := 1 + float32(math.Log10(1+float64(*reach))/reachDecades)
	if boost > maxReachBoost {
		return maxReachBoost
	}
	return boost
}

func rankPriority(base float32, reach *int64) float32 {
	return base * reachBoost(reach)
}

func repriceForReach(current float32, oldReach, newReach *int64) float32 {
	repriced := (current / reachBoost(oldReach)) * reachBoost(newReach)
	if repriced < current {
		return current
	}
	return repriced
}

func maxReach(a, b *int64) *int64 {
	if a == nil {
		return b
	}
	if b == nil || *a >= *b {
		return a
	}
	return b
}
