// Package workpop renders the catalog work POPULATION a maintenance lane runs
// over as a SQL predicate.
//
// Every enrichment lane needs the same four sets, and each one that spelled the
// predicate out privately became a place the definition could drift from
// model.ClaimStateKey — the read face's authority on what "published" means.
// Three copies existed before this package (bgmsummaries, intromt, dlsitemedia)
// and the drift risk is not hypothetical: wave 161 renamed the only value `site`
// has ever held (galgame_wiki → kungal), and the lanes that had pinned that
// literal silently selected nothing.
//
// PUBLISHED is the narrow, product-facing set: claimed AND actually on the
// public face. It is NOT a synonym for claimed — in prod, claimed is ~64.5k
// works of which only ~11k are published; the rest are the draft sea, which
// this track has repeatedly declined to spend translation budget on (the step-75
// ruling). The predicate mirrors ClaimStateKey's `live` rule exactly, including
// its two easily-missed halves: a NULL claim_state is live (a claimed row no
// projector has stamped yet), and a row without product_work_id reads as
// unclaimed on the wire and must filter as unclaimed here.
package workpop

import "fmt"

type Population string

const (
	All       Population = "all"
	Bodyless  Population = "bodyless"
	Claimed   Population = "claimed"
	Published Population = "published"
)

func Predicate(pop Population, alias string) (string, error) {
	q := ""
	if alias != "" {
		q = alias + "."
	}
	switch pop {
	case All, "":
		return "TRUE", nil
	case Bodyless:
		return fmt.Sprintf("(%[1]ssite IS NULL OR %[1]ssite = '')", q), nil
	case Claimed:
		return fmt.Sprintf("(%[1]ssite IS NOT NULL AND %[1]ssite <> '')", q), nil
	case Published:
		return fmt.Sprintf(`(%[1]ssite IS NOT NULL AND %[1]ssite <> '' AND %[1]sproduct_work_id IS NOT NULL
			AND (%[1]sclaim_state IS NULL OR %[1]sclaim_state = 0))`, q), nil
	default:
		return "", fmt.Errorf("unknown population %q (want %s|%s|%s|%s)", pop, All, Bodyless, Claimed, Published)
	}
}
