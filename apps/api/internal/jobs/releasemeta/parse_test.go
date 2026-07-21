package releasemeta

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestParseFuzzyDate(t *testing.T) {
	const maxYear = 2029
	cases := []struct {
		in      string
		ok      bool
		y       int16
		m, d    int
		partial bool // m/d expected nil
	}{
		{"2004-04-28", true, 2004, 4, 28, false},
		{"1996-07-01", true, 1996, 7, 1, false},
		{"2016", true, 2016, 0, 0, true},                   // bare year — legal partial
		{"2016-05", true, 2016, 5, 0, true},                // year+month
		{"2016-00-00", true, 2016, 0, 0, true},             // month 0 → year-only
		{"2016-13-01", true, 2016, 0, 0, true},             // month 13 → year-only (day dies with it)
		{"2016-05-00", true, 2016, 5, 0, true},             // day 0 → year+month
		{"2016-05-40", true, 2016, 5, 0, true},             // day 40 → year+month
		{" 2019-09-02 00:00:00 ", true, 2019, 9, 2, false}, // trailing time ignored
		{"2050-01-01", false, 0, 0, 0, false},              // EG TBA placeholder → gated
		{"2099-12-31", false, 0, 0, 0, false},              // DLsite placeholder → gated
		{"1949-01-01", false, 0, 0, 0, false},              // below the floor
		{"abcd-ef-gh", false, 0, 0, 0, false},
		{"", false, 0, 0, 0, false},
		{"20", false, 0, 0, 0, false},
	}
	for _, c := range cases {
		y, m, d, ok := parseFuzzyDate(c.in, maxYear)
		assert.Equal(t, c.ok, ok, c.in)
		if !c.ok {
			continue
		}
		assert.Equal(t, c.y, y, c.in)
		if c.m == 0 {
			assert.Nil(t, m, c.in)
		} else {
			assert.EqualValues(t, c.m, *m, c.in)
		}
		if c.d == 0 {
			assert.Nil(t, d, c.in)
		} else {
			assert.EqualValues(t, c.d, *d, c.in)
		}
	}
}
