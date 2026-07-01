package dto

import "time"

// Admin DTOs for the oauth-hosted /api/v1/admin/artifact/* endpoints. These are
// the code-first source for the admin OpenAPI spec (cmd/gen-openapi -admin) and,
// through it, the frontend's generated TypeScript types
// (apps/web/shared/types/generated/artifact-admin-api.ts). Keep field names/tags
// in lockstep with what the admin handlers actually return.

// AdminArtifactRow is one artifact as shown in the admin browser (no S3
// presigning — metadata only).
type AdminArtifactRow struct {
	UUID           string    `json:"uuid"`
	Name           string    `json:"name"`
	FileKey        string    `json:"file_key"`
	FileSize       int64     `json:"file_size"`
	MimeType       string    `json:"mime_type"`
	SiteKey        string    `json:"site_key"`
	Status         string    `json:"status" enum:"uploading,ready,failed" doc:"Upload lifecycle status"`
	Public         bool      `json:"public"`
	UploaderSub    string    `json:"uploader_sub"`
	UploaderClient string    `json:"uploader_client"`
	Checksum       string    `json:"checksum"`
	CreatedAt      time.Time `json:"created_at"`
	// UpdatedAt = last progress (init / multipart persist / resume). For
	// status=uploading rows it's the "idle since" signal the UI shows and Reclaim
	// guards on.
	UpdatedAt time.Time `json:"updated_at"`
}

// AdminArtifactList is a paginated page of admin rows.
type AdminArtifactList struct {
	Items []AdminArtifactRow `json:"items"`
	Total int64              `json:"total"`
	Page  int                `json:"page"`
	Limit int                `json:"limit"`
}

// AdminArtifactSiteStats is the per-site usage breakdown.
type AdminArtifactSiteStats struct {
	Count int64 `json:"count"`
	Bytes int64 `json:"bytes"`
}

// AdminArtifactStats is the aggregate dashboard counters across all sites.
type AdminArtifactStats struct {
	TotalCount  int64                             `json:"total_count"`
	TotalBytes  int64                             `json:"total_bytes"`
	Uploading   int64                             `json:"uploading"`
	Failed      int64                             `json:"failed"`
	SoftDeleted int64                             `json:"soft_deleted"`
	BySite      map[string]AdminArtifactSiteStats `json:"by_site,omitempty"`
}

// AdminArtifactDelete is the soft-delete result.
type AdminArtifactDelete struct {
	UUID        string `json:"uuid"`
	SoftDeleted bool   `json:"soft_deleted"`
}

// AdminArtifactReclaim is the reclaim (immediate abort+delete) result.
type AdminArtifactReclaim struct {
	UUID      string `json:"uuid"`
	Reclaimed bool   `json:"reclaimed"`
}
