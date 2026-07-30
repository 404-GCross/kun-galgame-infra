package wikirescue

import (
	"context"
	"fmt"
	"strings"

	"gorm.io/gorm"
)

// introZhColumns are the wiki body's Chinese intro columns, in the order the read
// face prefers them: zh_cn is the editorial default, zh_tw the fallback the bridge
// consulted when zh_cn was blank. The HELD column of a body is the first of these
// two carrying text, and it is the column step u empties and step v adopts from.
var introZhColumns = []struct{ Column, Lang string }{
	{"intro_zh_cn", "zh-Hans"},
	{"intro_zh_tw", "zh-Hant"},
}

// zhOnlyBody is one CLAIMED wiki body whose ja AND en intro columns are blank
// while a zh column holds text — the candidate class both step u and step v are
// carved out of, together with the kana verdict on that text.
type zhOnlyBody struct {
	galgameID int64
	workID    int64
	// column / lang are the held zh column and the BCP-47 tag it maps to.
	column, lang string
	// raw is the held column's value VERBATIM (never the trimmed one): emptiness
	// is judged on the trimmed text, but the text that moves or lands is the raw
	// column, exactly as step q treats the ja/en columns.
	raw     string
	class   kanaClass
	density float64
	cjk     int
}

// parkedGrayIntro is one body the kana classifier could not judge — the gray band
// (bilingual fields, and fields too short for the density to be evidence). NEITHER
// step u nor step v touches these; the artifact carries the evidence a human needs
// to rule on them, including the head of the text itself.
type parkedGrayIntro struct {
	WorkID    int64   `json:"work_id"`
	GalgameID int64   `json:"galgame_id"`
	Column    string  `json:"column"`
	Density   float64 `json:"kana_density"`
	CJK       int     `json:"cjk_chars"`
	Head      string  `json:"head"`
}

// grayHeadRunes is how much of a parked text the artifact carries — enough to
// recognize the language at a glance without turning the JSON into a second copy
// of the wiki table.
const grayHeadRunes = 80

// introManualGray pins specific bodies to the GRAY verdict, overriding the kana
// density. The density is blind to composition: a Chinese annotation dominated by
// QUOTED Japanese release titles reads as Japanese by sheer volume. Known cases
// (acceptance finding E, refs/proj/146 §6.6) are parked with the measured gray
// band — step u must not move them, step v must not adopt them.
var introManualGray = map[int64]bool{
	// A Chinese release annotation quoting four Japanese titles: the quoted text
	// alone pushes the density past kanaJaDensity.
	2776: true,
}

// zhOnlyClaimedBodies resolves the candidate class shared by steps u and v:
// claimed bodies with a blank ja column, a blank en column, and text in a zh one.
//
// The emptiness test is strings.TrimSpace — Go's Unicode-wide definition, the
// same one the retired read-time bridge used (an ideographic space is whitespace;
// SQL btrim would have kept it and called the column non-blank).
func (r *Runner) zhOnlyClaimedBodies(ctx context.Context, span claimSpan) ([]zhOnlyBody, error) {
	var bodies []struct {
		ID        int64  `gorm:"column:id"`
		IntroJaJP string `gorm:"column:intro_ja_jp"`
		IntroEnUS string `gorm:"column:intro_en_us"`
		IntroZhCN string `gorm:"column:intro_zh_cn"`
		IntroZhTW string `gorm:"column:intro_zh_tw"`
	}
	if err := r.galgame.WithContext(ctx).Raw(
		`SELECT id, intro_ja_jp, intro_en_us, intro_zh_cn, intro_zh_tw
		 FROM galgame ORDER BY id`).Scan(&bodies).Error; err != nil {
		return nil, fmt.Errorf("read galgame intros: %w", err)
	}
	out := make([]zhOnlyBody, 0, 64)
	for _, b := range bodies {
		workID, ok := span.byGalgame[b.ID]
		if !ok {
			continue // not a claim — the read face never showed this body either
		}
		if strings.TrimSpace(b.IntroJaJP) != "" || strings.TrimSpace(b.IntroEnUS) != "" {
			continue // an original survives; the zh column is a translation (ruling ①)
		}
		byCol := map[string]string{"intro_zh_cn": b.IntroZhCN, "intro_zh_tw": b.IntroZhTW}
		for _, p := range introZhColumns {
			raw := byCol[p.Column]
			if strings.TrimSpace(raw) == "" {
				continue
			}
			class, density, cjk := classifyKana(raw)
			if introManualGray[b.ID] {
				class = kanaGray
			}
			out = append(out, zhOnlyBody{
				galgameID: b.ID, workID: workID, column: p.Column, lang: p.Lang,
				raw: raw, class: class, density: density, cjk: cjk,
			})
			break // the FIRST non-blank column is the held one
		}
	}
	return out, nil
}

