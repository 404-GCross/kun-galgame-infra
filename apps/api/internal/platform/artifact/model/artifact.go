package model

import (
	"time"

	"gorm.io/datatypes"
	"gorm.io/gorm"
)

const (
	StatusUploading = 0
	StatusReady     = 1
	StatusFailed    = 2
)

type Artifact struct {
	ID   uint   `gorm:"primaryKey" json:"id"`
	UUID string `gorm:"type:uuid;uniqueIndex;default:gen_random_uuid()" json:"uuid"`

	SiteKey        string `gorm:"column:site_key;size:32;not null;index" json:"site_key"`
	UploaderSub    string `gorm:"column:uploader_sub;size:64;index" json:"uploader_sub"`
	UploaderClient string `gorm:"column:uploader_client;size:64" json:"uploader_client"`

	Name        string `gorm:"size:255;not null" json:"name"`
	Description string `gorm:"type:text;default:''" json:"description"`
	FileKey     string `gorm:"column:file_key;size:512;not null" json:"-"`

	FileSize     int64  `gorm:"column:file_size;not null;default:0" json:"file_size"`
	ReportedSize int64  `gorm:"column:reported_size;not null" json:"reported_size"`
	MimeType     string `gorm:"column:mime_type;size:100;default:''" json:"mime_type"`
	Checksum     string `gorm:"size:64;default:''" json:"checksum"`

	Status int  `gorm:"default:0;index" json:"status"`
	Public bool `gorm:"not null;default:false" json:"public"`

	UploadID string `gorm:"column:upload_id;size:255;default:''" json:"-"`
	PartSize int64  `gorm:"column:part_size;default:0" json:"-"`

	Metadata  datatypes.JSON `gorm:"type:jsonb;default:'{}'" json:"metadata,omitempty"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
	DeletedAt gorm.DeletedAt `gorm:"index" json:"-"`
}

func (Artifact) TableName() string { return "artifacts" }

func (a *Artifact) IsReady() bool { return a.Status == StatusReady }

func (a *Artifact) IsMultipart() bool { return a.UploadID != "" }
