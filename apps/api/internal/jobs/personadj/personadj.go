package personadj

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

type Bucket string

const (
	BucketPersonEdge          Bucket = "person_edge"
	BucketCharacterCV         Bucket = "character_cv"
	BucketE4Split             Bucket = "e4_split"
	BucketPersonConflict      Bucket = "person_conflict"
	BucketCharacterPair       Bucket = "character_pair"
	BucketCharacterPairStrict Bucket = "character_pair_strict"
)

func ValidBucket(b Bucket) bool {
	_, ok := systemPrompts[b]
	return ok
}

type Packet struct {
	Bucket Bucket          `json:"bucket"`
	Key    string          `json:"key"`
	User   string          `json:"user"`
	Meta   json.RawMessage `json:"meta,omitempty"`
}

type Verdict struct {
	Key           string   `json:"key"`
	Bucket        Bucket   `json:"bucket"`
	Verdict       string   `json:"verdict"`
	Confidence    float64  `json:"confidence"`
	EntityKind    string   `json:"entity_kind,omitempty"`
	DetachSources []string `json:"detach_sources,omitempty"`
	Reason        string   `json:"reason"`
	Model         string   `json:"model"`
}

const (
	VerdictMerge    = "merge"
	VerdictDistinct = "distinct"
	VerdictUnsure   = "unsure"
)

func validVerdict(v string) bool {
	switch v {
	case VerdictMerge, VerdictDistinct, VerdictUnsure:
		return true
	}
	return false
}

const (
	KindPerson       = "person"
	KindOrganization = "organization"
	KindUnknown      = "unknown"
)

func normalizeKind(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case KindPerson, "人", "个人":
		return KindPerson
	case KindOrganization, "org", "company", "公司", "社团":
		return KindOrganization
	default:
		return KindUnknown
	}
}

func LoadPackets(path string, only Bucket) ([]Packet, error) {
	fh, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer fh.Close()
	var out []Packet
	seen := map[string]int{}
	sc := bufio.NewScanner(fh)
	sc.Buffer(make([]byte, 0, 1<<20), 16<<20)
	for line := 1; sc.Scan(); line++ {
		raw := strings.TrimSpace(sc.Text())
		if raw == "" {
			continue
		}
		var p Packet
		if err := json.Unmarshal([]byte(raw), &p); err != nil {
			return nil, fmt.Errorf("%s:%d: %w", path, line, err)
		}
		if p.Key == "" || p.User == "" {
			return nil, fmt.Errorf("%s:%d: packet needs a key and a rendered user message", path, line)
		}
		if !ValidBucket(p.Bucket) {
			return nil, fmt.Errorf("%s:%d: unknown bucket %q", path, line, p.Bucket)
		}
		if prev, dup := seen[p.Key]; dup {
			return nil, fmt.Errorf("%s:%d: key %q already used on line %d", path, line, p.Key, prev)
		}
		seen[p.Key] = line
		if only != "" && p.Bucket != only {
			continue
		}
		out = append(out, p)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func LoadVerdictKeys(path string) (map[string]bool, error) {
	fh, err := os.Open(path)
	if os.IsNotExist(err) {
		return map[string]bool{}, nil
	}
	if err != nil {
		return nil, err
	}
	defer fh.Close()
	out := map[string]bool{}
	sc := bufio.NewScanner(fh)
	sc.Buffer(make([]byte, 0, 1<<20), 16<<20)
	for sc.Scan() {
		raw := strings.TrimSpace(sc.Text())
		if raw == "" {
			continue
		}
		var v Verdict
		if err := json.Unmarshal([]byte(raw), &v); err != nil {
			return nil, err
		}
		if v.Key != "" {
			out[v.Key] = true
		}
	}
	return out, sc.Err()
}

func LoadVerdicts(path string) ([]Verdict, error) {
	fh, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer fh.Close()
	var out []Verdict
	sc := bufio.NewScanner(fh)
	sc.Buffer(make([]byte, 0, 1<<20), 16<<20)
	for sc.Scan() {
		raw := strings.TrimSpace(sc.Text())
		if raw == "" {
			continue
		}
		var v Verdict
		if err := json.Unmarshal([]byte(raw), &v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, sc.Err()
}
