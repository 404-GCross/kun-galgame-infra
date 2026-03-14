package providers

import (
	"context"
)

// OpenAIProvider implements content moderation using OpenAI
type OpenAIProvider struct {
	apiKey string
}

// NewOpenAIProvider creates a new OpenAIProvider
func NewOpenAIProvider(apiKey string) *OpenAIProvider {
	return &OpenAIProvider{apiKey: apiKey}
}

// Name returns the provider name
func (p *OpenAIProvider) Name() string {
	return "openai"
}

// Moderate performs content moderation
func (p *OpenAIProvider) Moderate(ctx context.Context, req ModerationRequest) (*ModerationResponse, error) {
	// TODO: implement OpenAI moderation API call
	// 1. Send request to OpenAI moderation endpoint
	// 2. Parse response
	// 3. Map categories to decision

	return &ModerationResponse{
		Decision:   DecisionApproved,
		Confidence: 1.0,
		Categories: make(map[string]float64),
		Reason:     "",
	}, nil
}
