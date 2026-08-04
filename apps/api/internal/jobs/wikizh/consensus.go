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
// only a floor.
//
// AGREEMENT IS ON THE DIRECTION, NOT THE LABEL. The first version of this fold
// required three IDENTICAL labels, which counted `equivalent` ("the choice does
// not matter") and `unsure` ("I abstain") as dissent. That put 306 works into
// the review pile — 197 compare, 109 usable — that no round had contradicted:
// a_better/a_better/equivalent is not a split, it is two votes and a shrug.
//
// A work is decided when no round points the other way AND a majority actively
// took the deciding side. Abstentions may accompany a majority but may not BE
// one: a lone vote among two shrugs is not a consensus. When one round wants
// the wiki text and another refuses it, the work is CONTESTED — that, and only
// that, is the borderline case worth a human.
//
// The folded verdict carries the lowest confidence among the rounds that took a
// side. Abstentions are excluded from that floor on purpose: their confidence
// is a confidence in not deciding, and folding it in would hold the decision
// hostage to a non-vote.
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
		// Agreement is on the DIRECTION, not the label: a round that says
		// "equivalent" or "unsure" has not contradicted one that says
		// "a_better". A work is contested only when one round wants the wiki
		// text and another refuses it.
		var pro, con int
		minDeciding := 1.0 // lowest confidence among the rounds that took a side
		for _, v := range vs {
			switch leaning(v.Verdict) {
			case 1:
				pro++
			case -1:
				con++
			default:
				continue // an abstention states no direction, so its
				// confidence is a confidence in NOT deciding. Folding it into
				// the gate would hold a decision hostage to a non-vote.
			}
			if v.Confidence < minDeciding {
				minDeciding = v.Confidence
			}
		}

		switch {
		case pro > 0 && con > 0:
			// One round wants the wiki text, another refuses it. This is the
			// real borderline case and the only one worth a human.
			st.Contested++
			out = append(out, unsureFold(folded, "各轮方向相反:"+verdictList(vs)))

		case pro*2 > len(vs) && con == 0:
			// A majority actively wanted it and nobody objected. Abstentions
			// alongside are tolerated, but they cannot BE the majority: one
			// lone vote among two abstentions is not a consensus.
			if pro == len(vs) {
				st.Unanimous++
			} else {
				st.Leaning++
			}
			folded = firstDeciding(vs)
			folded.Confidence = minDeciding
			out = append(out, folded)

		case con > 0 && pro == 0:
			// Nobody wanted it. Nothing is written either way, so this needs no
			// majority test — declining is the safe default, and the machine
			// lane fills the slot.
			st.Declined++
			out = append(out, firstDeciding(vs))

		default:
			// Mostly abstentions, or a single vote that no round seconded.
			st.Abstained++
			out = append(out, unsureFold(folded, "无人表态或表态不足半数:"+verdictList(vs)))
		}
	}
	return out, st
}

// Tiebreak lets a designated round decide the works the ordinary rounds
// CONTESTED, and nothing else. It is deliberately not just another round in
// Consensus: a fourth ordinary vote on a work already split 2-1 leaves it
// split, and the adversarial round is not an ordinary vote — it is the same
// question asked in a form that controls for the reason the others were
// unstable (adversarial.go).
//
// Its authority is therefore scoped: it may only speak where the fold said
// contested, it may not overturn a work the ordinary rounds decided, and a
// missing or abstaining tiebreak leaves the work in the review pile rather
// than silently resolving it.
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

// TiebreakStats reports the pass; every contested work lands in exactly one of
// the four outcome counters.
type TiebreakStats struct {
	Eligible        int
	ResolvedFor     int // the tiebreak wants the wiki text
	ResolvedAgainst int // it does not — no write, and no human either
	StillUnsure     int
	NoTiebreak      int
}

func (s TiebreakStats) String() string {
	return fmt.Sprintf("eligible=%d resolved_for=%d resolved_against=%d still_unsure=%d no_tiebreak=%d",
		s.Eligible, s.ResolvedFor, s.ResolvedAgainst, s.StillUnsure, s.NoTiebreak)
}

// ConsensusStats reports the fold, so a run never silently shrinks its
// population: every work is in exactly one of these counts.
type ConsensusStats struct {
	Rounds     int
	Works      int
	Unanimous  int // every round took the same side
	Leaning    int // a majority took one side, the rest abstained, none objected
	Declined   int // nobody wanted the wiki text
	Contested  int // rounds pointed opposite ways — the review pile
	Abstained  int // nobody took a side, or too few did
	Incomplete int
}

func (s ConsensusStats) String() string {
	return fmt.Sprintf("rounds=%d works=%d unanimous=%d leaning=%d declined=%d contested=%d abstained=%d incomplete=%d",
		s.Rounds, s.Works, s.Unanimous, s.Leaning, s.Declined, s.Contested, s.Abstained, s.Incomplete)
}

// firstDeciding returns the first round that took a side, so the folded verdict
// carries a real label and that round's reason rather than an abstention's.
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
