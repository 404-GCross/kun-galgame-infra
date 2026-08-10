package service

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

func relevantScore(scores map[string]float64) float32 {
	var best float64
	for _, cat := range adoptedCategoryList {
		if s, ok := scores[cat]; ok && s > best {
			best = s
		}
	}
	return float32(best)
}

func adoptedTrueCategories(cats map[string]bool) []string {
	var out []string
	for _, cat := range adoptedCategoryList {
		if cats[cat] {
			out = append(out, cat)
		}
	}
	return out
}
