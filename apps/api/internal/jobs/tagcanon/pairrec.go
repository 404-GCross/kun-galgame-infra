package tagcanon

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
)

// pairRec is ONE decision flowing through all three 70b phases (a single record
// type keeps propose→make-review→apply-reviewed consistent):
//
//   - propose writes it with the raw LLM verdict (Bucket/Approve unset);
//   - make-review fills Bucket + Approve (+ NeedsReview) and re-emits it;
//   - apply-reviewed consumes the records with Approve=true.
//
// Sources are carried as registry KEYS (not seed ids) so a JSONL produced in
// rehearsal applies in prod regardless of auto-increment drift; apply resolves
// keys→ids at write time. Tier/Kind are pointers so "unset" (a pair record, or
// a proposal the model declined) is distinguishable from a meaningful 0 (core /
// content).
type pairRec struct {
	Kind string `json:"kind"` // "pair" | "single"

	// pair fields (Kind == "pair")
	ASource  string `json:"a_source,omitempty"`
	AName    string `json:"a_name,omitempty"`
	AOrig    string `json:"a_orig,omitempty"`
	AUsage   int    `json:"a_usage,omitempty"`
	BSource  string `json:"b_source,omitempty"`
	BName    string `json:"b_name,omitempty"`
	BOrig    string `json:"b_orig,omitempty"`
	BUsage   int    `json:"b_usage,omitempty"`
	Block    string `json:"block,omitempty"`
	Relation string `json:"relation,omitempty"`

	// single-source admission fields (Kind == "single")
	Source string `json:"source,omitempty"`
	Name   string `json:"name,omitempty"`
	Orig   string `json:"orig,omitempty"`
	Usage  int    `json:"usage,omitempty"`
	Tier   *int16 `json:"tier,omitempty"`
	Kind_  *int16 `json:"tier_kind,omitempty"` // proposed kind (named Kind_ to avoid clashing with the Kind discriminator)

	// verdict (common)
	Confidence float64 `json:"confidence"`
	Reason     string  `json:"reason,omitempty"`
	Model      string  `json:"model,omitempty"`

	// review bucketing (filled by make-review)
	Bucket      string `json:"bucket,omitempty"`       // "high" | "medium" | "low"
	Approve     bool   `json:"approve"`                // apply-reviewed acts on this
	NeedsReview bool   `json:"needs_review,omitempty"` // medium — a human must set Approve
}

// writeRecords writes records as JSONL (one compact object per line).
func writeRecords(path string, recs []pairRec) error {
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

// readRecords reads a JSONL file of pairRec (blank lines tolerated).
func readRecords(path string) ([]pairRec, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []pairRec
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	line := 0
	for sc.Scan() {
		line++
		b := sc.Bytes()
		if len(trimSpaceBytes(b)) == 0 {
			continue
		}
		var r pairRec
		if err := json.Unmarshal(b, &r); err != nil {
			return nil, fmt.Errorf("%s:%d: %w", path, line, err)
		}
		out = append(out, r)
	}
	if err := sc.Err(); err != nil && err != io.EOF {
		return nil, err
	}
	return out, nil
}

func trimSpaceBytes(b []byte) []byte {
	i, j := 0, len(b)
	for i < j && (b[i] == ' ' || b[i] == '\t' || b[i] == '\r' || b[i] == '\n') {
		i++
	}
	for j > i && (b[j-1] == ' ' || b[j-1] == '\t' || b[j-1] == '\r' || b[j-1] == '\n') {
		j--
	}
	return b[i:j]
}

// i16p boxes an int16 for the optional Tier/Kind_ proposal fields.
func i16p(v int16) *int16 { return &v }
