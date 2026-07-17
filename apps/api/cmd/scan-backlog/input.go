package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"unicode/utf8"
)

// readInput streams the JSONL, returning the valid records (capped at limit when
// limit > 0) and the hygiene counters. A non-UTF-8 line is counted invalidUTF8;
// a line that fails to decode or lacks an id/text is a badLine; both are skipped
// (spec ruling 1). Blank lines are ignored silently.
func readInput(r io.Reader, limit int) ([]inputRecord, inputStats, error) {
	var recs []inputRecord
	var st inputStats
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), scanBufMax)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		if !utf8.Valid(line) {
			st.invalidUTF8++
			continue
		}
		var rec inputRecord
		if err := json.Unmarshal(line, &rec); err != nil {
			st.badLines++
			continue
		}
		if rec.ID == "" || rec.Text == "" {
			st.badLines++
			continue
		}
		st.valid++
		recs = append(recs, rec)
		if limit > 0 && len(recs) >= limit {
			break
		}
	}
	if err := sc.Err(); err != nil {
		return nil, st, fmt.Errorf("scan input: %w", err)
	}
	return recs, st, nil
}

// resumeState is what a prior -out gives us: the set of already-succeeded ids to
// skip, and the previously-scored rows to fold into the summary/worklist.
type resumeState struct {
	done       map[string]struct{}
	prevScored []scoredRow
}

// loadResume reads an existing -out JSONL (an append log). A line with an
// "error" field is a prior failure — NOT counted done, so it is retried this
// run. A success line (a "flagged" field, no error) marks its id done and folds
// into the summary basis. A later success supersedes an earlier failure for the
// same id (append-mode reality). A missing file → empty state (first run).
func loadResume(path string) (resumeState, error) {
	rs := resumeState{done: map[string]struct{}{}}
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return rs, nil
		}
		return rs, fmt.Errorf("open resume %s: %w", path, err)
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), scanBufMax)
	byID := map[string]scoredRow{} // last success wins per id
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 || !utf8.Valid(line) {
			continue
		}
		var probe struct {
			ID      string `json:"id"`
			Error   string `json:"error"`
			Flagged *bool  `json:"flagged"`
		}
		if err := json.Unmarshal(line, &probe); err != nil || probe.ID == "" {
			continue // ignore unrecognizable lines in the append log
		}
		if probe.Error != "" || probe.Flagged == nil {
			continue // a prior failure (or non-success) — retry it, do not mark done
		}
		var row scoredRow
		if err := json.Unmarshal(line, &row); err != nil {
			continue
		}
		byID[row.ID] = row
	}
	if err := sc.Err(); err != nil {
		return rs, fmt.Errorf("scan resume %s: %w", path, err)
	}
	for id, row := range byID {
		rs.done[id] = struct{}{}
		rs.prevScored = append(rs.prevScored, row)
	}
	return rs, nil
}
