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

const dimsFileName = "dims.jsonl"

type dimsEntry struct {
	SubjectID int64  `json:"subject_id"`
	W         int    `json:"w"`
	H         int    `json:"h"`
	File      string `json:"file"`
}

func (e dimsEntry) portrait() bool { return e.W > 0 && e.H > 0 && e.H > e.W }

type dims struct {
	entry map[string]dimsEntry
}

func loadDims(mirrorRoot string) (*dims, error) {
	path := filepath.Join(mirrorRoot, dimsFileName)
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open dims manifest %s: %w", path, err)
	}
	defer f.Close()

	d := &dims{entry: map[string]dimsEntry{}}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
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

func coverPath(mirrorRoot, subjectID string, e dimsEntry) string {
	rel := strings.TrimSpace(e.File)
	if rel == "" {
		rel = filepath.Join(subjectID, "cover.jpg")
	}
	return filepath.Join(mirrorRoot, rel)
}

func isBodyless(site *string) bool { return site == nil || *site == "" }

func fileExists(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && !fi.IsDir() && fi.Size() > 0
}
