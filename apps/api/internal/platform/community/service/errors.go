// Package service is the community platform domain logic: thread / post /
// reaction / feedback flows, application-maintained denormalized counters
// (doc 11 §4.3, no DB triggers), write-time sanitization, and the TL0 sandbox
// enforcement point (doc 11 §6 layer 2). The trust ENGINE (promotion metering,
// weighted flags, review queue) is step 04; the HTTP/S2S face is step 02
// Phase B.
package service

import "errors"

var (
	// ErrThreadNotFound is returned when a target thread id does not exist.
	ErrThreadNotFound = errors.New("community: thread not found")
	// ErrThreadNotOpen is returned when posting to a closed/hidden/deleted
	// thread.
	ErrThreadNotOpen = errors.New("community: thread not open")
	// ErrNotFeedback is returned when a feedback operation targets a
	// non-feedback thread.
	ErrNotFeedback = errors.New("community: thread is not a feedback thread")
	// ErrPostNotFound is returned when a target post id does not exist.
	ErrPostNotFound = errors.New("community: post not found")
	// ErrReviewNotFound is returned when a target review-queue item does not
	// exist (or is not actionable).
	ErrReviewNotFound = errors.New("community: review item not found")
	// ErrNotAuthor is returned when a user tries to edit or self-delete a post
	// they do not own (both are author-only operations).
	ErrNotAuthor = errors.New("community: not the post author")
	// ErrPostNotEditable is returned when editing a post that is not in an
	// editable state: a hidden/held or tombstoned post is not an editable surface
	// (editing is content-only; a removed post stays removed — invariant 13).
	ErrPostNotEditable = errors.New("community: post not editable")
)

// SandboxError is returned when a TL0 newcomer trips a sandbox limit (doc 11
// §6 layer 2). It is a distinct type so the HTTP face (Phase B) can map it to a
// 429/403 with the specific reason.
type SandboxError struct{ Reason string }

func (e *SandboxError) Error() string { return "community: sandbox limit: " + e.Reason }
