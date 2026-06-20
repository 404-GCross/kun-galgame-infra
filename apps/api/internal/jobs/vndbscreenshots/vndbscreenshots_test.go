package vndbscreenshots

import "testing"

func TestRating(t *testing.T) {
	cases := []struct {
		in   float64
		want int16
	}{
		{0.0, 0}, {0.4, 0}, {0.5, 1}, {1.0, 1}, {1.49, 1}, {1.5, 2}, {1.6, 2}, {2.0, 2}, {-1, 0}, {5, 2},
	}
	for _, c := range cases {
		if got := rating(c.in); got != c.want {
			t.Errorf("rating(%v) = %d, want %d", c.in, got, c.want)
		}
	}
}
