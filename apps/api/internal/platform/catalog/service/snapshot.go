package service

import (
	"encoding/json"
	"fmt"

	"api/internal/platform/catalog/model"
	"api/internal/platform/catalog/repository"

	"gorm.io/datatypes"
	"gorm.io/gorm"
)

// Snapshot shapes: FULL entity state including nested children, so unmerge
// can rebuild an entity from its merged_source revision without touching any
// other table. changed_fields is always provided by the caller at edit time,
// never reconstructed by diffing snapshots (the galgame_revision lesson).

type creditNameSnapshot struct {
	CreditName model.CatalogCreditName  `json:"credit_name"`
	Aliases    []model.CatalogNameAlias `json:"aliases"`
}

type personSnapshot struct {
	Person      model.CatalogPerson  `json:"person"`
	CreditNames []creditNameSnapshot `json:"credit_names"`
}

type labelSnapshot struct {
	Label   model.CatalogLabel        `json:"label"`
	Aliases []model.CatalogLabelAlias `json:"aliases"`
}

type characterSnapshot struct {
	Character model.CatalogCharacter        `json:"character"`
	Aliases   []model.CatalogCharacterAlias `json:"aliases"`
}

type workSnapshot struct {
	Work   model.CatalogWork        `json:"work"`
	Titles []model.CatalogWorkTitle `json:"titles"`
}

// takeSnapshot builds the full nested snapshot of an entity.
func takeSnapshot(tx *gorm.DB, entityType int16, id int64) (datatypes.JSON, error) {
	var doc any
	switch entityType {
	case model.EntityTypePerson:
		var p model.CatalogPerson
		if err := tx.First(&p, id).Error; err != nil {
			return nil, err
		}
		var names []model.CatalogCreditName
		if err := tx.Where("person_id = ?", id).Order("id").Find(&names).Error; err != nil {
			return nil, err
		}
		snap := personSnapshot{Person: p, CreditNames: make([]creditNameSnapshot, 0, len(names))}
		for _, n := range names {
			var aliases []model.CatalogNameAlias
			if err := tx.Where("credit_name_id = ?", n.ID).Order("id").Find(&aliases).Error; err != nil {
				return nil, err
			}
			snap.CreditNames = append(snap.CreditNames, creditNameSnapshot{CreditName: n, Aliases: aliases})
		}
		doc = snap
	case model.EntityTypeCreditName:
		var n model.CatalogCreditName
		if err := tx.First(&n, id).Error; err != nil {
			return nil, err
		}
		var aliases []model.CatalogNameAlias
		if err := tx.Where("credit_name_id = ?", id).Order("id").Find(&aliases).Error; err != nil {
			return nil, err
		}
		doc = creditNameSnapshot{CreditName: n, Aliases: aliases}
	case model.EntityTypeLabel:
		var l model.CatalogLabel
		if err := tx.First(&l, id).Error; err != nil {
			return nil, err
		}
		var aliases []model.CatalogLabelAlias
		if err := tx.Where("label_id = ?", id).Order("id").Find(&aliases).Error; err != nil {
			return nil, err
		}
		doc = labelSnapshot{Label: l, Aliases: aliases}
	case model.EntityTypeCharacter:
		var c model.CatalogCharacter
		if err := tx.First(&c, id).Error; err != nil {
			return nil, err
		}
		var aliases []model.CatalogCharacterAlias
		if err := tx.Where("character_id = ?", id).Order("id").Find(&aliases).Error; err != nil {
			return nil, err
		}
		doc = characterSnapshot{Character: c, Aliases: aliases}
	case model.EntityTypeWork:
		var w model.CatalogWork
		if err := tx.First(&w, id).Error; err != nil {
			return nil, err
		}
		var titles []model.CatalogWorkTitle
		if err := tx.Where("work_id = ?", id).Order("id").Find(&titles).Error; err != nil {
			return nil, err
		}
		doc = workSnapshot{Work: w, Titles: titles}
	default:
		return nil, fmt.Errorf("catalog snapshot: unsupported entity type %d", entityType)
	}
	raw, err := json.Marshal(doc)
	if err != nil {
		return nil, fmt.Errorf("catalog snapshot: marshal: %w", err)
	}
	return datatypes.JSON(raw), nil
}

// writeRevision appends one revision row inside the caller's transaction —
// revision numbering is per-entity monotonic (see repository.NextRevision
// for the locking contract).
func writeRevision(tx *gorm.DB, entityType int16, entityID int64, action int16,
	snapshot datatypes.JSON, changedFields datatypes.JSON, actorID *int64, note string) error {
	rev, err := repository.NextRevision(tx, entityType, entityID)
	if err != nil {
		return err
	}
	return tx.Create(&model.CatalogRevision{
		EntityType:    entityType,
		EntityID:      entityID,
		Revision:      rev,
		Action:        action,
		Snapshot:      snapshot,
		ChangedFields: changedFields,
		ActorID:       actorID,
		IsMinor:       false,
		Note:          note,
	}).Error
}

// marshalChangedFields encodes the caller-collected changed-field map;
// nil map → NULL column.
func marshalChangedFields(changed map[string]any) (datatypes.JSON, error) {
	if len(changed) == 0 {
		return nil, nil
	}
	raw, err := json.Marshal(changed)
	if err != nil {
		return nil, fmt.Errorf("catalog revision: marshal changed_fields: %w", err)
	}
	return datatypes.JSON(raw), nil
}
