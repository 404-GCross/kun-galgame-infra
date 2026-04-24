// Package moderation defines the provider interface for image content
// moderation and wires it into the upload pipeline.
//
// V3 ships with a noop provider (always approves). Real providers
// (aliyun / tencent content-safety, or a local NSFW model) implement
// the Provider interface.
//
// Two integration points exist:
//
//  1. Synchronous check: called inline during upload. Must return fast
//     (< 300ms typical) or the request will hang. Intended to cheaply
//     block the most obvious violations.
//
//  2. Asynchronous check: image is uploaded + stored, then enqueued for
//     deeper analysis. The async worker updates review_status.
package moderation

import (
	"context"
	"time"
)

// Verdict is the outcome of a moderation check.
type Verdict int

const (
	VerdictApprove   Verdict = 1
	VerdictReject    Verdict = 2
	VerdictReview    Verdict = 3 // needs human review
	VerdictUndecided Verdict = 0 // async decision pending
)

// Decision is the full result of a moderation check. Labels is an
// arbitrary provider-specific JSON-serializable value (nsfw score,
// violence score, OCR hits, etc.) that gets stored on the image record.
type Decision struct {
	Verdict Verdict        `json:"verdict"`
	Labels  map[string]any `json:"labels,omitempty"`
	Reason  string         `json:"reason,omitempty"`
	Elapsed time.Duration  `json:"elapsed"`
}

// Provider is implemented by concrete moderation backends.
type Provider interface {
	// Name returns the provider identifier for logs and metrics.
	Name() string

	// SyncCheck is called inline during upload. Should respect the
	// passed context's deadline strictly. If the provider is too slow
	// or errors, return VerdictUndecided and let the async worker
	// handle it.
	SyncCheck(ctx context.Context, body []byte, mime string) (*Decision, error)

	// AsyncCheck is called by the worker for the queued images. Can
	// be slower and more thorough than SyncCheck.
	AsyncCheck(ctx context.Context, body []byte, mime string) (*Decision, error)
}
