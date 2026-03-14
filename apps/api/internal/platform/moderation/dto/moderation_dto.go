package dto

import "time"

// JobResponse represents a moderation job response
type JobResponse struct {
	ID            uint       `json:"id"`
	UUID          string     `json:"uuid"`
	ContentType   string     `json:"content_type"`
	ContentID     string     `json:"content_id"`
	Status        string     `json:"status"`
	Decision      string     `json:"decision"`
	Confidence    float64    `json:"confidence"`
	Reason        string     `json:"reason"`
	PolicyVersion string     `json:"policy_version"`
	ReviewedBy    *string    `json:"reviewed_by,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
	CompletedAt   *time.Time `json:"completed_at,omitempty"`
}

// ManualReviewRequest represents a manual review request
type ManualReviewRequest struct {
	Decision string `json:"decision" validate:"required,oneof=approved rejected"`
}

// CreateJobRequest represents a job creation request
type CreateJobRequest struct {
	ContentType   string `json:"content_type" validate:"required,oneof=text image"`
	ContentID     string `json:"content_id" validate:"required"`
	PolicyVersion string `json:"policy_version"`
}

// PolicyResponse represents a moderation policy response
type PolicyResponse struct {
	ID          uint      `json:"id"`
	Version     string    `json:"version"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Rules       any       `json:"rules"`
	IsActive    bool      `json:"is_active"`
	CreatedAt   time.Time `json:"created_at"`
}
