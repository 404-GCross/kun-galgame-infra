package editspec

import (
	"context"
	"fmt"

	catmodel "api/internal/platform/catalog/model"
	"api/internal/platform/editing"

	"gorm.io/gorm"
)

// The three vocabulary intro tables are the same table three times: an owner
// id, a lang, an intro, a source_id, unique on (owner, lang, source). One
// description of that shape drives all three Applies, because three copies of
// the same twenty lines is how the curated-lane predicate ends up subtly
// different in exactly one of them.
//
// Unlike catalog_work_intro these carry NO provenance column — there is no
// machine-translation lane for vocabulary text — so the curated lane here is
// simply source_id = curated.
type introTable struct {
	ownerCol string
	// empty is a zero model value, used as GORM's table handle for delete.
	empty func() any
	// newRow builds one curated row.
	newRow func(ownerID int64, lang, intro string) any
	// read returns the owner's curated rows as lang→intro.
	read func(ctx context.Context, db *gorm.DB, ownerID int64) (map[string]string, error)
}

var introTableLabel = introTable{
	ownerCol: "label_id",
	empty:    func() any { return &catmodel.CatalogLabelIntro{} },
	newRow: func(id int64, lang, intro string) any {
		return &catmodel.CatalogLabelIntro{LabelID: id, Lang: lang, Intro: intro, SourceID: curatedSourceID}
	},
	read: func(ctx context.Context, db *gorm.DB, ownerID int64) (map[string]string, error) {
		var rows []catmodel.CatalogLabelIntro
		if err := curatedIntroScope(ctx, db, "label_id", ownerID).Find(&rows).Error; err != nil {
			return nil, err
		}
		out := make(map[string]string, len(rows))
		for _, r := range rows {
			out[r.Lang] = r.Intro
		}
		return out, nil
	},
}

var introTableTag = introTable{
	ownerCol: "tag_id",
	empty:    func() any { return &catmodel.CatalogTagIntro{} },
	newRow: func(id int64, lang, intro string) any {
		return &catmodel.CatalogTagIntro{TagID: id, Lang: lang, Intro: intro, SourceID: curatedSourceID}
	},
	read: func(ctx context.Context, db *gorm.DB, ownerID int64) (map[string]string, error) {
		var rows []catmodel.CatalogTagIntro
		if err := curatedIntroScope(ctx, db, "tag_id", ownerID).Find(&rows).Error; err != nil {
			return nil, err
		}
		out := make(map[string]string, len(rows))
		for _, r := range rows {
			out[r.Lang] = r.Intro
		}
		return out, nil
	},
}

var introTableSeries = introTable{
	ownerCol: "series_id",
	empty:    func() any { return &catmodel.CatalogSeriesIntro{} },
	newRow: func(id int64, lang, intro string) any {
		return &catmodel.CatalogSeriesIntro{SeriesID: id, Lang: lang, Intro: intro, SourceID: curatedSourceID}
	},
	read: func(ctx context.Context, db *gorm.DB, ownerID int64) (map[string]string, error) {
		var rows []catmodel.CatalogSeriesIntro
		if err := curatedIntroScope(ctx, db, "series_id", ownerID).Find(&rows).Error; err != nil {
			return nil, err
		}
		out := make(map[string]string, len(rows))
		for _, r := range rows {
			out[r.Lang] = r.Intro
		}
		return out, nil
	},
}

func curatedIntroScope(ctx context.Context, db *gorm.DB, ownerCol string, ownerID int64) *gorm.DB {
	return db.WithContext(ctx).Where(ownerCol+" = ? AND source_id = ?", ownerID, curatedSourceID)
}

// applyEntityIntros builds the intros Apply for one of those tables: a full
// replace of the curated lane. The parse is shared with catalog.work.intros, so
// "an intro list" has one wire shape across every family.
func applyEntityIntros(t introTable) editing.ApplyFunc {
	return func(ctx context.Context, tx *gorm.DB, entityID int64, value any) error {
		intros, err := parseIntros(value)
		if err != nil {
			return fmt.Errorf("editspec: intros: %w", err)
		}
		if err := curatedIntroScope(ctx, tx, t.ownerCol, entityID).
			Delete(t.empty()).Error; err != nil {
			return err
		}
		for _, in := range intros {
			if err := tx.WithContext(ctx).Create(t.newRow(entityID, in.Lang, in.Intro)).Error; err != nil {
				return err
			}
		}
		return nil
	}
}

// loadEntityIntros reads the curated lane back in the declared language order —
// the same canonical form catalog.work.intros returns, so the value round-trips.
func loadEntityIntros(ctx context.Context, db *gorm.DB, t introTable, entityID int64) ([]any, error) {
	byLang, err := t.read(ctx, db, entityID)
	if err != nil {
		return nil, err
	}
	out := make([]any, 0, len(byLang))
	for _, lang := range introLangs {
		if intro, ok := byLang[lang]; ok {
			out = append(out, map[string]any{"lang": lang, "intro": intro})
		}
	}
	return out, nil
}
