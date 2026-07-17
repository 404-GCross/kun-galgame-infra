package repository

import (
	"api/internal/platform/galgame/model"

	"gorm.io/gorm"
)

// Exported per-field relation writers (E2a). The editing engine's
// galgame.game Apply closures land ONE field at a time, so each relation
// write is a named helper here and ApplySnapshot composes exactly these —
// one truth, zero behavior drift between the snapshot path and the engine
// path. Set-valued relations reconcile by delta (see reconcileSet);
// links/covers/screenshots keep the historical clear-and-rebuild because
// they are order-significant / carry per-row payload.

// ReconcileAliases brings galgame_alias rows into agreement with `aliases`.
func ReconcileAliases(tx *gorm.DB, galgameID int, aliases []string) error {
	return reconcileSet(tx, "galgame_id", galgameID, "name", aliases,
		func(name string) model.GalgameAlias {
			return model.GalgameAlias{GalgameID: galgameID, Name: name}
		})
}

// ReconcileTagIDs brings galgame_tag_relation rows into agreement with `ids`.
func ReconcileTagIDs(tx *gorm.DB, galgameID int, ids []int) error {
	return reconcileSet(tx, "galgame_id", galgameID, "tag_id", ids,
		func(id int) model.GalgameTagRelation {
			return model.GalgameTagRelation{GalgameID: galgameID, TagID: id}
		})
}

// ReconcileOfficialIDs brings galgame_official_relation rows into agreement.
func ReconcileOfficialIDs(tx *gorm.DB, galgameID int, ids []int) error {
	return reconcileSet(tx, "galgame_id", galgameID, "official_id", ids,
		func(id int) model.GalgameOfficialRelation {
			return model.GalgameOfficialRelation{GalgameID: galgameID, OfficialID: id}
		})
}

// ReconcileEngineIDs brings galgame_engine_relation rows into agreement.
func ReconcileEngineIDs(tx *gorm.DB, galgameID int, ids []int) error {
	return reconcileSet(tx, "galgame_id", galgameID, "engine_id", ids,
		func(id int) model.GalgameEngineRelation {
			return model.GalgameEngineRelation{GalgameID: galgameID, EngineID: id}
		})
}

// RebuildLinks clears and rebuilds galgame_link from `links`, owned by
// userID (the acting editor; 0 = unattributed system write).
func RebuildLinks(tx *gorm.DB, galgameID, userID int, links []model.SnapshotLink) error {
	if err := tx.Where("galgame_id = ?", galgameID).Delete(&model.GalgameLink{}).Error; err != nil {
		return err
	}
	for _, link := range links {
		if err := tx.Create(&model.GalgameLink{
			GalgameID: galgameID,
			UserID:    userID,
			Name:      link.Name,
			Link:      link.Link,
			Source:    link.Source,
			SourceKey: link.SourceKey,
		}).Error; err != nil {
			return err
		}
	}
	return nil
}

// RebuildCovers clears and rebuilds the cover candidate set. The partial
// unique index idx_galgame_cover_pinned (galgame_id WHERE sort_order=0) is
// enforced by Postgres: at most one pinned row per galgame or the insert
// fails.
func RebuildCovers(tx *gorm.DB, galgameID int, covers []model.SnapshotCover) error {
	if err := tx.Where("galgame_id = ?", galgameID).Delete(&model.GalgameCover{}).Error; err != nil {
		return err
	}
	for _, c := range covers {
		if err := tx.Create(&model.GalgameCover{
			GalgameID: galgameID,
			ImageHash: c.ImageHash,
			SortOrder: c.SortOrder,
			Sexual:    c.Sexual,
			Violence:  c.Violence,
			Source:    c.Source,
			SourceKey: c.SourceKey,
			Kind:      c.Kind,
		}).Error; err != nil {
			return err
		}
	}
	return nil
}

// RebuildScreenshots clears and rebuilds the gallery/screenshot set.
func RebuildScreenshots(tx *gorm.DB, galgameID int, screenshots []model.SnapshotScreenshot) error {
	if err := tx.Where("galgame_id = ?", galgameID).Delete(&model.GalgameScreenshot{}).Error; err != nil {
		return err
	}
	for _, sh := range screenshots {
		if err := tx.Create(&model.GalgameScreenshot{
			GalgameID: galgameID,
			ImageHash: sh.ImageHash,
			SortOrder: sh.SortOrder,
			Caption:   sh.Caption,
			Sexual:    sh.Sexual,
			Violence:  sh.Violence,
			Source:    sh.Source,
			SourceKey: sh.SourceKey,
		}).Error; err != nil {
			return err
		}
	}
	return nil
}
