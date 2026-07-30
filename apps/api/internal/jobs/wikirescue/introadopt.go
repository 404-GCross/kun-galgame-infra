package wikirescue

import (
	"context"
	"fmt"
	"strings"

	"api/internal/platform/catalog/repository"

	"gorm.io/gorm"
)

// introAdoptMinRunes is the minimum trimmed length for a zh column to count as a
// translation at all. The class's short tail is editor STUBS ("-", "无", "***",
// "截图" — acceptance finding D, refs/proj/146 §6.6) whose adoption would render
// a broken-looking face where blank is honest; the archival dump keeps the stubs
// (ruling ②), so nothing is lost by not adopting them.
const introAdoptMinRunes = 4

// stepIntroOrphanAdopt is step v — ORPHAN zh ADOPTION (refs/proj/146 §2, user
// ruling of 2026-07-30: keep every one of them).
//
// Ruling ① discards the claimed works' Chinese intros because they are
// translations whose ja/en original survives in the same body — the archival dump
// keeps them and the intromt wave re-derives zh from the original. This step is
// the ruling's carve-out: a claimed work whose ONLY intro text anywhere is that
// Chinese column has no original to re-derive from, so discarding it is not
// "dropping a translation", it is deleting the sole copy of a hand-written
// description. Those are adopted as first-class zh SOURCE rows.
//
// PREDICATE — the machine definition of "no original exists anywhere":
//
//	claimed ∧ wiki ja blank ∧ wiki en blank ∧ a zh column holds text
//	        ∧ the kana verdict on that text is CHINESE
//	        ∧ the work has NO catalog_work_intro row at all (any lang, any source,
//	          any provenance)
//
// The last clause is what makes this an ORPHAN rescue rather than a second read of
// the discarded population: a work carrying a bangumi summary or a dlsite blurb
// still has an original the MT lane can work from.
//
// The kana verdict is re-run here rather than assumed from step u's pass. Run
// order is u → q → v and a reattributed body has left the class by then anyway
// (its zh column is empty), but a v run on a database where u has NOT run would
// otherwise adopt Japanese text under a zh tag — a mislabel this step must not be
// able to produce, whatever order it is invoked in. GRAY bodies are skipped for
// the same reason: they are parked for adjudication, not silently filed as
// Chinese.
//
// MECHANISM SAFETY — the reason this needs no new protection anywhere else:
//
//   - zh is NOT in step q's lang scope ({ja,en}), so neither the daily chain nor
//     the W1 final sweep can see, update or delete these rows.
//   - a provenance=source zh row makes the work fail intromt's fill-missing
//     predicate, so adoption AUTOMATICALLY outranks machine translation — no MT
//     code changes, no exclusion list.
//
// The rows are byte-identical in shape to step q's mirrored rows (source =
// galgame_wiki, provenance = source, empty src_hash / mt_model) because they are
// the same kind of fact: first-party wiki text pivoted out of a body column.
//
// One-shot, like steps t and u: not in the daily chain, not in the W1 final sweep.
// Idempotent by the orphan clause itself — an adopted work now HAS an intro row,
// so a second pass plans zero.
func (r *Runner) stepIntroOrphanAdopt(ctx context.Context) (Stats, error) {
	st := Stats{Step: "v"}

	span, err := r.claimedSpan(ctx)
	if err != nil {
		return st, err
	}
	cands, err := r.zhOnlyClaimedBodies(ctx, span)
	if err != nil {
		return st, err
	}
	st.Source = len(cands)

	// The works that already carry intro text of any kind — the whole table, not
	// the wiki-attributed slice: a bangumi summary is an original too.
	var haveIntro []int64
	if err := r.catalog.WithContext(ctx).Raw(
		`SELECT DISTINCT work_id FROM catalog_work_intro`).Scan(&haveIntro).Error; err != nil {
		return st, fmt.Errorf("read works with intro rows: %w", err)
	}
	sourced := make(map[int64]struct{}, len(haveIntro))
	for _, id := range haveIntro {
		sourced[id] = struct{}{}
	}

	rows := make([][]any, 0, len(cands))
	var hant, skippedNonJa, placeholders int
	for _, c := range cands {
		if c.class != kanaZh {
			skippedNonJa++ // japanese (step u's) or gray (parked, awaiting a ruling)
			continue
		}
		st.Anchored++
		if len([]rune(strings.TrimSpace(c.raw))) < introAdoptMinRunes {
			placeholders++ // an editor's stub, not a translation
			continue
		}
		if _, ok := sourced[c.workID]; ok {
			st.Skipped++ // an original exists elsewhere → ruling ① discards this text
			continue
		}
		if c.lang == "zh-Hant" {
			hant++
		}
		rows = append(rows, []any{c.workID, c.lang, c.raw, r.wikiSrc, introProvenanceSource, "", "", r.now, r.now})
	}
	st.Planned = len(rows)
	st.Note = fmt.Sprintf("one-shot orphan adoption (never the daily chain); candidates=%d chinese=%d orphans-adopted=%d (zh-Hant %d) non-orphan-discarded=%d not-chinese=%d placeholders-skipped=%d; zh is outside step q's lang scope, so these rows are structurally safe from the mirror",
		st.Source, st.Anchored, len(rows), hant, st.Skipped, skippedNonJa, placeholders)

	if !r.opts.Apply || len(rows) == 0 {
		return st, nil
	}
	err = r.catalog.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		landed, err := insertReturning(tx, "catalog_work_intro",
			[]string{"work_id", "lang", "intro", "source_id", "provenance", "src_hash", "mt_model", "created_at", "updated_at"},
			"work_id", rows)
		if err != nil {
			return err
		}
		st.Written = len(landed)
		if len(landed) == 0 {
			return nil
		}
		if err := repository.TouchWorks(ctx, tx, landed); err != nil {
			return fmt.Errorf("touch works: %w", err)
		}
		st.Touched = distinctCount(landed)
		return nil
	})
	return st, err
}
