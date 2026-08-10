package model

type CatalogMedium struct {
	ID           int16  `gorm:"primaryKey;autoIncrement:false" json:"id"`
	Key          string `gorm:"uniqueIndex;not null" json:"key"`
	NameCN       string `gorm:"not null;default:''" json:"name_cn"`
	IsDeprecated bool   `gorm:"not null;default:false" json:"is_deprecated"`
}

func (CatalogMedium) TableName() string { return "catalog_medium" }

type CatalogSource struct {
	ID           int16   `gorm:"primaryKey;autoIncrement:false" json:"id"`
	Key          string  `gorm:"uniqueIndex;not null" json:"key"`
	URLTemplate  *string `gorm:"" json:"url_template"`
	TrustTier    int16   `gorm:"not null" json:"trust_tier"`
	Note         string  `gorm:"not null;default:''" json:"note"`
	IsDeprecated bool    `gorm:"not null;default:false" json:"is_deprecated"`
}

func (CatalogSource) TableName() string { return "catalog_source" }

type CatalogRole struct {
	ID           int64  `gorm:"primaryKey;autoIncrement:false" json:"id"`
	Key          string `gorm:"uniqueIndex;not null" json:"key"`
	Category     string `gorm:"not null;default:''" json:"category"`
	NameCN       string `gorm:"not null;default:''" json:"name_cn"`
	NameJA       string `gorm:"not null;default:''" json:"name_ja"`
	NameEN       string `gorm:"not null;default:''" json:"name_en"`
	ParentID     *int64 `gorm:"" json:"parent_id"`
	IsDeprecated bool   `gorm:"not null;default:false" json:"is_deprecated"`
}

func (CatalogRole) TableName() string { return "catalog_role" }

type CatalogSourceRoleMap struct {
	SourceID   int16  `gorm:"primaryKey;autoIncrement:false" json:"source_id"`
	SourceRole string `gorm:"primaryKey" json:"source_role"`
	RoleID     int64  `gorm:"not null" json:"role_id"`
	Note       string `gorm:"not null;default:''" json:"note"`

	Role *CatalogRole `gorm:"foreignKey:RoleID" json:"role,omitempty"`
}

func (CatalogSourceRoleMap) TableName() string { return "catalog_source_role_map" }

const (
	RelationDomainWork   int16 = 0
	RelationDomainEntity int16 = 1
)

type CatalogRelationType struct {
	ID            int64  `gorm:"primaryKey;autoIncrement:false" json:"id"`
	Key           string `gorm:"uniqueIndex;not null" json:"key"`
	Domain        int16  `gorm:"not null" json:"domain"`
	ForwardPhrase string `gorm:"not null" json:"forward_phrase"`
	ReversePhrase string `gorm:"not null" json:"reverse_phrase"`
	IsSymmetric   bool   `gorm:"not null;default:false" json:"is_symmetric"`
	IsDeprecated  bool   `gorm:"not null;default:false" json:"is_deprecated"`
}

func (CatalogRelationType) TableName() string { return "catalog_relation_type" }

type CatalogPlatform struct {
	ID           int16  `gorm:"primaryKey;autoIncrement:false" json:"id"`
	Key          string `gorm:"uniqueIndex;not null" json:"key"`
	DisplayName  string `gorm:"not null" json:"display_name"`
	IsDeprecated bool   `gorm:"not null;default:false" json:"is_deprecated"`
}

func (CatalogPlatform) TableName() string { return "catalog_platform" }
