package releasemeta

import (
	"strconv"
	"strings"
)

// parseFuzzyDate parses a source date string ('YYYY[-MM[-DD]]…') tolerantly
// into the nullable released_y/m/d trio:
//
//   - the leading 4 digits must be a year inside [minYear, maxYear], else the
//     whole date is rejected (ok=false) — this drops garbage AND the 2050/2099
//     TBA placeholders;
//   - month/day are best-effort: a missing or invalid month (0, >12, junk)
//     yields a legal year-only partial (and drops the day with it — a day
//     without a month is meaningless); an invalid day yields year+month.
//
// Trailing text beyond the day is ignored, so full timestamps parse too.
func parseFuzzyDate(s string, maxYear int) (y int16, m, d *int16, ok bool) {
	s = strings.TrimSpace(s)
	if len(s) < 4 {
		return 0, nil, nil, false
	}
	yy, err := strconv.Atoi(s[:4])
	if err != nil || yy < minYear || yy > maxYear {
		return 0, nil, nil, false
	}
	y = int16(yy)
	if len(s) >= 7 && s[4] == '-' {
		if mm, err := strconv.Atoi(s[5:7]); err == nil && mm >= 1 && mm <= 12 {
			mv := int16(mm)
			m = &mv
			if len(s) >= 10 && s[7] == '-' {
				if dd, err := strconv.Atoi(s[8:10]); err == nil && dd >= 1 && dd <= 31 {
					dv := int16(dd)
					d = &dv
				}
			}
		}
	}
	return y, m, d, true
}
