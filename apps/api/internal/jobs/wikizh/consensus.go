package wikizh

import (
	"fmt"
	"sort"
)

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
		var pro, con int
		minDeciding := 1.0
		for _, v := range vs {
			switch leaning(v.Verdict) {
			case 1:
				pro++
			case -1:
				con++
			default:
				continue
			}
			if v.Confidence < minDeciding {
				minDeciding = v.Confidence
			}
		}

		switch {
		case pro > 0 && con > 0:
			st.Contested++
			out = append(out, unsureFold(folded, "各轮方向相反:"+verdictList(vs)))

		case pro*2 > len(vs) && con == 0:
			if pro == len(vs) {
				st.Unanimous++
			} else {
				st.Leaning++
			}
			folded = firstDeciding(vs)
			folded.Confidence = minDeciding
			out = append(out, folded)

		case con > 0 && pro == 0:
			st.Declined++
			out = append(out, firstDeciding(vs))

		default:
			st.Abstained++
			out = append(out, unsureFold(folded, "无人表态或表态不足半数:"+verdictList(vs)))
		}
	}
	return out, st
}

func Tiebreak(folded []Verdict, tie []Verdict) ([]Verdict, TiebreakStats) {
	var st TiebreakStats
	by := make(map[int64]Verdict, len(tie))
	for _, v := range tie {
		by[v.WorkID] = v
	}
	out := make([]Verdict, 0, len(folded))
	for _, f := range folded {
		if f.Verdict != VerdictUnsure {
			out = append(out, f)
			continue
		}
		st.Eligible++
		t, ok := by[f.WorkID]
		if !ok {
			st.NoTiebreak++
			out = append(out, f)
			continue
		}
		if leaning(t.Verdict) == 0 {
			st.StillUnsure++
			out = append(out, f)
			continue
		}
		if leaning(t.Verdict) > 0 {
			st.ResolvedFor++
		} else {
			st.ResolvedAgainst++
		}
		out = append(out, t)
	}
	return out, st
}

type TiebreakStats struct {
	Eligible        int
	ResolvedFor     int
	ResolvedAgainst int
	StillUnsure     int
	NoTiebreak      int
}

func (s TiebreakStats) String() string {
	return fmt.Sprintf("eligible=%d resolved_for=%d resolved_against=%d still_unsure=%d no_tiebreak=%d",
		s.Eligible, s.ResolvedFor, s.ResolvedAgainst, s.StillUnsure, s.NoTiebreak)
}

type ConsensusStats struct {
	Rounds     int
	Works      int
	Unanimous  int
	Leaning    int
	Declined   int
	Contested  int
	Abstained  int
	Incomplete int
}

func (s ConsensusStats) String() string {
	return fmt.Sprintf("rounds=%d works=%d unanimous=%d leaning=%d declined=%d contested=%d abstained=%d incomplete=%d",
		s.Rounds, s.Works, s.Unanimous, s.Leaning, s.Declined, s.Contested, s.Abstained, s.Incomplete)
}

func firstDeciding(vs []Verdict) Verdict {
	for _, v := range vs {
		if leaning(v.Verdict) != 0 {
			return v
		}
	}
	return vs[0]
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
