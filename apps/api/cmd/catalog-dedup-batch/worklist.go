package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"sort"
)

// A worklist lets a wave that computes its own candidates OUTSIDE this binary
// drive the merge machinery here without a second merger existing. Wave 154
// (refs/proj/154-p3-character-residual.md) is the first user: its cross-source
// character residual rules (alias-expanded name equality under same_work, and
// the CV-person review lane) need evidence this package's SQL detectors do not
// carry, but the execution path — ProposeMerge -> ApproveMerge -> 48h cooling
// -> ExecuteMerge, with the same survivor already chosen — must stay the one
// path an admin click takes.
//
// Format: one JSON object per line, blank lines ignored.
//
//	{"class":"character","survivor":112961,"sources":[14292]}
//
// Any further keys (evidence, names, provenance) are ignored here on purpose:
// the worklist file doubles as the wave's durable artefact, so it carries more
// than this binary needs.
type worklistEntry struct {
	Class    string  `json:"class"`
	Survivor int64   `json:"survivor"`
	Sources  []int64 `json:"sources"`
}

// loadWorklist reads and validates a worklist into merge groups.
//
// The validation is the whole point of the lane: a worklist is only safe to
// execute if it is a PARTITION of the entities it touches. An id appearing in
// two groups (or as both a survivor and a source) would make the outcome depend
// on execution order, which is exactly the chain hazard runExecute already has
// to clean up after; refusing the file is far better than discovering it after
// the cooling window. Groups are returned in survivor order so a -limit canary
// selects a stable prefix.
func loadWorklist(path string) ([]mergeGroup, error) {
	fh, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer fh.Close()

	var groups []mergeGroup
	owner := map[int64]int{} // entity id -> line number that already claimed it
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
		case classCharacter, classCreditName, classOrphanCreditName, classMixedCreditName:
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
