package providers

import (
	"context"
)

// AnthropicProvider implements content moderation using Anthropic
type AnthropicProvider struct {
	apiKey string
}

// NewAnthropicProvider creates a new AnthropicProvider
func NewAnthropicProvider(apiKey string) *AnthropicProvider {
	return &AnthropicProvider{apiKey: apiKey}
}

// Name returns the provider name
func (p *AnthropicProvider) Name() string {
	return "anthropic"
}

// Moderate performs content moderation
func (p *AnthropicProvider) Moderate(ctx context.Context, req ModerationRequest) (*ModerationResponse, error) {
	// TODO: implement Anthropic moderation API call
	// 1. Send request to Anthropic API
	// 2. Parse response
	// 3. Map categories to decision

	return &ModerationResponse{
		Decision:   DecisionApproved,
		Confidence: 1.0,
		Categories: make(map[string]float64),
		Reason:     "",
	}, nil
}
