package personphotos

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const dimsFileName = "dims.jsonl"

var mirrorExts = []string{"jpg", "jpeg", "png", "webp", "gif"}

type dimsEntry struct {
	ID   string `json:"id"`
	File string `json:"file"`
	W    int    `json:"w"`
	H    int    `json:"h"`
	URL  string `json:"url"`
}

type mirror struct {
	root  string
	entry map[string]dimsEntry
}

func loadMirror(root string) (*mirror, error) {
	m := &mirror{root: root, entry: map[string]dimsEntry{}}
	if root == "" {
		return m, nil
	}
	path := filepath.Join(root, dimsFileName)
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return m, nil
		}
		return nil, fmt.Errorf("open dims manifest %s: %w", path, err)
	}
	defer f.Close()

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

func (m *mirror) has(externalID string) bool {
	_, ok := m.resolve(externalID)
	return ok
}

func fileExists(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && !fi.IsDir() && fi.Size() > 0
}
