package wikirescue

import (
	"context"
	"fmt"
	"strings"

	"api/internal/platform/catalog/model"

	"gorm.io/gorm"
)

// introProvenanceSource is catalog_work_intro.provenance for UPSTREAM SOURCE
// text (1 = machine translation, the intromt lane's property). Every row this
// step writes is source text pivoted out of the wiki body.
const introProvenanceSource int16 = 0

// introMirrorLangs are the two languages step q mirrors, in the wiki body's
// column order. zh_cn / zh_tw are DELIBERATELY absent: ruling ① of 2026-07-29
// discards the claimed Chinese intros wholesale (the wiki's zh bodies are
// themselves translations with no upstream, the final archival dump preserves
// them, and the follow-up intromt wave re-derives zh-Hans from the ja original).
// A claimed work's Chinese intro disappearing from the read face after the flip
// is therefore an INTENDED change, recorded in the acceptance ledger — not a gap.
var introMirrorLangs = []struct{ Column, Lang string }{
	{"intro_ja_jp", "ja"},
	{"intro_en_us", "en"},
}

// introKey identifies one catalog_work_intro row: the table's unique key
// uq_catalog_work_intro(work_id, lang, source_id).
type introKey struct {
	workID int64
	lang   string
	srcID  int16
}

// introVal is the body text step q owns. provenance is pinned to source (0) on
// every mirrored row, so it is not a diffable value — it is part of the scope.
type introVal struct{ intro string }

// stepIntroMirror mirrors the CLAIMED works' wiki intro columns into
// catalog_work_intro (step q, refs/proj/140 §2.2) AND, riding the same body read,
// the EDITORIAL DISPLAY axis onto catalog_work.display_nsfw (§5b).
//
// The two arms share a step because they share a source query: both are
// projections of the galgame row this step already has in hand, and reading the
// 60k-row body table once instead of twice is the whole reason the display axis
// did not get a step letter of its own.
//
// The read face used to pivot galgame.intro_* into language rows attributed to
// galgame_wiki (service.bridgeGalgameIntros). This step materializes the ja/en
// half of that pivot verbatim, including its two exact quirks:
//
//   - the EMPTINESS TEST is on the TRIMMED text, but the STORED value is the RAW
//     column (the bridge emits `byCol[p.Column]`, not the trimmed string, after
//     testing the trimmed one). Copying the trim into the value would rewrite
//     leading/trailing whitespace on the wire.
//   - attribution is galgame_wiki for every row: the wiki body has no per-source
//     column, so the bridge attributes the whole pivot to the first-party source.
//
// OWNERSHIP SCOPE — (claimed work, source=galgame_wiki, lang ∈ {ja,en},
// provenance=SOURCE). provenance=1 rows are the intromt wave's property and are
// never read, updated or deleted here.
//
// One subtlety the scope query has to handle: provenance is NOT part of
// uq_catalog_work_intro(work_id, lang, source_id). So a machine row could in
// principle occupy a key this step wants to insert into. Such a key is SKIPPED
// (counted, never overwritten) rather than stolen — the machine row's owner
// decides its fate, and a silent ON CONFLICT DO NOTHING would hide the collision.
func (r *Runner) stepIntroMirror(ctx context.Context) (Stats, error) {
	st := Stats{Step: "q"}

	span, err := r.claimedSpan(ctx)
	if err != nil {
		return st, err
	}

	var bodies []struct {
		ID           int64  `gorm:"column:id"`
		IntroJaJP    string `gorm:"column:intro_ja_jp"`
		IntroEnUS    string `gorm:"column:intro_en_us"`
		ContentLimit string `gorm:"column:content_limit"`
	}
	if err := r.galgame.WithContext(ctx).Raw(
		`SELECT id, intro_ja_jp, intro_en_us, coalesce(content_limit, '') AS content_limit
		 FROM galgame ORDER BY id`).Scan(&bodies).Error; err != nil {
		return st, fmt.Errorf("read galgame intros: %w", err)
	}
	desired := newMirrorDesired[introKey, introVal](len(bodies))
	// wantNSFW is the display arm's desired set, over the WHOLE claimed population:
	// a claimed work whose body is gone declares nothing, which is false — the same
	// value the bridge's EXISTS predicate and its absent-from-the-map loader both
	// produced for it.
	wantNSFW := make(map[int64]bool, len(span.byGalgame))
	for _, workID := range span.byGalgame {
		wantNSFW[workID] = false
	}
	for _, b := range bodies {
		workID, ok := span.byGalgame[b.ID]
		if !ok {
			continue
		}
		wantNSFW[workID] = b.ContentLimit == model.WikiContentLimitNSFW
		byCol := map[string]string{"intro_ja_jp": b.IntroJaJP, "intro_en_us": b.IntroEnUS}
		for _, p := range introMirrorLangs {
			raw := byCol[p.Column]
			if strings.TrimSpace(raw) == "" {
				continue // the bridge tests the trimmed text …
			}
			st.Source++
			// … and stores the RAW one.
			desired.put(introKey{workID: workID, lang: p.Lang, srcID: r.wikiSrc}, introVal{intro: raw})
		}
	}

	// ── existing: the unique-key space this step writes into, provenance carried
	// so a machine row can be recognized and left alone.
	var scope []struct {
		ID         int64  `gorm:"column:id"`
		WorkID     int64  `gorm:"column:work_id"`
		Lang       string `gorm:"column:lang"`
		SourceID   int16  `gorm:"column:source_id"`
		Intro      string `gorm:"column:intro"`
		Provenance int16  `gorm:"column:provenance"`
	}
	if err := r.catalog.WithContext(ctx).Raw(
		`SELECT t.id, t.work_id, t.lang, t.source_id, t.intro, t.provenance FROM catalog_work_intro t
		 `+claimedScopeJoin+` WHERE t.source_id = ? AND t.lang IN ?`,
		siteGalgameWiki, r.wikiSrc, []string{"ja", "en"}).Scan(&scope).Error; err != nil {
		return st, fmt.Errorf("read catalog_work_intro scope: %w", err)
	}
	existing := make(map[introKey]mirrorExisting[introVal], len(scope))
	for _, x := range scope {
		k := introKey{workID: x.WorkID, lang: x.Lang, srcID: x.SourceID}
		if x.Provenance != introProvenanceSource {
			// A machine translation holds this key. Not ours: drop our desired row
			// too, so the insert cannot collide and the row stays untouched.
			desired.drop(k)
			st.Skipped++
			continue
		}
		existing[k] = mirrorExisting[introVal]{id: x.ID, val: introVal{intro: x.Intro}}
	}

	spec := mirrorSpec[introKey, introVal]{
		table:      "catalog_work_intro",
		insertCols: []string{"work_id", "lang", "intro", "source_id", "provenance", "src_hash", "mt_model", "created_at", "updated_at"},
		rowOf: func(k introKey, v introVal) []any {
			return []any{k.workID, k.lang, v.intro, k.srcID, introProvenanceSource, "", "", r.now, r.now}
		},
		updateCols: []string{"intro", "updated_at"},
		updateArgs: func(v introVal) []any { return []any{v.intro, r.now} },
		workOf:     func(k introKey) int64 { return k.workID },
		same:       func(a, b introVal) bool { return a.intro == b.intro },
	}
	ledger := newMirrorLedger()
	if err := runMirror(ctx, r, ledger, spec, desired, existing); err != nil {
		return st, err
	}

	flipped, err := r.mirrorDisplayNSFW(ctx, ledger, wantNSFW)
	if err != nil {
		return st, err
	}
	ledger.into(&st)
	st.Scalar = flipped
	st.Note = fmt.Sprintf("scope=claimed×galgame_wiki×{ja,en}×provenance=source; machine-held keys skipped=%d; zh_cn/zh_tw discarded by ruling ①; catalog_work.display_nsfw flipped=%d", st.Skipped, flipped)
	return st, nil
}

