package dto

// InitUploadRequest starts an upload: the caller declares the file up front and
// gets back presigned URL(s) to PUT bytes straight to B2.
type InitUploadRequest struct {
	Name        string `json:"name" validate:"required,max=255"`
	Description string `json:"description,omitempty" validate:"max=2000"`
	FileSize    int64  `json:"file_size" validate:"required,gt=0"`
	MimeType    string `json:"mime_type,omitempty" validate:"max=100"`
	Checksum    string `json:"checksum,omitempty" validate:"omitempty,len=64"` // optional sha256 hex
	Public      bool   `json:"public,omitempty"`
	// UploaderSub names the acting user on the backend (Basic) path, where
	// there's no user JWT. Ignored on the JWT path (the sub comes from claims).
	UploaderSub string `json:"uploader_sub,omitempty" validate:"max=64"`
}

// PartURL is one presigned multipart part URL.
type PartURL struct {
	PartNumber int32  `json:"part_number"`
	URL        string `json:"url"`
}

// InitUploadResponse tells the client how to upload. Single-PUT sets UploadURL;
// multipart sets UploadID + PartSize + PartURLs.
type InitUploadResponse struct {
	UUID      string    `json:"uuid"`
	Multipart bool      `json:"multipart"`
	UploadURL string    `json:"upload_url,omitempty"`
	UploadID  string    `json:"upload_id,omitempty"`
	PartSize  int64     `json:"part_size,omitempty"`
	PartURLs  []PartURL `json:"part_urls,omitempty"`
	ExpiresAt string    `json:"expires_at"`
}

// CompletedPart is one finished multipart part the client reports back.
type CompletedPart struct {
	PartNumber int32  `json:"part_number" validate:"required,gt=0"`
	ETag       string `json:"etag" validate:"required"`
}

// UploadedPart is one multipart part already stored, returned by the resume
// endpoint so the client skips re-uploading it (and reuses its ETag at Complete).
type UploadedPart struct {
	PartNumber int32  `json:"part_number"`
	ETag       string `json:"etag"`
	Size       int64  `json:"size"`
}

// ResumeUploadResponse re-drives an interrupted upload (status=uploading). For a
// single-PUT upload it returns a fresh UploadURL (the original presigned URL has
// usually expired). For multipart it returns the parts already on storage (skip
// them; reuse their ETags at Complete) plus fresh presigned URLs for ONLY the
// parts still missing — the S3-multipart-native equivalent of tus "give me the
// current offset and let me continue".
type ResumeUploadResponse struct {
	UUID          string         `json:"uuid"`
	Multipart     bool           `json:"multipart"`
	UploadURL     string         `json:"upload_url,omitempty"`     // single-PUT: re-presigned PUT
	PartSize      int64          `json:"part_size,omitempty"`      // multipart
	UploadedParts []UploadedPart `json:"uploaded_parts,omitempty"` // multipart: already stored (skip)
	PartURLs      []PartURL      `json:"part_urls,omitempty"`      // multipart: missing parts only
	ExpiresAt     string         `json:"expires_at"`
}

// ManifestInput is the optional game-launch manifest, supplied at Complete time.
type ManifestInput struct {
	Executable   string         `json:"executable" validate:"required,max=500"`
	Arguments    string         `json:"arguments,omitempty" validate:"max=1000"`
	WorkingDir   string         `json:"working_dir,omitempty" validate:"max=500"`
	SavePath     string         `json:"save_path,omitempty" validate:"max=500"`
	Requirements map[string]any `json:"requirements,omitempty"`
}

// CompleteUploadRequest finalises an upload. Parts is required for multipart
// (ignored for single-PUT). Manifest is optional.
type CompleteUploadRequest struct {
	Parts    []CompletedPart `json:"parts,omitempty"`
	Manifest *ManifestInput  `json:"manifest,omitempty"`
}

// ArtifactResponse is the public view of an artifact.
type ArtifactResponse struct {
	UUID        string `json:"uuid"`
	SiteKey     string `json:"site_key"`
	Name        string `json:"name"`
	Description string `json:"description"`
	FileSize    int64  `json:"file_size"`
	MimeType    string `json:"mime_type"`
	Checksum    string `json:"checksum"`
	Status      int    `json:"status"`
	Public      bool   `json:"public"`
	CreatedAt   string `json:"created_at"`
}

// DownloadResponse is the issued download URL (presigned GET or Worker domain).
type DownloadResponse struct {
	URL       string `json:"url"`
	ExpiresAt string `json:"expires_at,omitempty"` // empty for non-expiring Worker URLs
}
