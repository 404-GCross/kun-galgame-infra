package tagsafety

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
)

const VocabSource = "catalog_tag"

type Verdict struct {
	Source     string  `json:"source"`
	Name       string  `json:"name"`
	Uses       int     `json:"uses"`
	Votes      int     `json:"votes"`
	Class      string  `json:"class"`
	Confidence float64 `json:"confidence"`
	Reason     string  `json:"reason,omitempty"`
	Model      string  `json:"model,omitempty"`
	Note       string  `json:"note,omitempty"`
}

func (v Verdict) key() string { return verdictKey(v.Source, v.Name) }

func verdictKey(source, name string) string { return source + "\x00" + name }

type ReviewedLine struct {
	Source string `json:"source"`
	Name   string `json:"name"`
	Class  string `json:"class"`
	Reason string `json:"reason,omitempty"`
}

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