// mirrorDisplayNSFW brings catalog_work.display_nsfw in step with the wiki bodies'
// editorial display flag — the A2-R5 axis, nativized (refs/proj/140 §5b). want is
// keyed by CATALOG work id and covers the whole claimed population.
//
// A WORK-SCALAR mirror, so it is not runMirror: there is no row to insert and none
// to delete (the work exists either way), only a column to correct in both
// directions. That makes the diff a two-armed id list, the shape
// mirrorTagVocabularySexual already uses for the tag vocabulary.
//
// OWNERSHIP SCOPE — the column on CLAIMED rows. A bodyless work's false is never
// written here and never read either: DisplayLimitKey consults the age axis for it.
// The UPDATE bumps updated_at itself, which IS the changes-feed touch (repository.
// TouchWorks does exactly that), so the flipped works are marked in the step's
// ledger rather than touched a second time.
func (r *Runner) mirrorDisplayNSFW(ctx context.Context, l *mirrorLedger, want map[int64]bool) (int, error) {
	var current []struct {
		ID          int64 `gorm:"column:id"`
		DisplayNSFW bool  `gorm:"column:display_nsfw"`
	}
	if err := r.catalog.WithContext(ctx).Raw(
		`SELECT id, display_nsfw FROM catalog_work
		 WHERE site = ? AND product_work_id IS NOT NULL AND deleted_at IS NULL
		 ORDER BY id`, siteGalgameWiki).Scan(&current).Error; err != nil {
		return 0, fmt.Errorf("read catalog_work.display_nsfw scope: %w", err)
	}
	var promote, demote []int64
	for _, w := range current {
		switch {
		case want[w.ID] && !w.DisplayNSFW:
			promote = append(promote, w.ID)
		case !want[w.ID] && w.DisplayNSFW:
			demote = append(demote, w.ID)
		}
	}
	for _, ids := range [][]int64{promote, demote} {
		for _, id := range ids {
			l.touched[id] = struct{}{}
		}
	}
	changed := len(promote) + len(demote)
	if !r.opts.Apply || changed == 0 {
		return changed, nil
	}
	return changed, r.catalog.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for _, arm := range []struct {
			ids []int64
			val bool
		}{{promote, true}, {demote, false}} {
			for start := 0; start < len(arm.ids); start += paramChunk {
				end := min(start+paramChunk, len(arm.ids))
				if err := tx.Exec(
					`UPDATE catalog_work SET display_nsfw = ?, updated_at = now() WHERE id IN ?`,
					arm.val, arm.ids[start:end]).Error; err != nil {
					return fmt.Errorf("update catalog_work.display_nsfw: %w", err)
				}
			}
		}
		return nil
	})
}
