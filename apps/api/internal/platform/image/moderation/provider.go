package moderation

import (
	"context"
	"time"
)

type Verdict int

const (
	VerdictApprove   Verdict = 1
	VerdictReject    Verdict = 2
	VerdictReview    Verdict = 3
	VerdictUndecided Verdict = 0
)

type Decision struct {
	Verdict Verdict        `json:"verdict"`
	Labels  map[string]any `json:"labels,omitempty"`
	Reason  string         `json:"reason,omitempty"`
	Elapsed time.Duration  `json:"elapsed"`
}

type Provider interface {
	Name() string

	SyncCheck(ctx context.Context, body []byte, mime string) (*Decision, error)

	AsyncCheck(ctx context.Context, body []byte, mime string) (*Decision, error)
}
