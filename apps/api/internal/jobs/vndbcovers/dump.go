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

// cvMeta is the per-cover metadata read from the dump's db/images: content
// ratings + pixel count (for the pre-upload pixel-limit check).
type cvMeta struct {
	sexual, violence int16
	pixels           int
}

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
	imagesFallbackCols = map[string]int{"id": 0, "width": 1, "height": 2, "c_sexual_avg": 4, "c_violence_avg": 6}
	// db/releases_vn: `id vid rtype` — release id + vn id.
	relVNFallbackCols = map[string]int{"id": 0, "vid": 1}
	// db/releases_images: `id img vid itype lang` — release id + cover cv-id + type.
	relImgFallbackCols = map[string]int{"id": 0, "img": 1, "itype": 3}
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

// loadCoverMeta reads the dump's db/images, keeping only cv* rows ->
// {sexual, violence, pixels (width*height)}.
func loadCoverMeta(imagesPath string) (map[string]cvMeta, error) {
	cols, err := resolveCols(imagesPath, imagesFallbackCols, "id", "width", "height", "c_sexual_avg", "c_violence_avg")
	if err != nil {
		return nil, err
	}
	idCol, wCol, hCol, sexCol, violCol := cols[0], cols[1], cols[2], cols[3], cols[4]
	maxCol := max(idCol, max(wCol, max(hCol, max(sexCol, violCol))))

	f, err := os.Open(imagesPath)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	out := map[string]cvMeta{}
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
		w, _ := strconv.Atoi(fields[wCol])
		h, _ := strconv.Atoi(fields[hCol])
		out[id] = cvMeta{sexual: rating(sexAvg), violence: rating(violAvg), pixels: w * h}
	}
	return out, sc.Err()
}

// loadReleaseCovers reads db/releases_vn (release→vn) + db/releases_images
// (release→cv-id+type) and returns v-id → release covers (raw; the caller dedups
// against the main cover). Only cv* images are kept; ratings come from db/images.
func loadReleaseCovers(relVNPath, relImgPath string, meta map[string]cvMeta) (map[string][]coverItem, error) {
	cols, err := resolveCols(relVNPath, relVNFallbackCols, "id", "vid")
	if err != nil {
		return nil, err
	}
	relCol, vnCol := cols[0], cols[1]
	maxRV := max(relCol, vnCol)

	rf, err := os.Open(relVNPath)
	if err != nil {
		return nil, err
	}
	defer rf.Close()
	relToVNs := map[string][]string{}
	sc := bufio.NewScanner(rf)
	sc.Buffer(make([]byte, 1024*1024), 1024*1024)
	for sc.Scan() {
		f := strings.Split(sc.Text(), "\t")
		if len(f) <= maxRV {
			continue
		}
		rel, vn := f[relCol], f[vnCol]
		if strings.HasPrefix(vn, "v") {
			relToVNs[rel] = append(relToVNs[rel], vn)
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}

	cols2, err := resolveCols(relImgPath, relImgFallbackCols, "id", "img", "itype")
	if err != nil {
		return nil, err
	}
	relC, imgC, typeC := cols2[0], cols2[1], cols2[2]
	maxRI := max(relC, max(imgC, typeC))

	imgf, err := os.Open(relImgPath)
	if err != nil {
		return nil, err
	}
	defer imgf.Close()
	out := map[string][]coverItem{}
	sc2 := bufio.NewScanner(imgf)
	sc2.Buffer(make([]byte, 1024*1024), 1024*1024)
	for sc2.Scan() {
		f := strings.Split(sc2.Text(), "\t")
		if len(f) <= maxRI {
			continue
		}
		rel, img, kind := f[relC], f[imgC], f[typeC]
		if !strings.HasPrefix(img, cvPrefix) {
			continue // covers are cv*; skip any other id namespace
		}
		m := meta[img]
		for _, vn := range relToVNs[rel] {
			out[vn] = append(out[vn], coverItem{cvID: img, kind: kind, sexual: m.sexual, violence: m.violence, pixels: m.pixels})
		}
	}
	return out, sc2.Err()
}

// dumpSource resolves the full cover set per VN from the offline VNDB dump
// (one-time bulk backfill). Byte fetching is shared with the API path via fetchCover.
type dumpSource struct {
	vnCover   map[string]string      // v-id -> main cover cv-id (db/vn.image)
	relCovers map[string][]coverItem // v-id -> release covers (db/releases_*)
	meta      map[string]cvMeta      // cv-id -> sexual/violence/pixels
}

func newDumpSource(vnPath, imagesPath, relVNPath, relImgPath string) (*dumpSource, error) {
	vnCover, err := loadVNCoverMap(vnPath)
	if err != nil {
		return nil, fmt.Errorf("load db/vn (%s): %w", vnPath, err)
	}
	if len(vnCover) == 0 {
		return nil, fmt.Errorf("db/vn (%s) produced 0 cover mappings — wrong path or column layout", vnPath)
	}
	meta, err := loadCoverMeta(imagesPath)
	if err != nil {
		return nil, fmt.Errorf("load db/images (%s): %w", imagesPath, err)
	}
	if len(meta) == 0 {
		return nil, fmt.Errorf("db/images (%s) produced 0 cv rows — wrong path or column layout", imagesPath)
	}
	relCovers, err := loadReleaseCovers(relVNPath, relImgPath, meta)
	if err != nil {
		return nil, fmt.Errorf("load release covers (%s + %s): %w", relVNPath, relImgPath, err)
	}
	if len(relCovers) == 0 {
		return nil, fmt.Errorf("db/releases_images (%s) produced 0 release covers — wrong path or column layout", relImgPath)
	}
	return &dumpSource{vnCover: vnCover, relCovers: relCovers, meta: meta}, nil
}

func (d *dumpSource) prefetch(context.Context, []string) error { return nil }

// lookup returns the deduped cover set for a VN: the main cover (kind="main")
// first, then release covers (kind=their type), skipping any repeated cv-id.
func (d *dumpSource) lookup(vndbID string) []coverItem {
	out := make([]coverItem, 0, 8)
	seen := map[string]bool{}
	if cv, ok := d.vnCover[vndbID]; ok {
		m := d.meta[cv]
		out = append(out, coverItem{cvID: cv, kind: kindMain, sexual: m.sexual, violence: m.violence, pixels: m.pixels})
		seen[cv] = true
	}
	for _, it := range d.relCovers[vndbID] {
		if seen[it.cvID] {
			continue
		}
		seen[it.cvID] = true
		out = append(out, it)
	}
	return out
}
