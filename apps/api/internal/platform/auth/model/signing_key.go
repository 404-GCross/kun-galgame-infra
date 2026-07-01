package model

import (
	"time"

	"gorm.io/datatypes"
)

// SigningKey is one OIDC signing key in the three-state lifecycle
// (pending -> active -> retired). Public material is served verbatim in the
// JWKS; the private key is stored AES-256-GCM encrypted under the KEK and is
// never served or logged.
//
// The CHECK constraints are created by AutoMigrate on this (new) table.
// Design: docs/auth/03-oidc-standardization-design.md §4.
type SigningKey struct {
	// Kid is the RFC 7638 JWK thumbprint — appears in every JWS header and the
	// JWK entry.
	Kid string `gorm:"primaryKey;size:64" json:"kid"`
	Alg string `gorm:"size:8;not null;check:alg IN ('ES256','RS256')" json:"alg"`
	Use string `gorm:"size:4;not null;default:sig" json:"use"`
	// PublicJWK is the public JWK served verbatim in the JWK Set.
	PublicJWK datatypes.JSON `gorm:"type:jsonb;not null" json:"-"`
	// PrivateKeyEnc is AES-256-GCM(PKCS#8 DER) under the KEK — never served.
	PrivateKeyEnc []byte     `gorm:"type:bytea;not null" json:"-"`
	State         string     `gorm:"size:8;not null;index;check:state IN ('pending','active','retired')" json:"state"`
	CreatedAt     time.Time  `json:"created_at"`
	ActivatedAt   *time.Time `json:"activated_at,omitempty"`
	RetiredAt     *time.Time `json:"retired_at,omitempty"`
}

// TableName returns the table name for SigningKey.
func (SigningKey) TableName() string { return "signing_keys" }
