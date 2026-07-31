package editspec

import (
	"context"
	"fmt"
	"strings"

	catmodel "api/internal/platform/catalog/model"

	"gorm.io/gorm"
)

// catalog.work.intros — the work's synopsis in the four supported languages,
// as ONE list field (03 定案 §2).
//
// Canonical element shape:
//
//	{"lang": <en|ja|zh-Hans|zh-Hant>, "intro": <non-empty>}
//
// The lane, and why the field is safe to full-replace: an intro row is keyed
// (work_id, lang, source_id), and this field owns exactly the rows with
// source_id = curated AND provenance = SOURCE. Everything else on the table is
// somebody else's property and is never read, written or deleted here:
//
//   - importer rows carry their own source_id (vndb / dlsite / bangumi …);
//   - machine-translation rows carry provenance = 1. The intro-MT lane is
//     already immune from the other side too — its fill-missing candidate query
//     skips any work holding a zh-Hans/zh-Hant provenance=0 row, and its upsert
//     is guarded `WHERE provenance = 1` — so a curated zh intro both wins the
//     read face (provenance ascending) and stops the machine lane from
//     regenerating one. That is the "人工天然压过 MT" property the matrix
//     claims, verified against internal/jobs/intromt rather than assumed.
//
// zh is a first-class editable language here, which is the point of ruling ①
// (2026-07-29): the wiki's zh bodies were discarded because they were
// translations with no upstream, and the editing face is where a human-written
// zh synopsis becomes real data again.
var introLangs = []string{"en", "ja", "zh-Hans", "zh-Hant"}

type workIntro struct {
	Lang  string
	Intro string
}

func parseIntros(v any) ([]workIntro, error) {
	arr, err := asArray(v, "intro objects")
	if err != nil {
		return nil, err
	}
	out := make([]workIntro, 0, len(arr))
	seen := make(map[string]struct{}, len(arr))
	for i, el := range arr {
		obj, err := asObject(el, i, "lang", "intro")
		if err != nil {
			return nil, err
		}
		lang, err := objString(obj, "lang", i, true, 32)
		if err != nil {
			return nil, err
		}
		allowed := false
		for _, l := range introLangs {
			if l == lang {
				allowed = true
				break
			}
		}
		if !allowed {
			return nil, fmt.Errorf("element %d: lang must be one of %s", i, strings.Join(introLangs, ", "))
		}
		if _, dup := seen[lang]; dup {
			return nil, fmt.Errorf("element %d: duplicate lang %q", i, lang)
		}
		seen[lang] = struct{}{}
		intro, err := objString(obj, "intro", i, true, maxIntroRunes)
		if err != nil {
			return nil, err
		}
		out = append(out, workIntro{Lang: lang, Intro: intro})
	}
	return out, nil
}

func validateIntros(v any) error {
	_, err := parseIntros(v)
	return err
}

// applyIntros full-replaces the CURATED source rows of this work's intros. An
// empty array is legal and means "this work has no curated synopsis", which is
// how a language gets removed — the read face then falls back to whatever
// importer or machine row exists, exactly as it does for a work nobody edited.
func applyIntros(ctx context.Context, tx *gorm.DB, entityID int64, value any) error {
	intros, err := parseIntros(value)
	if err != nil {
		return fmt.Errorf("editspec: intros: %w", err)
	}
	if err := assertWorkExists(ctx, tx, entityID); err != nil {
		return err
	}
	// The delete scope is the curated lane's SOURCE rows (the full replace)
	// PLUS any curated machine row in a language this edit writes.
	//
	// That second half is forced by the table's key: uq_catalog_work_intro is
	// (work_id, lang, source_id) — provenance is NOT part of it — so at most
	// one row can occupy a (lang, curated) coordinate. A machine translation
	// attributed to this source is exactly what a human writing that language
	// supersedes: the read face already prefers provenance 0, and the MT lane
	// will not regenerate the row while a source row exists. Machine rows in
	// languages this edit does NOT write are untouched.
	langs := make([]string, 0, len(intros))
	for _, in := range intros {
		langs = append(langs, in.Lang)
	}
	del := tx.WithContext(ctx).
		Where("work_id = ? AND source_id = ?", entityID, curatedSourceID).
		Where("provenance = ? OR lang IN ?", catmodel.IntroProvenanceSource, langs)
	if len(langs) == 0 {
		del = tx.WithContext(ctx).
			Where("work_id = ? AND source_id = ? AND provenance = ?",
				entityID, curatedSourceID, catmodel.IntroProvenanceSource)
	}
	if err := del.Delete(&catmodel.CatalogWorkIntro{}).Error; err != nil {
		return err
	}
	if len(intros) == 0 {
		return nil
	}
	rows := make([]catmodel.CatalogWorkIntro, 0, len(intros))
	for _, in := range intros {
		rows = append(rows, catmodel.CatalogWorkIntro{
			WorkID: entityID, Lang: in.Lang, Intro: in.Intro,
			SourceID: curatedSourceID, Provenance: catmodel.IntroProvenanceSource,
		})
	}
	return tx.WithContext(ctx).Create(&rows).Error
}

// loadIntros reads the curated lane back as the canonical field value, ordered
// by the declared language order so the value round-trips independently of
// insertion order (unlike titles, intros carry no meaningful sequence).
func loadIntros(ctx context.Context, db *gorm.DB, workID int64) ([]any, error) {
	var rows []catmodel.CatalogWorkIntro
	if err := db.WithContext(ctx).
		Where("work_id = ? AND source_id = ? AND provenance = ?",
			workID, curatedSourceID, catmodel.IntroProvenanceSource).
		Find(&rows).Error; err != nil {
		return nil, err
	}
	byLang := make(map[string]string, len(rows))
	for _, r := range rows {
		byLang[r.Lang] = r.Intro
	}
	out := make([]any, 0, len(rows))
	for _, lang := range introLangs {
		if intro, ok := byLang[lang]; ok {
			out = append(out, map[string]any{"lang": lang, "intro": intro})
		}
	}
	return out, nil
}
