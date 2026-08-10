package creditsplit

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
)

type Row struct {
	CreditNameID  int64    `json:"credit_name_id"`
	DetachSources []string `json:"detach_sources"`
	Reason        string   `json:"reason,omitempty"`
}

func LoadWorklist(path string) ([]Row, error) {
	if path == "" {
		return nil, fmt.Errorf("--worklist is required")
	}
	fh, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer fh.Close()
	var out []Row
	seen := map[int64]int{}
	sc := bufio.NewScanner(fh)
	sc.Buffer(make([]byte, 0, 1<<16), 8<<20)
	for line := 1; sc.Scan(); line++ {
		raw := strings.TrimSpace(sc.Text())
		if raw == "" {
			continue
		}
		var r Row
		if err := json.Unmarshal([]byte(raw), &r); err != nil {
			return nil, fmt.Errorf("%s:%d: %w", path, line, err)
		}
		if r.CreditNameID <= 0 {
			return nil, fmt.Errorf("%s:%d: credit_name_id must be a positive id", path, line)
		}
		if len(r.DetachSources) == 0 {
			return nil, fmt.Errorf("%s:%d: credit_name %d has no detach_sources — a split with nothing to detach is a no-op, not a row", path, line, r.CreditNameID)
		}
		if prev, dup := seen[r.CreditNameID]; dup {
			return nil, fmt.Errorf("%s:%d: credit_name %d already listed on line %d", path, line, r.CreditNameID, prev)
		}
		seen[r.CreditNameID] = line
		for i, s := range r.DetachSources {
			r.DetachSources[i] = strings.ToLower(strings.TrimSpace(s))
		}
		sort.Strings(r.DetachSources)
		out = append(out, r)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%s: no rows", path)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreditNameID < out[j].CreditNameID })
	return out, nil
}

func WriteReceipts(path string, receipts []Receipt) error {
	if path == "" {
		return nil
	}
	fh, err := os.Create(path)
	if err != nil {
		return err
	}
	defer fh.Close()
	w := bufio.NewWriter(fh)
	for _, r := range receipts {
		raw, err := json.Marshal(r)
		if err != nil {
			return err
		}
		if _, err := w.Write(append(raw, '\n')); err != nil {
			return err
		}
	}
	return w.Flush()
}
