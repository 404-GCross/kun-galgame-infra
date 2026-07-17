package editing

import (
	"errors"
	"fmt"
	"strings"
)

// Sentinel errors. HTTP faces map them onto status codes; the engine itself
// is transport-agnostic.
var (
	ErrUnknownEntityType = errors.New("editing: entity type not registered")
	ErrEntityNotFound    = errors.New("editing: entity not found")
	ErrProposalNotFound  = errors.New("editing: proposal not found")
	ErrRevisionNotFound  = errors.New("editing: revision not found")
	// ErrEmptyPatch: a proposal with no fields (at create), or whose
	// amendments removed every field (at merge — decline it instead).
	ErrEmptyPatch = errors.New("editing: patch has no fields")
	// ErrEmptyDelta: an amendment that sets and unsets nothing.
	ErrEmptyDelta = errors.New("editing: amendment delta is empty")
	// ErrNotOpen: a state transition on a proposal that is not open.
	ErrNotOpen = errors.New("editing: proposal is not open")
	// ErrNotProposer: withdraw is proposer-only.
	ErrNotProposer = errors.New("editing: only the proposer may withdraw")
	// ErrNoEffectiveChanges: every patched value already equals the current
	// state — merging would record an empty revision, so the engine refuses.
	ErrNoEffectiveChanges = errors.New("editing: patch has no effective changes")
)

// UnknownFieldError: a patch/delta references a key absent from the
// registry (422 — the eternal-key contract means this is always a caller
// bug, never a version skew).
type UnknownFieldError struct{ Key string }

func (e *UnknownFieldError) Error() string {
	return fmt.Sprintf("editing: unknown field key %q", e.Key)
}

// LockedFieldError: the field's effective propose rule is locked — nobody
// may propose it (422: a schema property, not a caller property).
type LockedFieldError struct{ Key string }

func (e *LockedFieldError) Error() string {
	return fmt.Sprintf("editing: field %q is locked", e.Key)
}

// ValidationError: a field value failed its registered validator (422).
type ValidationError struct {
	Key    string
	Reason string
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("editing: field %q: %s", e.Key, e.Reason)
}

// PermissionError: the caller fails the field's propose/review rule (403).
type PermissionError struct {
	Key    string
	Action string // "propose" | "review"
}

func (e *PermissionError) Error() string {
	return fmt.Sprintf("editing: not allowed to %s field %q", e.Action, e.Key)
}

// ConflictError: rebase found fields changed since the proposal's base
// revision that no amendment has re-adjudicated (409 + the field list; the
// review UI resolves per field by amending, doc 21 §2.3).
type ConflictError struct{ Keys []string }

func (e *ConflictError) Error() string {
	return fmt.Sprintf("editing: conflicting fields: %s", strings.Join(e.Keys, ", "))
}
