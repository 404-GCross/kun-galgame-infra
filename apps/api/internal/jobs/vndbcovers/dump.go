package vndbcovers

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const cvPrefix = "cv" // VNDB cover image-id prefix

// rating maps a VNDB `c_*_avg` value (0-200 = average vote * 100, where the raw
// vote scale is 0=safe/tame, 1=suggestive/violent, 2=explicit/brutal) onto our
// int16 0-2 column. Round-half-up; clamp to [0,2]. (Mirrors the proven
// migrate-galgame-screenshots helper — intentional small dup, that cmd is a
// frozen one-shot.)
func rating(avg100 int) int16 {
	return int16(min(2, max(0, (avg100+50)/100)))
}

type rate struct{ sexual, violence int16 }

// readLocal reads a cover file from the rsync mirror at <dir>/<rel>.
func readLocal(dir, rel string) ([]byte, error) {
	return os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
}

// cvRelPath maps a VNDB cover id ("cv12345") to its path relative to the
// vndb-img root, mirroring the t.vndb.org layout: cv/<n mod 100, 2-digit>/<n>.jpg.
func cvRelPath(cv string) (string, error) {
	num := strings.TrimPrefix(cv, cvPrefix)
	n, err := strconv.Atoi(num)
	if err != nil {
		return "", fmt.Errorf("bad cover id %q", cv)
	}
	return fmt.Sprintf("cv/%02d/%s.jpg", n%100, num), nil
}

// Documented VNDB dump column layouts (db/<table>), used only when the companion
// db/<table>.header file is absent. Stable for years; a present .header overrides
// these so a schema drift is handled transparently.
var (
	// db/vn: `id image c_image olang ...` — `image` is the VN's cover id, col 1
	// (col 2 `c_image` is a cache field; don't use it).
	vnFallbackCols     = map[string]int{"id": 0, "image": 1}
	imagesFallbackCols = map[string]int{"id": 0, "c_sexual_avg": 4, "c_violence_avg": 6}
)

// resolveCols returns the 0-based index of each named column, preferring the
// dump file's companion ".header" (a single tab-separated line of column names)
// and falling back to a documented layout when no header ships with the dump.
func resolveCols(dataPath string, fallback map[string]int, names ...string) ([]int, error) {
	if cols, err := headerCols(dataPath, names...); err == nil {
		return cols, nil
	}
	out := make([]int, len(names))
	for i, n := range names {
		j, ok := fallback[n]
		if !ok {
			return nil, fmt.Errorf("%s: no .header and no fallback index for column %q", dataPath, n)
		}
		out[i] = j
	}
	return out, nil
}

func headerCols(dataPath string, names ...string) ([]int, error) {
	b, err := os.ReadFile(dataPath + ".header")
	if err != nil {
		return nil, err
	}
	cols := strings.Split(strings.TrimSpace(string(b)), "\t")
	idx := make(map[string]int, len(cols))
	for i, c := range cols {
		idx[strings.TrimSpace(c)] = i
	}
	out := make([]int, len(names))
	for i, n := range names {
		j, ok := idx[n]
		if !ok {
			return nil, fmt.Errorf("column %q not in %s.header", n, dataPath)
		}
		out[i] = j
	}
	return out, nil
}

// loadVNCoverMap reads the dump's db/vn into v-id -> cover cv-id (the `image`
// column), keeping only rows that actually have a cover.
func loadVNCoverMap(vnPath string) (map[string]string, error) {
	cols, err := resolveCols(vnPath, vnFallbackCols, "id", "image")
	if err != nil {
		return nil, err
	}
	idCol, imgCol := cols[0], cols[1]
	maxCol := max(idCol, imgCol)

	f, err := os.Open(vnPath)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	out := map[string]string{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1024*1024), 1024*1024)
	for sc.Scan() {
		fields := strings.Split(sc.Text(), "\t")
		if len(fields) <= maxCol {
			continue
		}
		vid, img := fields[idCol], fields[imgCol]
		// `\N`/empty image (no cover) or malformed → skip.
		if !strings.HasPrefix(vid, "v") || !strings.HasPrefix(img, cvPrefix) {
			continue
		}
		out[vid] = img
	}
	return out, sc.Err()
}

// loadCoverRatings reads the dump's db/images, keeping only cv* rows ->
// {sexual, violence}.
func loadCoverRatings(imagesPath string) (map[string]rate, error) {
	cols, err := resolveCols(imagesPath, imagesFallbackCols, "id", "c_sexual_avg", "c_violence_avg")
	if err != nil {
		return nil, err
	}
	idCol, sexCol, violCol := cols[0], cols[1], cols[2]
	maxCol := max(idCol, max(sexCol, violCol))

	f, err := os.Open(imagesPath)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	out := map[string]rate{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1024*1024), 1024*1024)
	for sc.Scan() {
		fields := strings.Split(sc.Text(), "\t")
		if len(fields) <= maxCol {
			continue
		}
		id := fields[idCol]
		if !strings.HasPrefix(id, cvPrefix) {
			continue // covers only; skip ch*/sf*
		}
		sexAvg, _ := strconv.Atoi(fields[sexCol])
		violAvg, _ := strconv.Atoi(fields[violCol])
		out[id] = rate{sexual: rating(sexAvg), violence: rating(violAvg)}
	}
	return out, sc.Err()
}

// dumpSource resolves cover metadata from the offline VNDB dump (one-time bulk
// backfill). Byte fetching is shared with the API path via fetchCover.
type dumpSource struct {
	vnCover map[string]string // v-id -> cv-id
	ratings map[string]rate   // cv-id -> sexual/violence
}

func newDumpSource(vnPath, imagesPath string) (*dumpSource, error) {
	vnCover, err := loadVNCoverMap(vnPath)
	if err != nil {
		return nil, fmt.Errorf("load db/vn (%s): %w", vnPath, err)
	}
	if len(vnCover) == 0 {
		return nil, fmt.Errorf("db/vn (%s) produced 0 cover mappings — wrong path or column layout", vnPath)
	}
	ratings, err := loadCoverRatings(imagesPath)
	if err != nil {
		return nil, fmt.Errorf("load db/images (%s): %w", imagesPath, err)
	}
	if len(ratings) == 0 {
		return nil, fmt.Errorf("db/images (%s) produced 0 cv ratings — wrong path or column layout", imagesPath)
	}
	return &dumpSource{vnCover: vnCover, ratings: ratings}, nil
}

func (d *dumpSource) prefetch(context.Context, []string) error { return nil }

func (d *dumpSource) lookup(vndbID string) (coverMeta, bool) {
	cv, ok := d.vnCover[vndbID]
	if !ok {
		return coverMeta{}, false
	}
	r := d.ratings[cv] // zero value (0/0) if the cv has no images row — safe default
	return coverMeta{cvID: cv, sexual: r.sexual, violence: r.violence}, true
}
