package moderation

import (
	"context"
	"time"
)

// NoopProvider approves everything. V1 / V2 default; real providers plug
// in at V3 deployment time.
type NoopProvider struct{}

func NewNoop() *NoopProvider { return &NoopProvider{} }

func (*NoopProvider) Name() string { return "noop" }

func (*NoopProvider) SyncCheck(_ context.Context, _ []byte, _ string) (*Decision, error) {
	return &Decision{Verdict: VerdictApprove, Elapsed: 0 * time.Nanosecond}, nil
}

func (*NoopProvider) AsyncCheck(_ context.Context, _ []byte, _ string) (*Decision, error) {
	return &Decision{Verdict: VerdictApprove}, nil
}
