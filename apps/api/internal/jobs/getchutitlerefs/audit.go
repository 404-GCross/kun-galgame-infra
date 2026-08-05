package getchutitlerefs

import (
	"encoding/csv"
	"os"
	"strconv"
)

// WHY THIS DUMP EXISTS. Roster confirmation is a WORK-level signal: the catalog
// hangs characters off the work, not the release. So it corroborates "this
// product is that game" and says nothing at all about "this product is that
// BOX" — the release within the work is chosen by the edition and platform
// narrowing alone, with no second signal behind it.
//
// That is the one claim in this lane no automated check covers, so it gets
// dumped for a human instead of asserted. Each row carries the product title
// and the chosen release title side by side, which is exactly what is needed to
// see a wrong box at a glance.
type auditRow struct {
	GetchuID     string
	GetchuTitle  string
	Edition      string
	WorkID       int64
	ReleaseID    int64
	ReleaseTitle string
	Platform     string
	Lang         string
	Siblings     int // same-day releases the narrowing had to choose between
	Confirmed    bool
}

// writeAudit dumps the resolved candidates as CSV.
func writeAudit(path string, rows []auditRow) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	w := csv.NewWriter(f)
	defer w.Flush()
	if err := w.Write([]string{
		"getchu_id", "getchu_title", "edition", "work_id",
		"release_id", "release_title", "platform", "lang", "siblings", "roster_confirmed",
	}); err != nil {
		return err
	}
	for _, r := range rows {
		if err := w.Write([]string{
			r.GetchuID, r.GetchuTitle, r.Edition, strconv.FormatInt(r.WorkID, 10),
			strconv.FormatInt(r.ReleaseID, 10), r.ReleaseTitle, r.Platform, r.Lang,
			strconv.Itoa(r.Siblings), strconv.FormatBool(r.Confirmed),
		}); err != nil {
			return err
		}
	}
	return w.Error()
}
