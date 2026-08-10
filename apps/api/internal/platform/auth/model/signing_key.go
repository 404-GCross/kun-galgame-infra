package model

import (
	"time"

	"gorm.io/datatypes"
)

type SigningKey struct {
	Kid           string         `gorm:"primaryKey;size:64" json:"kid"`
	Alg           string         `gorm:"size:8;not null;check:alg IN ('ES256','RS256')" json:"alg"`
	Use           string         `gorm:"size:4;not null;default:sig" json:"use"`
	PublicJWK     datatypes.JSON `gorm:"type:jsonb;not null" json:"-"`
	PrivateKeyEnc []byte         `gorm:"type:bytea;not null" json:"-"`
	State         string         `gorm:"size:8;not null;index;check:state IN ('pending','active','retired')" json:"state"`
	CreatedAt     time.Time      `json:"created_at"`
	ActivatedAt   *time.Time     `json:"activated_at,omitempty"`
	RetiredAt     *time.Time     `json:"retired_at,omitempty"`
}

func (SigningKey) TableName() string { return "signing_keys" }
