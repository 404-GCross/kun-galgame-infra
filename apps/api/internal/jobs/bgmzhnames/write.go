package bgmzhnames

import (
	"context"
	"log/slog"

	"api/internal/platform/catalog/model"
	"api/internal/platform/catalog/repository"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type writer struct {
	db        *gorm.DB
	spec      laneSpec
	existing  map[int64]*zhAliasState
	hostWorks map[int64][]int64
	stats     *Stats
	apply     bool
	touched   []int64
}

func (w *writer) write(ctx context.Context, p plan) {
	state := w.existing[p.OwnerID]
	if state == nil {
		state = &zhAliasState{names: map[string]bool{}}
		w.existing[p.OwnerID] = state
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

		state.names[name] = true
		state.hasPrimary = true
		if !w.apply {
			continue
		}
		inserted, err := w.spec.insert(ctx, w.db, p.OwnerID, name, primary)
		if err != nil {
			w.stats.Errors++
			slog.Warn("bgm-zh-names write", "entity", p.EntityID, "owner", p.OwnerID,
				"external", p.ExternalID, "name", name, "err", err)
			continue
		}
		if !inserted {
			w.stats.Conflict++
			continue
		}
		w.stats.Inserted++
		if primary {
			w.stats.PrimarySet++
		}
		wroteAny = true
	}
	if wroteAny {
		w.touched = append(w.touched, w.hostWorks[p.OwnerID]...)
	}
}

func insertCharacterAlias(ctx context.Context, db *gorm.DB, characterID int64, name string, primary bool) (bool, error) {
	res := db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "character_id"}, {Name: "name"}, {Name: "lang"}},
		DoNothing: true,
	}).Create(&model.CatalogCharacterAlias{
		CharacterID:        characterID,
		Name:               name,
		Lang:               LangZhHans,
		Kind:               model.AliasKindTranslation,
		IsPrimaryForLocale: primary,
		SourceID:           bangumiSourceRef(),
		Provenance:         model.AliasProvenanceSource,
	})
	return res.RowsAffected > 0, res.Error
}

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
		EntityID: p.EntityID, OwnerID: p.OwnerID, ExternalID: p.ExternalID, Name: name, Primary: primary,
	})
}
