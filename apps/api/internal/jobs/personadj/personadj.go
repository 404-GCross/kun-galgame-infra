// Package personadj is the LLM adjudication lane of the cross-source identity
// program (wave 156, refs/proj/156-p2b-adjudication.md; charter refs/proj/150).
//
// Four leftover buckets of waves 152/153/154 — downgraded person edges,
// character same-CV pairs, import-era contaminated credit names and the person
// conflict clusters — are judged pair by pair by a chat model, exactly the
// posture wave 87 (tag canonicalization) established:
//
//   - the wave computes its EVIDENCE PACKETS outside this package and hands
//     them in as JSONL, so a packet doubles as the durable artefact and as the
//     inline evidence of the human review file;
//   - a verdict is one of merge / distinct / unsure, and unsure never reaches
//     an automatic lane;
//   - a completion whose finish_reason is not "stop" is REFUSED, never parsed
//     (glm is a thinking model: a truncated answer looks well-formed);
//   - verdicts are appended to disk as they land, so a crashed batch resumes
//     without re-spending the judged prefix.
//
// This package speaks HTTP and files only. It never touches the database: the
// batch runs on the ops host where the gateway credentials live, while the
// packets are built and the verdicts are consumed on a machine with database
// access.
package personadj

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// Bucket names the adjudication question being asked. Each bucket has its own
// pinned system prompt, because the failure modes differ (namesakes for person
// edges, a voice actor playing two roles for characters, a bare surname
// conflating several people for the split lane).
type Bucket string

const (
	BucketPersonEdge     Bucket = "person_edge"
	BucketCharacterCV    Bucket = "character_cv"
	BucketE4Split        Bucket = "e4_split"
	BucketPersonConflict Bucket = "person_conflict"
)

// ValidBucket reports whether b has a pinned prompt.
func ValidBucket(b Bucket) bool {
	_, ok := systemPrompts[b]
	return ok
}

// Packet is one adjudication question. User is the fully rendered evidence
// packet — this package does no formatting of its own, so what the model reads
// is byte-for-byte what the review file shows a human. Meta is carried through
// untouched and is deliberately NOT shown to the model: it holds the
// independent ground-truth probe used for calibration.
type Packet struct {
	Bucket Bucket          `json:"bucket"`
	Key    string          `json:"key"`
	User   string          `json:"user"`
	Meta   json.RawMessage `json:"meta,omitempty"`
}

// Verdict is one judged packet.
type Verdict struct {
	Key    string `json:"key"`
	Bucket Bucket `json:"bucket"`
	// Verdict is merge / distinct / unsure. An unparseable or out-of-vocabulary
	// answer is an error, never a silent "unsure": a batch must be able to
	// report how much of it actually got judged.
	Verdict    string  `json:"verdict"`
	Confidence float64 `json:"confidence"`
	// EntityKind lets the person lanes flag a company / circle / brand that sits
	// in catalog_credit_name alongside people (wave 152 §7.5 — the table has no
	// person/organization discriminator). An organization pair never reaches the
	// automatic person lane whatever the verdict.
	EntityKind string `json:"entity_kind,omitempty"`
	// DetachSources is the e4 lane's answer to "which source anchors do not
	// belong on this name" — source keys (vndb / bgm / dlsite / eg).
	DetachSources []string `json:"detach_sources,omitempty"`
	Reason        string   `json:"reason"`
	Model         string   `json:"model"`
}

// Verdict values.
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

// Entity kinds.
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

// LoadPackets reads a packets JSONL file. Duplicate keys are refused: the key
// is what resume and calibration join on, so a duplicate would silently drop a
// question or double-count a verdict.
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

// LoadVerdictKeys reads the keys already present in a verdicts file. A missing
// file is not an error — it is the first run.
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

// LoadVerdicts reads a verdicts file whole (the consumer side).
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
