package personphotos

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// dimsFileName is the manifest the crawler writes at the mirror root: one JSON
// object per line, {"id","file","w","h","url"}.
const dimsFileName = "dims.jsonl"

// mirrorExts are the extensions a mirrored photo may carry, in probe order. The
// crawler saves whatever byte format the upstream served rather than
// re-encoding, so the lane takes the file that exists instead of demanding a
// format the source never offered. jpeg is accepted alongside jpg purely as
// producer tolerance — the contract says jpg.
var mirrorExts = []string{"jpg", "jpeg", "png", "webp", "gif"}

// dimsEntry is one line of dims.jsonl. Pixel sizes are recorded but are NOT a
// filter: unlike the Bangumi cover lane (which needs w/h to tell a portrait from
// a landscape), a person photo is used at whatever shape it has, and the image
// service measures dims + thumbhash itself on upload. The useful field here is
// `file` — the producer's own statement of what it wrote, which beats probing.
type dimsEntry struct {
	ID   string `json:"id"`
	File string `json:"file"` // mirror-relative, e.g. "12345/logo.png"
	W    int    `json:"w"`
	H    int    `json:"h"`
	URL  string `json:"url"`
}

// mirror is the local byte store: a root directory plus the optional manifest.
type mirror struct {
	root  string
	entry map[string]dimsEntry
}

// loadMirror reads the mirror root and its dims.jsonl if there is one.
//
// Both are tolerated absent. An empty root is the pre-crawl dry run, whose whole
// job is to size the population and emit the ids file — failing it for having no
// bytes would make the planning step impossible to perform. A missing dims.jsonl
// is tolerated for the same reason plus a stronger one: dims are not required to
// upload, so a manifest-less mirror is still a perfectly good mirror and this
// lane falls back to probing the documented layout.
//
// A CORRUPT manifest is a hard error, with its line number. It is a
// machine-generated artefact, so a malformed line signals a real producer
// problem; skipping it silently would quietly shrink the population.
func loadMirror(root string) (*mirror, error) {
	m := &mirror{root: root, entry: map[string]dimsEntry{}}
	if root == "" {
		return m, nil
	}
	path := filepath.Join(root, dimsFileName)
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return m, nil // no manifest — probe the documented layout instead
		}
		return nil, fmt.Errorf("open dims manifest %s: %w", path, err)
	}
	defer f.Close()

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
		if e.ID == "" {
			return nil, fmt.Errorf("dims manifest %s line %d: missing id", path, line)
		}
		m.entry[e.ID] = e
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("read dims manifest %s: %w", path, err)
	}
	return m, nil
}

// resolve returns the local path holding this external id's photo bytes, if any.
// The manifest's `file` wins when it points at real bytes; otherwise the
// documented layout <root>/<external_id>/logo.<ext> is probed in mirrorExts
// order. Probing is not a fallback for a wrong manifest so much as the normal
// path for a producer that shipped bytes without one.
func (m *mirror) resolve(externalID string) (string, bool) {
	if m.root == "" {
		return "", false
	}
	if e, ok := m.entry[externalID]; ok {
		if rel := strings.TrimSpace(e.File); rel != "" {
			if p := filepath.Join(m.root, rel); fileExists(p) {
				return p, true
			}
		}
	}
	for _, ext := range mirrorExts {
		p := filepath.Join(m.root, externalID, fileStem+"."+ext)
		if fileExists(p) {
			return p, true
		}
	}
	return "", false
}

// has reports whether the mirror already holds bytes for this external id.
func (m *mirror) has(externalID string) bool {
	_, ok := m.resolve(externalID)
	return ok
}

// fileExists reports whether path is a non-empty regular file. Zero-length is
// treated as absent: a truncated fetch is not bytes, and uploading it would burn
// the person's one slot on nothing.
func fileExists(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && !fi.IsDir() && fi.Size() > 0
}
