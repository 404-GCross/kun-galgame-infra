package orglabels

import (
	"context"
	"encoding/json"

	"api/internal/platform/catalog/model"

	"gorm.io/datatypes"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// edgeKindFor maps a label kind to the work_label attribution kind, following
// the established writer convention (survey of existing catalog_work_label):
// game brand → brand, doujin circle / group → circle, publisher → publisher.
func edgeKindFor(labelKind int16) int16 {
	switch labelKind {
	case model.LabelKindGameBrand:
		return model.WorkLabelKindBrand
	case model.LabelKindPublisher:
		return model.WorkLabelKindPublisher
	default: // doujin circle, group
		return model.WorkLabelKindCircle
	}
}

// mintLabel creates a new label for a source org with works but no matching
// label (裁定5): the label row + a self exact anchor + an imported revision +
// one work_label edge per work in W(O). Returns the number of edges written.
// Wrapped in a transaction so a partial mint never lands.
func mintLabel(ctx context.Context, db *gorm.DB, source int16, o *orgRec) (int, error) {
	rule := ruleSetFor(source).newLabel
	edgeKind := edgeKindFor(o.newKind)
	edgesWritten := 0

	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		label := model.CatalogLabel{
			DisplayName:     o.displayName,
			Latin:           o.latin,
			Lang:            o.lang,
			Kind:            o.newKind,
			FieldProvenance: mintProvenance(source, o),
		}
		if err := tx.Create(&label).Error; err != nil {
			return err
		}
		if err := tx.Create(&model.CatalogExternalRef{
			EntityType: model.EntityTypeLabel,
			EntityID:   label.ID,
			SourceID:   source,
			ExternalID: o.extID,
			LinkKind:   model.LinkKindExact,
			MatchedBy:  rule,
		}).Error; err != nil {
			return err
		}
		if err := tx.Create(&model.CatalogRevision{
			EntityType: model.EntityTypeLabel,
			EntityID:   label.ID,
			Revision:   1,
			Action:     model.RevisionActionImported,
			Snapshot:   labelSnapshot(label),
			IsMinor:    false,
		}).Error; err != nil {
			return err
		}
		edges := make([]model.CatalogWorkLabel, 0, len(o.works))
		src := source
		for _, w := range o.works {
			edges = append(edges, model.CatalogWorkLabel{
				WorkID: w, LabelID: label.ID, Kind: edgeKind, SourceID: &src,
			})
		}
		if len(edges) > 0 {
			res := tx.Clauses(clause.OnConflict{DoNothing: true}).CreateInBatches(edges, 1000)
			if res.Error != nil {
				return res.Error
			}
			edgesWritten = int(res.RowsAffected)
		}
		return nil
	})
	return edgesWritten, err
}

// mintProvenance records which importer minted the label's fields (裁定5). R8
// array shape ({"<field>":[{"source","at"}]}), matching the charattrs writer.
func mintProvenance(source int16, o *orgRec) datatypes.JSON {
	entry := []map[string]string{{"source": srcKey(source), "at": nowUTC().Format("2006-01-02T15:04:05Z")}}
	prov := map[string]any{
		"display_name": entry,
		"kind":         entry,
	}
	if o.lang != "" {
		prov["lang"] = entry
	}
	if o.latin != nil && *o.latin != "" {
		prov["latin"] = entry
	}
	b, _ := json.Marshal(prov)
	return b
}

// labelSnapshot is the full-entity revision-1 snapshot (importer convention).
func labelSnapshot(label model.CatalogLabel) datatypes.JSON {
	b, _ := json.Marshal(map[string]any{"label": label, "aliases": []any{}})
	return b
}

// ruleSetFor returns the four matched_by strings for a source.
func ruleSetFor(source int16) ruleSet {
	switch source {
	case sourceVNDB:
		return ruleSet{ruleVNDBCoworks, ruleVNDBCoworkName, ruleVNDBNameOnly, ruleVNDBNew}
	case sourceBangumi:
		return ruleSet{ruleBGMCoworks, ruleBGMCoworkName, ruleBGMNameOnly, ruleBGMNew}
	default:
		return ruleSet{ruleEGCoworks, ruleEGCoworkName, ruleEGNameOnly, ruleEGNew}
	}
}
