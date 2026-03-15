package dto

// InitUploadRequest represents an upload initialization request
type InitUploadRequest struct {
	Name        string `json:"name" validate:"required,max=255"`
	Description string `json:"description"`
	FileSize    int64  `json:"file_size" validate:"required,gt=0"`
	MimeType    string `json:"mime_type" validate:"required"`
}

// InitUploadResponse represents an upload initialization response
type InitUploadResponse struct {
	ArtifactUUID string `json:"artifact_uuid"`
	UploadURL    string `json:"upload_url"`
	ExpiresAt    string `json:"expires_at"`
}

// CompleteUploadRequest represents an upload completion request
type CompleteUploadRequest struct {
	ArtifactUUID string `json:"artifact_uuid" validate:"required"`
	Checksum     string `json:"checksum" validate:"required"`
}

// ArtifactResponse represents an artifact in API responses
type ArtifactResponse struct {
	UUID        string            `json:"uuid"`
	UserUUID    string            `json:"user_uuid"`
	Name        string            `json:"name"`
	Description string            `json:"description"`
	FileSize    int64             `json:"file_size"`
	MimeType    string            `json:"mime_type"`
	Status      int               `json:"status"`
	Metadata    map[string]any    `json:"metadata"`
	CreatedAt   string            `json:"created_at"`
}