// stepIntroLangFix is step u — LANGUAGE REATTRIBUTION (refs/proj/146 §1).
//
// A claimed body whose ja and en intro columns are blank while its zh column
// holds JAPANESE text is a data-entry error: the original was typed into the
// translation's column. This step moves it back —
// `intro_ja_jp := <the misfiled text>` and the held zh column emptied — and
// nothing else.
//
// IT WRITES THE WIKI SOURCE TABLE. That makes it the first step in this package
// to write `galgame`, and the legality is doc 140 §3's single-persistent-writer
// invariant read forwards: catalog_work_intro's (claimed × galgame_wiki ×
// {ja,en} × provenance=source) rows are step q's property, so while the wiki
// table is still alive a correction to that data has to be made UPSTREAM of q and
// materialized by q's next pass.
//
// ⛔ The red line this step exists to respect: inserting the ja row directly into
// catalog_work_intro would land it INSIDE step q's ownership scope
// (intromirror.go, scope=claimed×wiki×{ja,en}×provenance=source) with an empty
// wiki ja column behind it — so q's set-diff would DELETE it on the next daily
// 06:10 run. The fix has to go to the source, or it silently evaporates.
//
// Consequences of writing the wiki table, both intended:
//
//   - the projection is q's job, so u touches NO catalog table and bumps NO
//     catalog_work.updated_at. Run order is u → q; ParseSteps sorts q first
//     within one invocation, so the two are SEPARATE invocations.
//   - `galgame.updated` is bumped, which is the wiki face's own change marker.
//
// One-shot, like step t: never in the daily chain, never in the W1 final sweep.
// Idempotent all the same — a reattributed body now has a non-blank ja column and
// leaves the candidate class, so a second pass plans zero.
func (r *Runner) stepIntroLangFix(ctx context.Context) (Stats, error) {
	st := Stats{Step: "u"}

	span, err := r.claimedSpan(ctx)
	if err != nil {
		return st, err
	}
	cands, err := r.zhOnlyClaimedBodies(ctx, span)
	if err != nil {
		return st, err
	}
	st.Source = len(cands)

	type fix struct {
		galgameID int64
		column    string
		text      string
	}
	fixes := make([]fix, 0, len(cands))
	parked := make([]parkedGrayIntro, 0)
	zh := 0
	for _, c := range cands {
		switch c.class {
		case kanaJa:
			fixes = append(fixes, fix{galgameID: c.galgameID, column: c.column, text: c.raw})
		case kanaGray:
			parked = append(parked, parkedGrayIntro{
				WorkID: c.workID, GalgameID: c.galgameID, Column: c.column,
				Density: c.density, CJK: c.cjk, Head: head(c.raw, grayHeadRunes),
			})
		default:
			zh++ // a real Chinese translation — step v's business, not this step's
		}
	}
	st.Anchored = len(fixes)
	st.Parked = len(parked)
	st.Planned = len(fixes)
	st.Note = fmt.Sprintf("one-shot language reattribution of the WIKI source table (never the daily chain); candidates=%d japanese=%d gray-parked=%d chinese-left-to-v=%d; kana thresholds ja>=%.2f zh<%.2f min-cjk=%d; materialization is step q's",
		st.Source, len(fixes), len(parked), zh, kanaJaDensity, kanaGrayDensity, kanaMinCJK)

	if err := r.park("u-gray", parked); err != nil {
		return st, err
	}
	if !r.opts.Apply || len(fixes) == 0 {
		return st, nil
	}
	err = r.galgame.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for _, f := range fixes {
			// The column name comes from introZhColumns, never from caller input —
			// the same fixed-internal-SQL rule the mirror engine's table names follow.
			res := tx.Exec(
				`UPDATE galgame SET intro_ja_jp = ?, `+f.column+` = '', updated = now() WHERE id = ?`,
				f.text, f.galgameID)
			if res.Error != nil {
				return fmt.Errorf("reattribute galgame %d: %w", f.galgameID, res.Error)
			}
			st.Written += int(res.RowsAffected)
		}
		return nil
	})
	return st, err
}

// head returns a text's first n runes, so a parked sample is bounded by
// CHARACTERS and never splits a multi-byte rune the way a byte slice would.
func head(s string, n int) string {
	rs := []rune(s)
	if len(rs) <= n {
		return s
	}
	return string(rs[:n])
}
