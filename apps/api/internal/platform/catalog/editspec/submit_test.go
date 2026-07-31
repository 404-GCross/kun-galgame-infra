package editspec

import (
	"slices"
	"testing"
)

// TestSubmissionFieldsMatchTheMatrix pins the submission payload against the
// field table itself: a key added to catalog.work must make an explicit in/out
// decision here, and the only two keys currently out are the image facets.
// Without this, a new field would silently default to "not submittable" (the
// map has no entry) and the submission form would lose it with no error.
func TestSubmissionFieldsMatchTheMatrix(t *testing.T) {
	matrix := make([]string, 0, len(workFieldSpecs()))
	for _, f := range workFieldSpecs() {
		matrix = append(matrix, f.Key)
	}
	for key := range submissionFields {
		if !slices.Contains(matrix, key) {
			t.Fatalf("%s is submittable but not registered on catalog.work", key)
		}
	}
	var excluded []string
	for _, key := range matrix {
		if _, ok := submissionFields[key]; !ok {
			excluded = append(excluded, key)
		}
	}
	want := []string{FieldWorkCovers, FieldWorkScreenshots}
	if !slices.Equal(excluded, want) {
		t.Fatalf("excluded from submissions = %v, want %v", excluded, want)
	}
	// The exported list is the same set, in matrix order.
	if got := SubmissionFieldKeys(); len(got) != len(submissionFields) || !slices.Contains(got, FieldWorkDisplayName) {
		t.Fatalf("SubmissionFieldKeys: %v", got)
	}
}
