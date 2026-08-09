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

// CatalogWorkLabel is a work↔label ATTRIBUTION edge — "this label
// (circle/publisher/…) is organizationally responsible for this work". It is
// deliberately DISTINCT from a credit (catalog_credit): a credit is an
// authorship signature (a person/name performed a role); an attribution is a
// publishing/making responsibility. Step 14's analysis ("归属≠署名") stands —
// what changed is only WHEN the edge is built: the read surface (step 18,
// letmoe audio pages) is the first consumer that needs it, so the edge lands
// now (consumption-pulled capability).
//
// Stored as ONE row per (work, label, kind) — the composite PK IS the
// uniqueness (same shape as CatalogWorkRelation). Kind is part of the key so a
// label may relate to a work under more than one capacity. No revision: like a
// relation edge, this is relational bookkeeping, not a versioned entity.
type CatalogWorkLabel struct {
	WorkID  int64 `gorm:"primaryKey;autoIncrement:false" json:"work_id"`
	LabelID int64 `gorm:"primaryKey;autoIncrement:false;index" json:"label_id"` // reverse: label→works
	// Kind is a WorkLabelKind* constant. In the PK, no default tag — 0 (circle)
	// is a meaningful value the importer sets explicitly (default-tag zero-value
	// trap avoided).
	Kind int16 `gorm:"primaryKey;autoIncrement:false" json:"kind"`
	// SourceID records which import asserted the attribution; NULL = user.
	SourceID  *int16    `json:"source_id"`
	CreatedAt time.Time `json:"created_at"`

	Work  *CatalogWork  `gorm:"foreignKey:WorkID" json:"work,omitempty"`
	Label *CatalogLabel `gorm:"foreignKey:LabelID" json:"label,omitempty"`
}

func (CatalogWorkLabel) TableName() string { return "catalog_work_label" }

// CatalogReleaseLabel is the SAME attribution fact one layer down: "this label
// published / developed THIS RELEASE". It exists because the work-level edge
// above cannot express what the sources actually record (wave 200).
//
// あまいろショコラータ is the worked example. vndb holds it per release —
// きゃべつそふと made the PC original, Dramatic Create and HuneX shipped the
// Switch port, Sekai Project the English one, and a Russian group a fan patch.
// Flattened onto the work those become five interchangeable "companies", and
// the flattening is not even consistent: the English publisher arrived, the
// Russian one did not. Nobody chose that — it fell out of two importers with
// two different doctrines writing the same table (see the orglabels W(O)
// comment for the gate that was missing).
//
// So the fact lives where it is TRUE, and the work-level edge goes back to
// answering one narrow question ("who made this work") instead of accumulating
// everyone who ever touched any edition of it.
//
// Same shape as CatalogWorkLabel on purpose — ONE row per
// (release, label, kind), kind in the PK because a company is routinely both
// developer and publisher of the same release. Kind reuses the WorkLabelKind*
// constants: it is the same vocabulary of capacities, and a second enum
// meaning the same thing would drift.
type CatalogReleaseLabel struct {
	ReleaseID int64 `gorm:"primaryKey;autoIncrement:false" json:"release_id"`
	LabelID   int64 `gorm:"primaryKey;autoIncrement:false;index" json:"label_id"` // reverse: label→releases
	Kind      int16 `gorm:"primaryKey;autoIncrement:false" json:"kind"`
	// SourceID records which import asserted it; NULL = user.
	SourceID  *int16    `json:"source_id"`
	CreatedAt time.Time `json:"created_at"`

	Release *CatalogRelease `gorm:"foreignKey:ReleaseID" json:"release,omitempty"`
	Label   *CatalogLabel   `gorm:"foreignKey:LabelID" json:"label,omitempty"`
}

func (CatalogReleaseLabel) TableName() string { return "catalog_release_label" }

// CatalogWorkCharacter is a work↔character ROSTER edge — "this character
// appears in this work" (the花名册 membership fact). It is deliberately
// DISTINCT from a voice credit (catalog_credit.character_id): a credit is an
// authorship signature (a VA voiced the character); a roster edge is the
// appearance fact itself, which exists even when no VA is credited. A voiced
// character shows up in BOTH tables with different semantics; the read surface
// (step 46) merges them for display. This edge never touches credit rows.
//
// Shape differs from the composite-PK relation/label edges: an identity id +
// a UNIQUE (work_id, character_id) so a later, stronger source can UPGRADE an
// edge's kind in place (hence updated_at). matched_by is row-level provenance
// in the 'import:<job>' form (external_ref convention); the first source to
// assert a pair wins (insert-if-absent), so it records that first source.
type CatalogWorkCharacter struct {
	ID          int64 `gorm:"primaryKey;autoIncrement:false;type:bigint GENERATED BY DEFAULT AS IDENTITY PRIMARY KEY;default:(-)" json:"id"`
	WorkID      int64 `gorm:"not null;uniqueIndex:uq_catalog_work_character" json:"work_id"`
	CharacterID int64 `gorm:"not null;index;uniqueIndex:uq_catalog_work_character" json:"character_id"` // reverse: character→works
	// Kind is a WorkCharacterKind* constant. NOT NULL, no default — 0 (unknown)
	// is a meaningful value the importer sets explicitly (default-tag zero-value
	// trap avoided).
	Kind int16 `gorm:"not null" json:"kind"`
	// Spoiler is a Spoiler* constant (0=none 1=mild/minor 2=severe/major) — the
	// per-edge spoiler level of the character's appearance in this work (VNDB
	// chars_vns.spoil shape, step 47). NOT NULL, no default — 0 (none) is a
	// meaningful value (every Bangumi/EG edge is genuinely 0, VNDB carries the
	// real level), so it is set explicitly and the migration backfills existing
	// rows to 0 via guarded raw SQL (default-tag zero-value trap avoided).
	Spoiler int16 `gorm:"not null" json:"spoiler"`
	// MatchedBy is 'import:<job>' provenance; first source wins on conflict.
	MatchedBy string    `gorm:"not null" json:"matched_by"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`

	Work      *CatalogWork      `gorm:"foreignKey:WorkID" json:"work,omitempty"`
	Character *CatalogCharacter `gorm:"foreignKey:CharacterID" json:"character,omitempty"`
}

func (CatalogWorkCharacter) TableName() string { return "catalog_work_character" }
