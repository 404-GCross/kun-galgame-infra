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

type resumeState struct {
	done       map[string]struct{}
	prevScored []scoredRow
}

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
	byID := map[string]scoredRow{}
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
			continue
		}
		if probe.Error != "" || probe.Flagged == nil {
			continue
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
