package editspec

import (
	"context"
	"fmt"
	"strings"

	"gorm.io/gorm"
)

// The SUBMISSION payload (wave 162, 161 §6.P3-verdict STOP-1).
//
// A product's submit wizard mints a work and fills its content in one request.
// The content half is NOT a second write path: it is this package's own field
// table (workFieldSpecs), driven by the same Validate closures and the same
// Apply closures — including the mirror gate — that the editing face uses. A
// field cannot mean one thing when a user edits it and another when the same
// user first types it into the submission form, and a second copy of the write
// rules is precisely how the wiki's two writers drifted apart.
//
// The payload is a SUBSET of the matrix, and the two exclusions are deliberate:
//
//   - covers / screenshots — an image facet references bytes that must already
//     exist behind an image-service hash. A submission form uploads first and
//     edits the facet afterwards; accepting hashes at mint time would let a
//     submission reference bytes nothing else in the registry does.
//   - nothing else. display_name / olang / content_rating / titles / intros /
//     display_nsfw / links and the four edge fields are all in.
//
// Two keys that are absent from the matrix ITSELF and therefore cannot appear
// here either: a release date (03 §6-1 — the submitted date becomes one curated
// catalog_release row, written by the mint, not a work column) and a status
// (lifecycle is claim_state, moved only by the semantic actions).

// submissionFields is the closed set of field keys a submission may carry.
// Pinned by test against workFieldSpecs so a field added to the matrix has to
// make an explicit in/out decision here rather than silently defaulting to
// "submittable".
var submissionFields = map[string]struct{}{
	FieldWorkDisplayName:   {},
	FieldWorkOLang:         {},
	FieldWorkContentRating: {},
	FieldWorkTitles:        {},
	FieldWorkIntros:        {},
	FieldWorkDisplayNSFW:   {},
	FieldWorkTagIDs:        {},
	FieldWorkLabels:        {},
	FieldWorkEngineIDs:     {},
	FieldWorkSeriesIDs:     {},
	FieldWorkLinks:         {},
}

// SubmissionFieldKeys lists the accepted keys in matrix order, for an error
// message and for the spec's documentation of the payload.
func SubmissionFieldKeys() []string {
	out := make([]string, 0, len(submissionFields))
	for _, f := range workFieldSpecs() {
		if _, ok := submissionFields[f.Key]; ok {
			out = append(out, f.Key)
		}
	}
	return out
}

// SubmissionFieldError is a payload this face refuses: an unregistered or
// non-submittable key, or a value its field's own validator rejected. Field is
// always set; Err is nil for the "not submittable" case.
type SubmissionFieldError struct {
	Field string
	Err   error
}

func (e *SubmissionFieldError) Error() string {
	if e.Err == nil {
		return fmt.Sprintf("%s is not a submittable field (accepted: %s)",
			e.Field, strings.Join(SubmissionFieldKeys(), ", "))
	}
	return fmt.Sprintf("%s: %v", e.Field, e.Err)
}

func (e *SubmissionFieldError) Unwrap() error { return e.Err }

// ApplyWorkFields validates and applies a submission payload onto a work inside
// the caller's transaction. It is the mint-time counterpart of an engine merge:
// same field table, same validators, same Apply closures, same gate — the only
// difference is that no proposal or revision precedes it, because there is no
// prior value to propose against.
//
// Iteration follows the field table's order rather than the map's, so a payload
// with two bad fields always reports the same one.
func ApplyWorkFields(ctx context.Context, tx *gorm.DB, workID int64, values map[string]any) error {
	for key := range values {
		if _, ok := submissionFields[key]; !ok {
			return &SubmissionFieldError{Field: key}
		}
	}
	for _, spec := range workFieldSpecs() {
		value, present := values[spec.Key]
		if !present {
			continue
		}
		if spec.Validate != nil {
			if err := spec.Validate(value); err != nil {
				return &SubmissionFieldError{Field: spec.Key, Err: err}
			}
		}
		if err := spec.Apply(ctx, tx, workID, value); err != nil {
			return err
		}
	}
	return nil
}
