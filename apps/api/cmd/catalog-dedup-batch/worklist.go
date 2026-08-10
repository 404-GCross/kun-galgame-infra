package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"sort"
)

type worklistEntry struct {
	Class    string  `json:"class"`
	Survivor int64   `json:"survivor"`
	Sources  []int64 `json:"sources"`
}

func loadWorklist(path string) ([]mergeGroup, error) {
	fh, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer fh.Close()

	var groups []mergeGroup
	owner := map[int64]int{}
	sc := bufio.NewScanner(fh)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for line := 1; sc.Scan(); line++ {
		raw := sc.Bytes()
		if len(raw) == 0 {
			continue
		}
		var e worklistEntry
		if err := json.Unmarshal(raw, &e); err != nil {
			return nil, fmt.Errorf("%s:%d: %w", path, line, err)
		}
		switch e.Class {
		case classCharacter, classCreditName, classOrphanCreditName, classMixedCreditName, classPerson, classLabel:
		default:
			return nil, fmt.Errorf("%s:%d: unknown class %q", path, line, e.Class)
		}
		if e.Survivor <= 0 {
			return nil, fmt.Errorf("%s:%d: survivor must be a positive id", path, line)
		}
		if len(e.Sources) == 0 {
			return nil, fmt.Errorf("%s:%d: group has no sources", path, line)
		}
		for _, id := range append([]int64{e.Survivor}, e.Sources...) {
			if id <= 0 {
				return nil, fmt.Errorf("%s:%d: invalid entity id %d", path, line, id)
			}
			if prev, dup := owner[id]; dup {
				return nil, fmt.Errorf("%s:%d: entity %d already claimed on line %d — "+
					"a worklist must partition the entities it merges", path, line, id, prev)
			}
			owner[id] = line
		}
		groups = append(groups, mergeGroup{
			class: e.Class, survivor: e.Survivor, sources: e.Sources,
			sample: fmt.Sprintf("worklist %s:%d survivor=%d sources=%s",
				path, line, e.Survivor, joinIDs(e.Sources)),
		})
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if len(groups) == 0 {
		return nil, fmt.Errorf("%s: no groups", path)
	}
	sort.Slice(groups, func(a, b int) bool { return groups[a].survivor < groups[b].survivor })
	return groups, nil
}
