package model

// ReleasePrecision records how precise a galgame's release_date is. It is the
// single source of truth for date precision: release_date is normalized
// (day-unknown → first of month, month-unknown → first of year) and must be
// read together with this flag. Aligns with EDTF / ISO 8601-2 partial dates.
//
// See docs/galgame_wiki/06-release-calendar-design.md §2.
type ReleasePrecision string

const (
	// PrecisionDay: exact date, release_date = YYYY-MM-DD.
	PrecisionDay ReleasePrecision = "day"
	// PrecisionMonth: year+month known, day unknown; release_date = 1st of month.
	PrecisionMonth ReleasePrecision = "month"
	// PrecisionYear: only the year is known; release_date = Jan 1 of year.
	// Such a game belongs to a year, NOT to any specific month.
	PrecisionYear ReleasePrecision = "year"
	// PrecisionTBA: announced but the date is pending; release_date = nil.
	PrecisionTBA ReleasePrecision = "tba"
	// PrecisionUnknown: no date information at all; release_date = nil.
	PrecisionUnknown ReleasePrecision = "unknown"
)

// ReleasePrecisionValues is the closed set of valid values, mirrored by the
// chk_galgame_release_precision CHECK constraint in cmd/migrate-catalog.
var ReleasePrecisionValues = []ReleasePrecision{
	PrecisionDay, PrecisionMonth, PrecisionYear, PrecisionTBA, PrecisionUnknown,
}
