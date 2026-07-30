package wikirescue

import (
	"fmt"
	"strings"
)

// ParseSteps resolves a --step spec into the canonical execution order.
//
// The spec is "all" (or empty) for every step, a single step key, or a
// comma-separated selection such as "m,n,o". The selection form exists because
// the wave that owns a step is not the wave that owns the tool: W2 must be able
// to run its three steps against production without replaying W0's a..i, which
// are converged and whose re-run — while inert by construction — would still
// re-read the whole wiki family for nothing.
//
// The result is deduplicated and always sorted into the canonical order, never
// the order the caller typed: the steps have real dependencies (the gid map runs
// after the works exist; the image-usage adoption runs after the rows that
// reference the hashes are in place), and honoring a typed order would let a
// typo silently produce a half-projected run.
//
// The W1-pre mirror steps (refs/proj/140) split into two selections:
//
//	--step p,q,r,s   the idempotent followers — the daily chain's selection,
//	                 canonical order p → q → r → s
//	--step t         the ONE-SHOT meta adoption, run exactly once per database
//
// t is in the canonical order (so "all" and a rehearsal replay stay complete) but
// must NEVER join the daily chain, and the W1 final sweep must not run it either:
// its rows pass to the workratings / dlsitegenres importers the moment this wave
// removes their claim guards, and re-running t would overwrite the importers'
// fresher values with the wiki meta's older snapshot. See stepMetaAdopt.
func ParseSteps(spec string) ([]string, error) {
	want := strings.ToLower(strings.TrimSpace(spec))
	if want == "" || want == "all" {
		return steps, nil
	}
	wanted := make(map[string]struct{}, len(steps))
	for _, part := range strings.Split(want, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if !contains(steps, part) {
			return nil, fmt.Errorf("unknown step %q (want one of %s, a comma-separated selection, or \"all\")", part, strings.Join(steps, ","))
		}
		wanted[part] = struct{}{}
	}
	if len(wanted) == 0 {
		return nil, fmt.Errorf("empty step selection %q", spec)
	}
	out := make([]string, 0, len(wanted))
	for _, s := range steps {
		if _, ok := wanted[s]; ok {
			out = append(out, s)
		}
	}
	return out, nil
}
