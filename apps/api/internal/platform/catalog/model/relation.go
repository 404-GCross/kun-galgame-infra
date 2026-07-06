package model

import "time"

// Relation edges (doc 17 R4, MusicBrainz model): every relation is stored as
// ONE row — the read path renders both directions from the relation type's
// forward/reverse phrases, so there is no second row to drift out of sync.
//
// Service-side rules (step 05, pinned here for the record): edges are
// directed a --type--> b; SYMMETRIC types are normalized a<b before writing;
// the relation type's domain must match the edge family (work vs entity).
// The a<>b CHECKs live in the raw-SQL section.

// CatalogWorkRelation is a work↔work edge.
type CatalogWorkRelation struct {
	AWorkID        int64  `gorm:"primaryKey;autoIncrement:false" json:"a_work_id"`
	BWorkID        int64  `gorm:"primaryKey;autoIncrement:false;index" json:"b_work_id"` // reverse-direction lookups
	RelationTypeID int64  `gorm:"primaryKey;autoIncrement:false" json:"relation_type_id"`
	Note           string `gorm:"not null;default:''" json:"note"`
	// SourceID records which import asserted the edge; NULL = user-curated.
	SourceID  *int16    `json:"source_id"`
	CreatedAt time.Time `json:"created_at"`

	AWork        *CatalogWork         `gorm:"foreignKey:AWorkID" json:"a_work,omitempty"`
	BWork        *CatalogWork         `gorm:"foreignKey:BWorkID" json:"b_work,omitempty"`
	RelationType *CatalogRelationType `gorm:"foreignKey:RelationTypeID" json:"relation_type,omitempty"`
	Source       *CatalogSource       `gorm:"foreignKey:SourceID" json:"source,omitempty"`
}

func (CatalogWorkRelation) TableName() string { return "catalog_work_relation" }

// CatalogEntityRelation is an entity↔entity edge (label imprint/renamed-from/
// subsidiary, person membership, ...). Polymorphic endpoints — addressed by
// (EntityType* constant, id), no entity FKs (same rationale as the
// redirect/usage/revision tables).
type CatalogEntityRelation struct {
	EntityType     int16     `gorm:"primaryKey;autoIncrement:false;index:idx_catalog_entity_relation_reverse" json:"entity_type"`
	AID            int64     `gorm:"primaryKey;autoIncrement:false" json:"a_id"`
	BID            int64     `gorm:"primaryKey;autoIncrement:false;index:idx_catalog_entity_relation_reverse" json:"b_id"`
	RelationTypeID int64     `gorm:"primaryKey;autoIncrement:false" json:"relation_type_id"`
	Note           string    `gorm:"not null;default:''" json:"note"`
	CreatedAt      time.Time `json:"created_at"`

	RelationType *CatalogRelationType `gorm:"foreignKey:RelationTypeID" json:"relation_type,omitempty"`
}

func (CatalogEntityRelation) TableName() string { return "catalog_entity_relation" }
