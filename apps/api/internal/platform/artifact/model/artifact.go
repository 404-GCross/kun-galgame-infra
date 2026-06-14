package model

import (
	"time"

	"gorm.io/datatypes"
	"gorm.io/gorm"
)

// Artifact status values.
const (
	StatusUploading = 0 // Init done, not yet Completed
	StatusReady     = 1 // uploaded + verified
	StatusFailed    = 2 // verification / completion failed
)

// Artifact is one uploaded large file (uuid-addressed, site-prefixed object in
// a private B2 bucket). See docs/artifact/02-storage-and-schema.md.
type Artifact struct {
	ID   uint   `gorm:"primaryKey" json:"id"`
	UUID string `gorm:"type:uuid;uniqueIndex;default:gen_random_uuid()" json:"uuid"`

	SiteKey        string `gorm:"column:site_key;size:32;not null;index" json:"site_key"`
	UploaderSub    string `gorm:"column:uploader_sub;size:64;index" json:"uploader_sub"`
	UploaderClient string `gorm:"column:uploader_client;size:64" json:"uploader_client"`

	Name        string `gorm:"size:255;not null" json:"name"`
	Description string `gorm:"type:text;default:''" json:"description"`
	FileKey     string `gorm:"column:file_key;size:512;not null" json:"-"`

	FileSize     int64  `gorm:"column:file_size;not null;default:0" json:"file_size"` // verified size (Complete)
	ReportedSize int64  `gorm:"column:reported_size;not null" json:"reported_size"`   // declared at Init
	MimeType     string `gorm:"column:mime_type;size:100;default:''" json:"mime_type"`
	Checksum     string `gorm:"size:64;default:''" json:"checksum"` // caller-provided sha256 hex

	Status int  `gorm:"default:0;index" json:"status"`
	Public bool `gorm:"not null;default:false" json:"public"`

	// Multipart session state (empty/0 for single-PUT uploads).
	UploadID string `gorm:"column:upload_id;size:255;default:''" json:"-"`
	PartSize int64  `gorm:"column:part_size;default:0" json:"-"`

	Metadata  datatypes.JSON `gorm:"type:jsonb;default:'{}'" json:"metadata,omitempty"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
	DeletedAt gorm.DeletedAt `gorm:"index" json:"-"`
}

// TableName returns the table name for Artifact.
func (Artifact) TableName() string { return "artifacts" }

// IsReady reports whether the artifact is uploaded, verified and downloadable.
func (a *Artifact) IsReady() bool { return a.Status == StatusReady }

// IsMultipart reports whether this artifact was uploaded via S3 multipart.
func (a *Artifact) IsMultipart() bool { return a.UploadID != "" }
