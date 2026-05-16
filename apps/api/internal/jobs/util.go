package jobs

// chunk splits src into consecutive slices of at most n elements. Shared
// by jobs that batch work (e.g. reference-ping ≤1000 hashes/request).
func chunk[T any](src []T, n int) [][]T {
	if n < 1 {
		n = 1
	}
	out := make([][]T, 0, (len(src)+n-1)/n)
	for i := 0; i < len(src); i += n {
		out = append(out, src[i:min(i+n, len(src))])
	}
	return out
}
