package service

import "errors"

var (
	ErrSubjectKindNotRegistered = errors.New("trust: subject_kind not registered for site")
	ErrReasonUnknown            = errors.New("trust: unknown report reason")
	ErrRateLimited              = errors.New("trust: reporter rate limit exceeded")
	ErrInvalidSubjectURL        = errors.New("trust: subject_url must be http(s) and at most 512 chars")
	ErrReviewItemNotFound       = errors.New("trust: review item not found")
	ErrAlreadyClaimed           = errors.New("trust: review item already claimed")
	ErrIllegalTransition        = errors.New("trust: illegal review-item state transition")
	ErrInvalidDecision          = errors.New("trust: invalid decision")
	ErrDispositionNotFound      = errors.New("trust: disposition not found")
	ErrNotDeadLetter            = errors.New("trust: disposition is not dead-lettered")
	ErrSubjectKindNotFound      = errors.New("trust: subject kind not found")
	ErrReasonNotFound           = errors.New("trust: report reason not found")
	ErrSubjectKindExists        = errors.New("trust: subject kind already exists")
	ErrForwarderNotAllowed      = errors.New("trust: client is not an allowed forwarder")
	ErrInvalidOutcome           = errors.New("trust: invalid resolve outcome")
	ErrTermEmpty                = errors.New("trust: term is empty after normalization")
	ErrTermInvalidKind          = errors.New("trust: term kind must be suspect(0) or banned(1)")
	ErrTermInvalidPurpose       = errors.New("trust: term purpose must be abuse(0) or compliance(1)")
	ErrTermExists               = errors.New("trust: an active term with this norm already exists for the site")
	ErrTermNotFound             = errors.New("trust: term not found")
)
