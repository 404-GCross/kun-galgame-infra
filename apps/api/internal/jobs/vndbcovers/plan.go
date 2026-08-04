package vndbcovers

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
)

// userAgent identifies this backfill to VNDB and to the image CDN. A batch job
// that hides behind a browser string is the kind of neighbour that gets a
// source blocked for everyone.
const userAgent = "kun-galgame-infra/backfill-vndb-covers (+https://www.kungal.com)"

// maxRatingLevel is the top of the catalog's per-image rating scale, which is
// also the top of VNDB's own 0-2 vote scale — the two happen to agree, so the
// mapping is a rounding, not a rescale.
const maxRatingLevel = 2

// idFilter builds the /vn `filters` value for an explicit id set: a bare
// ["id","=","v17"] for one, wrapped in an ["or", …] for several.
func idFilter(ids []string) any {
	if len(ids) == 1 {
		return []any{"id", "=", ids[0]}
	}
	out := make([]any, 0, len(ids)+1)
	out = append(out, "or")
	for _, id := range ids {
		out = append(out, []any{"id", "=", id})
	}
	return out
}

// parseVNResponse decodes a /vn response body.
func parseVNResponse(r io.Reader) (*vnResponse, error) {
	var out vnResponse
	if err := json.NewDecoder(r).Decode(&out); err != nil {
		return nil, fmt.Errorf("decode vndb response: %w", err)
	}
	return &out, nil
}

// ratingLevel maps one of VNDB's averaged 0-2 content-rating votes onto the
// catalog's integer per-image flag. VNDB averages its voters, so the value is
// fractional (1.34 = "most voters said suggestive, some said explicit"); the
// catalog stores a discrete level, so the average is rounded HALF-UP — a cover
// sitting exactly on the fence is filed at the stricter level, which
// over-flags rather than under-filters (the same conservative direction
// dlsitemedia's ageToSexual chose). Out-of-range and NaN inputs clamp into
// 0..2 so a malformed payload can never write a rating the read face cannot
// interpret.
func ratingLevel(v float64) int16 {
	if math.IsNaN(v) || v <= 0 {
		return 0
	}
	lvl := int16(math.Floor(v + 0.5))
	if lvl > maxRatingLevel {
		return maxRatingLevel
	}
	return lvl
}

// portrait reports whether a VNDB `dims` pair describes a VERTICAL cover
// (h > w), which is what portrait_pinned marks for the portrait-first UI. VNDB
// covers are usually portrait, but plenty of older packages are landscape and
// must not claim the pin. A missing or non-positive dimension is never portrait
// — an unknown shape is filed as the unpinned default rather than guessed.
func portrait(dims []int) bool {
	if len(dims) != 2 {
		return false
	}
	w, h := dims[0], dims[1]
	return w > 0 && h > 0 && h > w
}

func shapeLabel(dims []int) string {
	if len(dims) != 2 || dims[0] <= 0 || dims[1] <= 0 {
		return "unknown"
	}
	if portrait(dims) {
		return "portrait"
	}
	return "landscape"
}

// planRow is one candidate's decided outcome. Img is nil when VNDB has no
// cover for the anchored vn (or does not know the vn at all), in which case
// Reason says which.
type planRow struct {
	WorkID int64
	VNDBID string
	Img    *vnImage
	Reason string // set only when Img == nil
}

func (p planRow) actionable() bool { return p.Img != nil && strings.TrimSpace(p.Img.URL) != "" }

// buildPlan pairs each candidate with the cover VNDB reported for its anchor
// and tallies the forecast counters.
func buildPlan(cands []candidate, images map[string]*vnImage, stats *Stats) []planRow {
	plan := make([]planRow, 0, len(cands))
	for _, c := range cands {
		row := planRow{WorkID: c.WorkID, VNDBID: c.VNDBID}
		img, known := images[c.VNDBID]
		switch {
		case !known:
			row.Reason = "vn-unknown" // VNDB did not return this id at all
		case img == nil || strings.TrimSpace(img.URL) == "":
			row.Reason = "no-image"
		default:
			row.Img = img
		}
		if !row.actionable() {
			stats.NoImage++
			plan = append(plan, row)
			continue
		}
		if portrait(row.Img.Dims) {
			stats.Portrait++
		} else {
			stats.Landscape++
		}
		stats.Planned++
		plan = append(plan, row)
	}
	return plan
}

// actionable returns the rows an --apply run will work on, capped by limit
// (0 = no cap). The cap counts WORKS TO UPLOAD, not candidates scanned, so
// --limit 20 means twenty covers.
func actionable(plan []planRow, limit int) []planRow {
	out := make([]planRow, 0, len(plan))
	for _, row := range plan {
		if !row.actionable() {
			continue
		}
		out = append(out, row)
		if limit > 0 && len(out) == limit {
			break
		}
	}
	return out
}

// anchorIDs lists the vn ids to ask VNDB about, deduplicated (two works may in
// principle point at one vn) while keeping candidate order stable.
func anchorIDs(cands []candidate) []string {
	seen := make(map[string]bool, len(cands))
	out := make([]string, 0, len(cands))
	for _, c := range cands {
		id := strings.TrimSpace(c.VNDBID)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out
}

// ParseIDs parses a --ids "1,2,3" list into work ids. Blank entries are
// tolerated (a trailing comma); anything else is a hard error, because a
// silently dropped id would make a targeted run quietly under-cover.
func ParseIDs(raw string) ([]int64, error) {
	var out []int64
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		id, err := strconv.ParseInt(part, 10, 64)
		if err != nil || id <= 0 {
			return nil, fmt.Errorf("--ids: %q is not a work id", part)
		}
		out = append(out, id)
	}
	return out, nil
}
