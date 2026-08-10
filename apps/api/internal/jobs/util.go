package jobs

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
