package editspec

import (
	"context"
	"fmt"
	"strings"

	catmodel "api/internal/platform/catalog/model"

	"gorm.io/gorm"
)

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

func SubmissionFieldKeys() []string {
	out := make([]string, 0, len(submissionFields))
	for _, f := range workFieldSpecs() {
		if _, ok := submissionFields[f.Key]; ok {
			out = append(out, f.Key)
		}
	}
	return out
}

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

type SubmissionAnchor struct {
	SourceKey  string
	ExternalID string
}

func SubmissionAnchorsOf(value any) []SubmissionAnchor {
	urls, err := parseLinks(value)
	if err != nil {
		return nil
	}
	out := make([]SubmissionAnchor, 0, len(urls))
	for _, u := range urls {
		cl, ok := classifyWorkLink(u)
		if !ok || cl.LinkKind != catmodel.LinkKindProbable {
			continue
		}
		out = append(out, SubmissionAnchor{SourceKey: cl.SourceKey, ExternalID: cl.ExternalID})
	}
	return out
}

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
