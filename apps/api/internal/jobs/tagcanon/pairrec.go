package tagcanon

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
)

type pairRec struct {
	Kind string `json:"kind"`

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

	Source string `json:"source,omitempty"`
	Name   string `json:"name,omitempty"`
	Orig   string `json:"orig,omitempty"`
	Usage  int    `json:"usage,omitempty"`
	Tier   *int16 `json:"tier,omitempty"`
	Kind_  *int16 `json:"tier_kind,omitempty"`
	Sexual bool   `json:"sexual,omitempty"`

	// kind:"map" — (Source, Name) → an existing canonical (MapToID) or a
	// canonical named MapTo, created on demand with Tier/Kind_/Sexual. The
	// map row keeps the SOURCE name; a "single" cannot say source ≠ canonical.
	// kind:"reject" — (Source, Name) is negative knowledge → catalog_tag_rejection.
	MapToID int64  `json:"map_to_id,omitempty"`
	MapTo   string `json:"map_to,omitempty"`
	By      string `json:"by,omitempty"`

	Confidence float64 `json:"confidence"`
	Reason     string  `json:"reason,omitempty"`
	Model      string  `json:"model,omitempty"`

	Bucket      string `json:"bucket,omitempty"`
	Approve     bool   `json:"approve"`
	NeedsReview bool   `json:"needs_review,omitempty"`
}

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

func i16p(v int16) *int16 { return &v }
