package main

import (
	"context"
	"log/slog"

	"gorm.io/gorm"
)

// pinPortrait sets image_hash as the game's portrait pin: demote any other
// portrait_pinned row, then promote the target — in ONE transaction so the
// partial unique index idx_galgame_cover_portrait_pinned never sees two pins
// (mirrors the sort_order=0 pin-new-banner flow). Idempotent: re-pinning the
// same hash demotes nothing (image_hash <> target) and re-sets true = no-op.
func pinPortrait(ctx context.Context, db *gorm.DB, gameID int, imageHash string) error {
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec(
			`UPDATE galgame_cover SET portrait_pinned = false
			  WHERE galgame_id = ? AND portrait_pinned AND image_hash <> ?`,
			gameID, imageHash).Error; err != nil {
			return err
		}
		return tx.Exec(
			`UPDATE galgame_cover SET portrait_pinned = true
			  WHERE galgame_id = ? AND image_hash = ?`,
			gameID, imageHash).Error
	})
}

// runPinApply pins the best portrait of every stateDirectPin game (>=1080).
// stateAlreadyPinned games are skipped (idempotent); a second run pins nothing.
func runPinApply(ctx context.Context, db *gorm.DB, sels []selection, limit int) error {
	var pinned, skippedAlready, failed int
	acted := 0
	for _, s := range sels {
		if s.State == stateAlreadyPinned {
			skippedAlready++
			continue
		}
		if s.State != stateDirectPin {
			continue
		}
		if limit > 0 && acted >= limit {
			break
		}
		acted++
		if err := pinPortrait(ctx, db, s.GameID, s.Best.Hash); err != nil {
			failed++
			slog.Warn("pin portrait", "gid", s.GameID, "hash", s.Best.Hash, "err", err)
			continue
		}
		pinned++
	}
	slog.Info("pin apply done", "direct_pinned", pinned, "skipped_already_pinned", skippedAlready, "failed", failed)
	return nil
}
