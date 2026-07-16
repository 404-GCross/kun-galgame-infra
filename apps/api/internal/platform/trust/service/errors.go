// Package service is the Trust & Safety platform domain logic: report intake
// (dedup / rate-limit / reputation weighting / threshold aggregation / negative-
// knowledge folding), the unified review inbox (claim / decide), the subject-
// kind + reason registries, the enforcement-callback dispatch worker, and the
// append-only audit hash chain. See refs/docs/nextmoe-draft/18.
package service

import "errors"

var (
	// ErrSubjectKindNotRegistered is the tenant fail-loud (invariant 11): the
	// caller's site has no such subject_kind registered → 422.
	ErrSubjectKindNotRegistered = errors.New("trust: subject_kind not registered for site")
	// ErrReasonUnknown is returned when a report's reason_key resolves to no
	// global or per-site reason.
	ErrReasonUnknown = errors.New("trust: unknown report reason")
	// ErrRateLimited is returned when a reporter exceeds the per-hour window
	// (章程 ruling 6) → structured 429.
	ErrRateLimited = errors.New("trust: reporter rate limit exceeded")
	// ErrInvalidSubjectURL is returned when a report's subject_url is not a
	// plausible http(s) link (BFFs construct it, so a bad value is a programming
	// error — fail-loud 422 rather than silently dropping the evidence link).
	ErrInvalidSubjectURL = errors.New("trust: subject_url must be http(s) and at most 512 chars")
	// ErrReviewItemNotFound is returned when a target review item does not
	// exist.
	ErrReviewItemNotFound = errors.New("trust: review item not found")
	// ErrAlreadyClaimed is returned when claiming an item another operator
	// already holds (or that is no longer pending) → 409.
	ErrAlreadyClaimed = errors.New("trust: review item already claimed")
	// ErrIllegalTransition is returned when a decide targets an item that is
	// already terminal (actioned/dismissed) → 409.
	ErrIllegalTransition = errors.New("trust: illegal review-item state transition")
	// ErrInvalidDecision is returned for a malformed decide (unknown decision,
	// or actioned without action/reason_code).
	ErrInvalidDecision = errors.New("trust: invalid decision")
	// ErrDispositionNotFound is returned when a target disposition does not
	// exist.
	ErrDispositionNotFound = errors.New("trust: disposition not found")
	// ErrNotDeadLetter is returned when redelivering a disposition that is not
	// in the dead_letter state.
	ErrNotDeadLetter = errors.New("trust: disposition is not dead-lettered")
	// ErrSubjectKindNotFound is returned when patching an absent subject kind.
	ErrSubjectKindNotFound = errors.New("trust: subject kind not found")
	// ErrReasonNotFound is returned when patching an absent reason.
	ErrReasonNotFound = errors.New("trust: report reason not found")
	// ErrSubjectKindExists is returned when registering a (site, key) that
	// already exists (registry rows are never deleted → re-open via PATCH).
	ErrSubjectKindExists = errors.New("trust: subject kind already exists")
	// ErrForwarderNotAllowed is returned when a client that is not on the
	// KUN_TRUST_FORWARDER_CLIENT_IDS allowlist calls forward/resolve → 403 (step
	// 03 ruling 3; the allowlist is the counterweight to forward carrying `site`
	// in its body rather than deriving it from the client binding).
	ErrForwarderNotAllowed = errors.New("trust: client is not an allowed forwarder")
	// ErrInvalidOutcome is returned when a forward/resolve outcome is neither
	// "approved" nor "rejected".
	ErrInvalidOutcome = errors.New("trust: invalid resolve outcome")
	// ErrTermEmpty is returned when a submitted Tier0 term normalizes to the
	// empty string (whitespace/format-only) — an empty norm would substring-match
	// every text, so it is rejected fail-loud (step 05).
	ErrTermEmpty = errors.New("trust: term is empty after normalization")
	// ErrTermInvalidKind is returned when a Tier0 term kind is neither suspect(0)
	// nor banned(1).
	ErrTermInvalidKind = errors.New("trust: term kind must be suspect(0) or banned(1)")
	// ErrTermExists is returned when creating a Tier0 term whose (site-or-global,
	// norm) already has an ACTIVE row (deprecate + re-create, never duplicate).
	ErrTermExists = errors.New("trust: an active term with this norm already exists for the site")
	// ErrTermNotFound is returned when deprecating an absent Tier0 term.
	ErrTermNotFound = errors.New("trust: term not found")
)
