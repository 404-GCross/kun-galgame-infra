package bangumicovers

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// dimsFileName is the manifest kun-bangumi-api's cover fetch (Phase 2, 56b)
// writes at the mirror root: one JSON object per line
// ({"subject_id":N,"w":W,"h":H,"file":"N/cover.jpg"}).
const dimsFileName = "dims.jsonl"

// dimsEntry is one line of dims.jsonl — a fetched Bangumi cover's pixel size and
// its mirror-relative path (the cross-repo mirror contract, 56b).
type dimsEntry struct {
	SubjectID int64  `json:"subject_id"`
	W         int    `json:"w"`
	H         int    `json:"h"`
	File      string `json:"file"` // mirror-relative, e.g. "12345/cover.jpg"
}

// portrait reports whether the cover is a vertical PORTRAIT (h > w) — the only
// shape this backfill pins. Landscape/square (h <= w) is skipped: DLsite already
// supplies the landscape cover for these works (step 55). A non-positive
// dimension is never portrait (a bad/zero manifest row is skipped, not pinned).
func (e dimsEntry) portrait() bool { return e.W > 0 && e.H > 0 && e.H > e.W }

// dims is the parsed dims.jsonl, keyed by subject id STRING (the same shape
// catalog_external_ref.external_id carries, so the join is byte-exact — no int
// re-parsing per candidate).
type dims struct {
	entry map[string]dimsEntry
}

// loadDims parses `<mirror>/dims.jsonl`. Blank lines are tolerated; a malformed
// JSON line is a hard error (with its line number) — the manifest is a
// machine-generated artifact, so a corrupt line signals a real producer problem
// and must fail loud, never be silently skipped. A missing file is an error
// (the mirror is required); pass an empty dims.jsonl for a candidate-count-only
// dry run.
func loadDims(mirrorRoot string) (*dims, error) {
	path := filepath.Join(mirrorRoot, dimsFileName)
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open dims manifest %s: %w", path, err)
	}
	defer f.Close()

	d := &dims{entry: map[string]dimsEntry{}}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024) // tolerate long lines
	line := 0
	for sc.Scan() {
		line++
		raw := strings.TrimSpace(sc.Text())
		if raw == "" {
			continue
		}
		var e dimsEntry
		if err := json.Unmarshal([]byte(raw), &e); err != nil {
			return nil, fmt.Errorf("dims manifest %s line %d: %w", path, line, err)
		}
		d.entry[strconv.FormatInt(e.SubjectID, 10)] = e
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("read dims manifest %s: %w", path, err)
	}
	return d, nil
}

// coverPath is the local path a mirrored Bangumi cover lives at. It prefers the
// manifest's `file` field (the authoritative mirror-relative path the producer
// wrote) joined to the mirror root, falling back to the documented
// <subject_id>/cover.jpg layout when a row omits it.
func coverPath(mirrorRoot, subjectID string, e dimsEntry) string {
	rel := strings.TrimSpace(e.File)
	if rel == "" {
		rel = filepath.Join(subjectID, "cover.jpg")
	}
	return filepath.Join(mirrorRoot, rel)
}

// isBodyless reports whether a catalog_work is bodyless (site NULL or ""). The
// XOR guard (§8.D): native media rows are written ONLY for bodyless works — a
// claimed work bridges its media at read time and must never get a native row.
func isBodyless(site *string) bool { return site == nil || *site == "" }

// fileExists reports whether path is a non-empty regular file.
func fileExists(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && !fi.IsDir() && fi.Size() > 0
}
