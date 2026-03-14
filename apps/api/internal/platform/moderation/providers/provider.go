package providers

import (
	"context"
)

// ContentType represents the type of content being moderated
type ContentType string

const (
	ContentTypeText  ContentType = "text"
	ContentTypeImage ContentType = "image"
)

// Decision represents the moderation decision
type Decision string

const (
	DecisionApproved     Decision = "approved"
	DecisionRejected     Decision = "rejected"
	DecisionManualReview Decision = "manual_review"
)

// ModerationRequest represents a moderation request
type ModerationRequest struct {
	ContentType   ContentType
	Text          string
	ImageURLs     []string
	PolicyVersion string
}

// ModerationResponse represents a moderation response
type ModerationResponse struct {
	Decision   Decision
	Confidence float64
	Categories map[string]float64
	Reason     string
}

// Provider is the interface for moderation providers
type Provider interface {
	// Moderate performs content moderation
	Moderate(ctx context.Context, req ModerationRequest) (*ModerationResponse, error)
	// Name returns the provider name
	Name() string
}
