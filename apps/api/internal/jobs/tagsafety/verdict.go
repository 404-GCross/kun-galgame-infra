package tagsafety

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
)

// VocabSource is the pseudo source key carried by verdicts about the CANONICAL
// vocabulary (catalog_tag rows, loaded with --vocab) rather than about one
// source's verbatim folksonomy. It is deliberately not a registry key so it can
// never collide with catalog_source.key and can never be resolved to a
// source_id — apply routes these rows straight at catalog_tag by name.
const VocabSource = "catalog_tag"

// Verdict is ONE line of the classify JSONL — the single record type flowing
// through classify → (human review) → apply. Sources are carried as registry
// KEYS (never seed ids) so a JSONL produced in rehearsal applies in prod
// regardless of auto-increment drift; apply resolves keys→ids at write time.
type Verdict struct {
	Source     string  `json:"source"` // "bangumi" / "dlsite" / VocabSource
	Name       string  `json:"name"`
	Uses       int     `json:"uses"`  // distinct works carrying the name
	Votes      int     `json:"votes"` // summed folksonomy count
	Class      string  `json:"class"`
	Confidence float64 `json:"confidence"`
	Reason     string  `json:"reason,omitempty"`
	Model      string  `json:"model,omitempty"`
	// Note is set by apply on review lines only, explaining WHY a line landed in
	// the review file (below threshold, or the de-flag guard).
	Note string `json:"note,omitempty"`
}

// key is the record identity: one verdict per (source, name). Resume skipping,
// reviewed-line overrides and write dedup all key on this.
func (v Verdict) key() string { return verdictKey(v.Source, v.Name) }

func verdictKey(source, name string) string { return source + "\x00" + name }

// ReviewedLine is one hand-ruled verdict fed back via --reviewed. Only the
// identity and the class are required — a human ruling applies at FULL trust
// (confidence is not consulted), which is exactly why the file is a separate
// explicit flag and never something apply picks up on its own.
type ReviewedLine struct {
	Source string `json:"source"`
	Name   string `json:"name"`
	Class  string `json:"class"`
	Reason string `json:"reason,omitempty"`
}

// readVerdicts reads a JSONL verdict file (blank lines tolerated). A missing
// file is NOT an error for the resume path — see loadDone.
func readVerdicts(path string) ([]Verdict, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []Verdict
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	line := 0
	for sc.Scan() {
		line++
		text := strings.TrimSpace(sc.Text())
		if text == "" {
			continue
		}
		var v Verdict
		if err := json.Unmarshal([]byte(text), &v); err != nil {
			return nil, fmt.Errorf("%s:%d: %w", path, line, err)
		}
		out = append(out, v)
	}
	if err := sc.Err(); err != nil && err != io.EOF {
		return nil, err
	}
	return out, nil
}

// readReviewed reads the hand-ruled JSONL (--reviewed). Lines with an unknown
// class are a HARD error: a typo in a human ruling must stop the run, not be
// silently dropped into "no write".
func readReviewed(path string) ([]ReviewedLine, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []ReviewedLine
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	line := 0
	for sc.Scan() {
		line++
		text := strings.TrimSpace(sc.Text())
		if text == "" {
			continue
		}
		var r ReviewedLine
		if err := json.Unmarshal([]byte(text), &r); err != nil {
			return nil, fmt.Errorf("%s:%d: %w", path, line, err)
		}
		if r.Source == "" || r.Name == "" {
			return nil, fmt.Errorf("%s:%d: reviewed line needs both source and name", path, line)
		}
		if !validClass(Class(r.Class)) {
			return nil, fmt.Errorf("%s:%d: reviewed line has invalid class %q", path, line, r.Class)
		}
		out = append(out, r)
	}
	if err := sc.Err(); err != nil && err != io.EOF {
		return nil, err
	}
	return out, nil
}

// loadDone reads the (source,name) keys already present in an output file so an
// interrupted classify run resumes instead of re-paying for the whole
// vocabulary. A MISSING file is the fresh-start case and returns an empty set; a
// present-but-unreadable file is a hard error (silently restarting from zero
// would double-bill the gateway and duplicate every line).
func loadDone(path string) (map[string]struct{}, error) {
	if path == "" {
		return map[string]struct{}{}, nil
	}
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return map[string]struct{}{}, nil
	}
	recs, err := readVerdicts(path)
	if err != nil {
		return nil, err
	}
	done := make(map[string]struct{}, len(recs))
	for _, r := range recs {
		done[r.key()] = struct{}{}
	}
	return done, nil
}

// verdictAppender appends JSONL lines to the output file, flushing after every
// batch. Appending (never rewriting) is what makes resume work: an interrupted
// run leaves a valid prefix, and the next run picks up after it.
type verdictAppender struct {
	f *os.File
	w *bufio.Writer
}

func newVerdictAppender(path string) (*verdictAppender, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, err
	}
	return &verdictAppender{f: f, w: bufio.NewWriter(f)}, nil
}

func (a *verdictAppender) append(recs ...Verdict) error {
	enc := json.NewEncoder(a.w)
	for _, r := range recs {
		if err := enc.Encode(r); err != nil {
			return err
		}
	}
	return a.w.Flush()
}

func (a *verdictAppender) Close() error {
	if err := a.w.Flush(); err != nil {
		a.f.Close()
		return err
	}
	return a.f.Close()
}

// writeVerdicts writes a fresh JSONL file (used for the review output, which is
// regenerated in full on every apply — unlike the classify output it is not a
// resumable log).
func writeVerdicts(path string, recs []Verdict) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	w := bufio.NewWriter(f)
	enc := json.NewEncoder(w)
	for _, r := range recs {
		if err := enc.Encode(r); err != nil {
			return err
		}
	}
	return w.Flush()
}
