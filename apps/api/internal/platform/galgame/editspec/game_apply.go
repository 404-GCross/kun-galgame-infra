package editspec

import (
	"context"
	"fmt"
	"time"

	"api/internal/platform/editing"
	"api/internal/platform/galgame/intronorm"
	"api/internal/platform/galgame/model"
	"api/internal/platform/galgame/repository"

	"gorm.io/gorm"
)

// Apply closures for galgame.game. Each one lands EXACTLY the write the old
// single write path (repository.ApplySnapshot) performed for its field:
// scalar columns mirror the updates map (including the intro image-strip and
// the release-date parse), relation fields call the same exported per-field
// helpers ApplySnapshot itself composes (repository/apply_fields.go) — one
// truth, two entry points.

// applyGameColumn writes one scalar galgame column. RowsAffected 0 = the row
// is gone → entity not found (mirrors catalog's applyWorkColumn posture).
// GORM's Update also bumps the autoUpdateTime `updated` column, matching the
// old ApplySnapshot which always rewrote the scalar map.
func applyGameColumn(column string, conv func(any) (any, error)) editing.ApplyFunc {
	return func(ctx context.Context, tx *gorm.DB, entityID int64, value any) error {
		v, err := conv(value)
		if err != nil {
			return fmt.Errorf("editspec: %s: %w", column, err)
		}
		res := tx.WithContext(ctx).Model(&model.Galgame{}).
			Where("id = ?", entityID).Update(column, v)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return editing.ErrEntityNotFound
		}
		return nil
	}
}

func convString(v any) (any, error) {
	s, ok := v.(string)
	if !ok {
		return nil, fmt.Errorf("must be a string")
	}
	return s, nil
}

// convIntro enforces "no images in intros — use the gallery" at the write
// path, exactly like ApplySnapshot does for every caller.
func convIntro(v any) (any, error) {
	s, ok := v.(string)
	if !ok {
		return nil, fmt.Errorf("must be a string")
	}
	return intronorm.StripImages(s), nil
}

// convReleaseDate turns the snapshot form (null / "YYYY-MM-DD") into the
// *time.Time the date column stores — the same model.ParseSnapshotReleaseDate
// ApplySnapshot uses.
func convReleaseDate(v any) (any, error) {
	if v == nil {
		return model.ParseSnapshotReleaseDate(nil), nil
	}
	s, ok := v.(string)
	if !ok {
		return nil, fmt.Errorf("must be null or a string")
	}
	return model.ParseSnapshotReleaseDate(&s), nil
}

func convBool(v any) (any, error) {
	b, ok := v.(bool)
	if !ok {
		return nil, fmt.Errorf("must be a boolean")
	}
	return b, nil
}

// convReleasePrecision maps the pre-P3 empty value to "unknown" — the column
// CHECK rejects "" and ApplySnapshot's ResolveReleasePrecision supplied the
// same terminal fallback when a snapshot carried no date context.
func convReleasePrecision(v any) (any, error) {
	s, ok := v.(string)
	if !ok {
		return nil, fmt.Errorf("must be a string")
	}
	if s == "" {
		return string(model.PrecisionUnknown), nil
	}
	return s, nil
}

func convNullableInt(v any) (any, error) {
	if v == nil {
		return (*int)(nil), nil
	}
	n, err := wholeNumber(v)
	if err != nil {
		return nil, err
	}
	i := int(n)
	return &i, nil
}

func convStatus(v any) (any, error) {
	n, err := wholeNumber(v)
	if err != nil {
		return nil, err
	}
	return int(n), nil
}

// touchGame is the relation appliers' twin of the scalar path's implicit
// `updated` bump (old ApplySnapshot always rewrote the scalar map, so any
// edit refreshed galgame.updated) and doubles as the entity-existence check.
func touchGame(ctx context.Context, tx *gorm.DB, gid int) error {
	res := tx.WithContext(ctx).Model(&model.Galgame{}).
		Where("id = ?", gid).Update("updated", time.Now())
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return editing.ErrEntityNotFound
	}
	return nil
}

func applyAliases(ctx context.Context, tx *gorm.DB, entityID int64, value any) error {
	aliases, err := decodeList[string](value, false)
	if err != nil {
		return fmt.Errorf("editspec: aliases: %w", err)
	}
	if err := touchGame(ctx, tx, int(entityID)); err != nil {
		return err
	}
	return repository.ReconcileAliases(tx.WithContext(ctx), int(entityID), aliases)
}

// applyIDList covers the three *_ids join-table fields.
func applyIDList(fieldKey string) editing.ApplyFunc {
	return func(ctx context.Context, tx *gorm.DB, entityID int64, value any) error {
		ids, err := decodeList[int](value, false)
		if err != nil {
			return fmt.Errorf("editspec: %s: %w", fieldKey, err)
		}
		if err := touchGame(ctx, tx, int(entityID)); err != nil {
			return err
		}
		gid := int(entityID)
		switch fieldKey {
		case FieldTagIDs:
			return repository.ReconcileTagIDs(tx.WithContext(ctx), gid, ids)
		case FieldOfficialIDs:
			return repository.ReconcileOfficialIDs(tx.WithContext(ctx), gid, ids)
		case FieldEngineIDs:
			return repository.ReconcileEngineIDs(tx.WithContext(ctx), gid, ids)
		}
		return fmt.Errorf("editspec: %s is not an id-list field", fieldKey)
	}
}

func applyLinks(ctx context.Context, tx *gorm.DB, entityID int64, value any) error {
	links, err := decodeList[model.SnapshotLink](value, true)
	if err != nil {
		return fmt.Errorf("editspec: links: %w", err)
	}
	if err := touchGame(ctx, tx, int(entityID)); err != nil {
		return err
	}
	// Link rows carry an owner uid; the engine rides the acting user on the
	// context (0 = unattributed, e.g. an S2S write with no actor plumbing).
	return repository.RebuildLinks(tx.WithContext(ctx), int(entityID),
		int(editing.ActorFromContext(ctx)), links)
}

func applyCovers(ctx context.Context, tx *gorm.DB, entityID int64, value any) error {
	covers, err := decodeList[model.SnapshotCover](value, true)
	if err != nil {
		return fmt.Errorf("editspec: covers: %w", err)
	}
	if err := touchGame(ctx, tx, int(entityID)); err != nil {
		return err
	}
	return repository.RebuildCovers(tx.WithContext(ctx), int(entityID), covers)
}

func applyScreenshots(ctx context.Context, tx *gorm.DB, entityID int64, value any) error {
	shots, err := decodeList[model.SnapshotScreenshot](value, true)
	if err != nil {
		return fmt.Errorf("editspec: screenshots: %w", err)
	}
	if err := touchGame(ctx, tx, int(entityID)); err != nil {
		return err
	}
	return repository.RebuildScreenshots(tx.WithContext(ctx), int(entityID), shots)
}
