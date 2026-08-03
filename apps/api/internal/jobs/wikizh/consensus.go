package wikizh

import (
	"fmt"
	"sort"
)

// Consensus folds N independent judging rounds into one verdict per work.
//
// WHY N ROUNDS. The v2 calibration ran the UNCHANGED compare prompt twice and
// 6 of 15 verdicts moved — including two that went from "unsure, 0.50" (no
// write) to "a_better, 0.90" (auto-write). That is not a prompt defect; it is
// the model being unstable on genuinely borderline cases, and a single round
// cannot tell a borderline case from a confident one because both come back
// wearing a confidence number.
//
// So agreement across independent rounds is the real signal, and confidence is
// only a floor. A work is auto-appliable when EVERY round returned the same
// verdict and every round cleared the gate. Anything else — a disagreement, a
// missing round, one low vote — becomes unsure and lands in the review pile.
// Unanimity rather than majority: a 2-of-3 split IS the borderline case this
// exists to catch.
//
// The folded verdict carries the LOWEST confidence seen, so a downstream gate
// can never be satisfied by an optimistic round alone.
func Consensus(rounds [][]Verdict) ([]Verdict, ConsensusStats) {
	var st ConsensusStats
	st.Rounds = len(rounds)
	if len(rounds) == 0 {
		return nil, st
	}

	byWork := map[int64][]Verdict{}
	for _, r := range rounds {
		for _, v := range r {
			byWork[v.WorkID] = append(byWork[v.WorkID], v)
		}
	}

	ids := make([]int64, 0, len(byWork))
	for id := range byWork {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })

	out := make([]Verdict, 0, len(ids))
	for _, id := range ids {
		vs := byWork[id]
		folded := vs[0]
		st.Works++

		if len(vs) != len(rounds) {
			st.Incomplete++
			out = append(out, unsureFold(folded, fmt.Sprintf(
				"仅 %d/%d 轮返回该条,不足以形成共识", len(vs), len(rounds))))
			continue
		}
		agreed, minConf := true, vs[0].Confidence
		for _, v := range vs[1:] {
			if v.Verdict != vs[0].Verdict {
				agreed = false
			}
			if v.Confidence < minConf {
				minConf = v.Confidence
			}
		}
		if !agreed {
			st.Disagreed++
			out = append(out, unsureFold(folded, "各轮裁决不一致:"+verdictList(vs)))
			continue
		}
		st.Unanimous++
		folded.Confidence = minConf
		folded.Reason = vs[0].Reason
		out = append(out, folded)
	}
	return out, st
}

// ConsensusStats reports the fold, so a run never silently shrinks its
// population: every work is in exactly one of these counts.
type ConsensusStats struct {
	Rounds     int
	Works      int
	Unanimous  int
	Disagreed  int
	Incomplete int
}

func (s ConsensusStats) String() string {
	return fmt.Sprintf("rounds=%d works=%d unanimous=%d disagreed=%d incomplete=%d",
		s.Rounds, s.Works, s.Unanimous, s.Disagreed, s.Incomplete)
}

func unsureFold(base Verdict, reason string) Verdict {
	base.Verdict = VerdictUnsure
	base.Confidence = 0
	base.Reason = reason
	return base
}

func verdictList(vs []Verdict) string {
	s := ""
	for i, v := range vs {
		if i > 0 {
			s += " / "
		}
		s += fmt.Sprintf("%s(%.2f)", v.Verdict, v.Confidence)
	}
	return s
}
