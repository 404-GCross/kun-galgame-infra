package service

// Omni-moderation category policy (spec 09 ruling 2): "policy is code". Every
// OpenAI moderation category is ADOPTED except bare `sexual` — that is a galgame
// rating-system matter (an age-gate/tagging concern), NOT a safety line, and is
// deliberately ignored so ordinary adult content does not escalate. `sexual/minors`
// IS adopted: it is the legal line, not a rating matter. relevantScore is the max
// category_score across the adopted set; the cascade escalates/convicts on that
// value alone, never on the ignored category.
var adoptedCategoryList = []string{
	"harassment",
	"harassment/threatening",
	"hate",
	"hate/threatening",
	"illicit",
	"illicit/violent",
	"self-harm",
	"self-harm/intent",
	"self-harm/instructions",
	"sexual/minors",
	"violence",
	"violence/graphic",
}

// relevantScore is the max category_score across the ADOPTED categories only —
// the ignored `sexual` never contributes. Absent categories score 0, so an
// all-clean (or sexual-only) verdict yields a low relevantScore that stays below
// the escalation threshold.
func relevantScore(scores map[string]float64) float32 {
	var best float64
	for _, cat := range adoptedCategoryList {
		if s, ok := scores[cat]; ok && s > best {
			best = s
		}
	}
	return float32(best)
}

// adoptedTrueCategories returns the adopted categories omni marked true, in the
// canonical policy order (deterministic). Used when omni convicts alone (the LLM
// tier is unconfigured); the ignored `sexual` is never included.
func adoptedTrueCategories(cats map[string]bool) []string {
	var out []string
	for _, cat := range adoptedCategoryList {
		if cats[cat] {
			out = append(out, cat)
		}
	}
	return out
}
