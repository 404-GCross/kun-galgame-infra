package dto

// InitUploadRequest starts an upload: the caller declares the file up front and
// gets back presigned URL(s) to PUT bytes straight to B2.
type InitUploadRequest struct {
	Name        string `json:"name" validate:"required,max=255"`
	Description string `json:"description" validate:"max=2000"`
	FileSize    int64  `json:"file_size" validate:"required,gt=0"`
	MimeType    string `json:"mime_type" validate:"max=100"`
	Checksum    string `json:"checksum" validate:"omitempty,len=64"` // optional sha256 hex
	Public      bool   `json:"public"`
	// UploaderSub names the acting user on the backend (Basic) path, where
	// there's no user JWT. Ignored on the JWT path (the sub comes from claims).
	UploaderSub string `json:"uploader_sub" validate:"max=64"`
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

// ManifestInput is the optional game-launch manifest, supplied at Complete time.
type ManifestInput struct {
	Executable   string         `json:"executable" validate:"required,max=500"`
	Arguments    string         `json:"arguments" validate:"max=1000"`
	WorkingDir   string         `json:"working_dir" validate:"max=500"`
	SavePath     string         `json:"save_path" validate:"max=500"`
	Requirements map[string]any `json:"requirements"`
}

// CompleteUploadRequest finalises an upload. Parts is required for multipart
// (ignored for single-PUT). Manifest is optional.
type CompleteUploadRequest struct {
	Parts    []CompletedPart `json:"parts"`
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
