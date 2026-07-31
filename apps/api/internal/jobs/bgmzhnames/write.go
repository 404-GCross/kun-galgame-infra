package bgmzhnames

import (
	"context"
	"log/slog"

	"api/internal/platform/catalog/model"
	"api/internal/platform/catalog/repository"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// writer turns decided plans into alias rows and collects the touch set.
type writer struct {
	db        *gorm.DB
	existing  map[int64]*zhAliasState
	hostWorks map[int64][]int64
	stats     *Stats
	apply     bool
	touched   []int64
}

// write plans (and in apply mode inserts) one character's names.
//
// Two rules meet here:
//
//   - fill-missing is per (character, name), NOT per language: the unique key is
//     (character_id, name, lang), so a character that already has a zh-Hans name
//     can still gain another one. A name it already carries is skipped.
//   - the locale primary is claimed by at most one row, and only when the
//     character has none. The main `简体中文名` is first in the plan, so it is the
//     one that claims it; an existing primary — a human edit or an earlier run —
//     is never flipped.
func (w *writer) write(ctx context.Context, p plan) {
	state := w.existing[p.CharacterID]
	if state == nil {
		state = &zhAliasState{names: map[string]bool{}}
		w.existing[p.CharacterID] = state
	}
	wroteAny := false
	for _, name := range p.Names {
		if state.names[name] {
			w.stats.SkippedDup++
			continue
		}
		primary := !state.hasPrimary
		w.stats.WouldInsert++
		w.collect(p, name, primary)

		// The decided plan must be identical in dry and apply: mark the name (and
		// the primary claim) as taken either way, so the forecast does not count
		// the same character's second name as a second primary.
		state.names[name] = true
		state.hasPrimary = true
		if !w.apply {
			continue
		}
		inserted, err := insertAlias(ctx, w.db, p.CharacterID, name, primary)
		if err != nil {
			w.stats.Errors++
			slog.Warn("bgm-zh-names write", "character", p.CharacterID, "external", p.ExternalID, "name", name, "err", err)
			continue
		}
		if !inserted { // concurrent writer / backstop — the row is already there
			w.stats.Conflict++
			continue
		}
		w.stats.Inserted++
		if primary {
			w.stats.PrimarySet++
		}
		wroteAny = true
	}
	// One touch per character that actually gained rows, however many it gained.
	if wroteAny {
		w.touched = append(w.touched, w.hostWorks[p.CharacterID]...)
	}
}

func insertAlias(ctx context.Context, db *gorm.DB, characterID int64, name string, primary bool) (bool, error) {
	res := db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "character_id"}, {Name: "name"}, {Name: "lang"}},
		DoNothing: true,
	}).Create(&model.CatalogCharacterAlias{
		CharacterID:        characterID,
		Name:               name,
		Lang:               LangZhHans,
		Kind:               model.AliasKindTranslation,
		IsPrimaryForLocale: primary,
	})
	return res.RowsAffected > 0, res.Error
}

// flushTouch bumps catalog_work.updated_at once per host work that actually
// gained a name, and records how many distinct works that was. A run that wrote
// nothing has an empty set and moves no watermark.
func (w *writer) flushTouch(ctx context.Context) error {
	seen := make(map[int64]struct{}, len(w.touched))
	ids := make([]int64, 0, len(w.touched))
	for _, id := range w.touched {
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	if err := repository.TouchWorks(ctx, w.db, ids); err != nil {
		return err
	}
	w.stats.Touched = len(ids)
	return nil
}

func (w *writer) collect(p plan, name string, primary bool) {
	if len(w.stats.Samples) >= maxSamples {
		return
	}
	w.stats.Samples = append(w.stats.Samples, Sample{
		CharacterID: p.CharacterID, ExternalID: p.ExternalID, Name: name, Primary: primary,
	})
}
