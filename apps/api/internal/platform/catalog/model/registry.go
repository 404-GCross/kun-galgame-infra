// Package model holds the GORM models of the kun_catalog database.
//
// This file defines the five vocabulary REGISTRY tables (doc 17 R1):
// editorial taxonomies are data rows, not code enums — adding a medium, an
// upstream source, a role, or a relation type is a seed INSERT, never a
// deploy. Registry rows are never DELETEd; retirement is is_deprecated=true
// (set manually, never by seeds).
//
// All registry primary keys are EXPLICIT, seed-owned values (no identity /
// autoincrement): vocabulary IDs must be identical across environments so a
// locally curated dataset can be moved to production wholesale.
package model

// CatalogMedium is one work medium (galgame / manga / novel / anime / asmr /
// doujin_game / music ...). catalog_work.medium_id references it.
type CatalogMedium struct {
	ID           int16  `gorm:"primaryKey;autoIncrement:false" json:"id"`
	Key          string `gorm:"uniqueIndex;not null" json:"key"`
	NameCN       string `gorm:"not null;default:''" json:"name_cn"`
	IsDeprecated bool   `gorm:"not null;default:false" json:"is_deprecated"`
}

func (CatalogMedium) TableName() string { return "catalog_medium" }

// CatalogSource is one upstream data source or external site (vndb / bangumi
// / dlsite / ... / official_site / twitter). external_ref and the
// survivorship configuration reference it.
type CatalogSource struct {
	ID  int16  `gorm:"primaryKey;autoIncrement:false" json:"id"`
	Key string `gorm:"uniqueIndex;not null" json:"key"`
	// URLTemplate renders an external_id into a URL (Wikidata P1630 style).
	// Left NULL by seeds; filled once the per-entity URL shapes are designed.
	// Validation/cleanup logic stays in code (MusicBrainz URLCleanup lesson).
	URLTemplate *string `gorm:"" json:"url_template"`
	// TrustTier drives the R8 three-layer exact-write policy:
	//   0 = first-party structural anchor — the ONLY tier allowed to
	//       auto-write link_kind=exact (with matched_by + sampled review);
	//   1 = community-maintained cross references — probable at best,
	//       human confirmation promotes to exact;
	//   2 = free-form user-entered — candidates only.
	//
	// No DB default on purpose (R1 sketches DEFAULT 1): with a default tag
	// GORM omits zero-valued fields from INSERTs, silently turning the
	// seeded tier-0 rows (user, dlsite) into tier 1. Seeds are the single
	// write path and always set the tier explicitly.
	TrustTier    int16  `gorm:"not null" json:"trust_tier"`
	Note         string `gorm:"not null;default:''" json:"note"`
	IsDeprecated bool   `gorm:"not null;default:false" json:"is_deprecated"`
}

func (CatalogSource) TableName() string { return "catalog_source" }

// CatalogRole is one credit role/position in the unified cross-media role
// vocabulary. The base vocabulary is generated from the Bangumi 246 staff
// positions (the most complete public baseline); other sources' role systems
// map onto it many-to-one via CatalogSourceRoleMap.
type CatalogRole struct {
	ID  int64  `gorm:"primaryKey;autoIncrement:false" json:"id"`
	Key string `gorm:"uniqueIndex;not null" json:"key"`
	// Category is a coarse grouping for edit UIs (from Bangumi's category
	// labels, e.g. "producer"); empty when the source position has none.
	Category     string `gorm:"not null;default:''" json:"category"`
	NameCN       string `gorm:"not null;default:''" json:"name_cn"`
	NameJA       string `gorm:"not null;default:''" json:"name_ja"`
	NameEN       string `gorm:"not null;default:''" json:"name_en"`
	ParentID     *int64 `gorm:"" json:"parent_id"`
	IsDeprecated bool   `gorm:"not null;default:false" json:"is_deprecated"`
}

func (CatalogRole) TableName() string { return "catalog_role" }

// CatalogSourceRoleMap maps one source-native role identifier onto the
// unified role vocabulary (many-to-one). SourceRole is source-defined text:
// for bangumi it is "<subject_type>:<position_id>" (e.g. "4:1001").
type CatalogSourceRoleMap struct {
	SourceID   int16  `gorm:"primaryKey;autoIncrement:false" json:"source_id"`
	SourceRole string `gorm:"primaryKey" json:"source_role"`
	RoleID     int64  `gorm:"not null" json:"role_id"`
	Note       string `gorm:"not null;default:''" json:"note"`

	Role *CatalogRole `gorm:"foreignKey:RoleID" json:"role,omitempty"`
}

func (CatalogSourceRoleMap) TableName() string { return "catalog_source_role_map" }

// Relation domains: a relation type applies to exactly one edge family.
const (
	RelationDomainWork   int16 = 0 // work↔work edges
	RelationDomainEntity int16 = 1 // entity↔entity edges (label/org/person)
)

// CatalogRelationType is one relation type in the unified cross-media
// relation vocabulary. Each relation edge is stored as ONE row; the reverse
// direction is rendered from ReversePhrase (MusicBrainz model — no second row
// to drift out of sync).
type CatalogRelationType struct {
	ID            int64  `gorm:"primaryKey;autoIncrement:false" json:"id"`
	Key           string `gorm:"uniqueIndex;not null" json:"key"`
	Domain        int16  `gorm:"not null" json:"domain"`
	ForwardPhrase string `gorm:"not null" json:"forward_phrase"`
	ReversePhrase string `gorm:"not null" json:"reverse_phrase"`
	// IsSymmetric relations (same_series, crossover_with, ...) are stored
	// with the endpoint IDs normalized a<b; both directions render the
	// forward phrase.
	IsSymmetric  bool `gorm:"not null;default:false" json:"is_symmetric"`
	IsDeprecated bool `gorm:"not null;default:false" json:"is_deprecated"`
}

func (CatalogRelationType) TableName() string { return "catalog_relation_type" }

// CatalogPlatform is the platform vocabulary registry (step 96, doc 17 R1 —
// every vocabulary is a lookup-table registry). Keys are the VNDB platform
// codes verbatim — the de-facto standard of the existing data (159,718
// catalog_release.platform rows + extra.platforms arrays already use them).
// catalog_release.platform / catalog_work_platform.platform stay TEXT codes
// (the lang-column convention — no FK rewire of the standing read contract);
// the registry supplies display names and the deprecation audit face.
type CatalogPlatform struct {
	ID  int16  `gorm:"primaryKey;autoIncrement:false" json:"id"`
	Key string `gorm:"uniqueIndex;not null" json:"key"`
	// DisplayName is the human label (English; zh localization is a future
	// vocabulary pass).
	DisplayName  string `gorm:"not null" json:"display_name"`
	IsDeprecated bool   `gorm:"not null;default:false" json:"is_deprecated"`
}

func (CatalogPlatform) TableName() string { return "catalog_platform" }
