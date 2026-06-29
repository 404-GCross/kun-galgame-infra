package dto

import (
	"testing"

	"api/pkg/utils"
)

// TestReleaseDateEmptyStringAccepted guards the regression where editing a
// galgame with no release date failed validation: on a *string field,
// `omitempty` keys off pointer-nil-ness, so a non-nil pointer to "" was NOT
// skipped and the old `datetime=2006-01-02` tag rejected "". "" is a valid
// input ("no / cleared date") per the galgame_wiki contract — the write layer
// maps it to NULL. Now guarded by the `date_or_empty` tag.
func TestReleaseDateEmptyStringAccepted(t *testing.T) {
	empty := ""
	good := "1999-06-04"
	ym := "2026-06"     // year-month precision (P4b: now accepted)
	yr := "2026"        // year precision (P4b: now accepted)
	bad := "1999/06/04" // slashes — never valid
	badMonth := "2026-13"

	cases := []struct {
		name    string
		req     any
		wantErr bool
	}{
		{"update: cleared date (\"\")", &UpdateGalgameRequest{ReleaseDate: &empty}, false},
		{"update: valid date", &UpdateGalgameRequest{ReleaseDate: &good}, false},
		{"update: year-month", &UpdateGalgameRequest{ReleaseDate: &ym}, false},
		{"update: year only", &UpdateGalgameRequest{ReleaseDate: &yr}, false},
		{"update: omitted (nil)", &UpdateGalgameRequest{ReleaseDate: nil}, false},
		{"update: malformed date", &UpdateGalgameRequest{ReleaseDate: &bad}, true},
		{"update: bad month", &UpdateGalgameRequest{ReleaseDate: &badMonth}, true},
		{"pr: cleared date (\"\")", &SubmitPRRequest{ReleaseDate: &empty}, false},
		{"pr: valid date", &SubmitPRRequest{ReleaseDate: &good}, false},
		{"pr: year-month", &SubmitPRRequest{ReleaseDate: &ym}, false},
		{"pr: malformed date", &SubmitPRRequest{ReleaseDate: &bad}, true},
	}
	for _, tc := range cases {
		err := utils.Validate(tc.req)
		if tc.wantErr && err == nil {
			t.Errorf("%s: expected validation error, got nil", tc.name)
		}
		if !tc.wantErr && err != nil {
			t.Errorf("%s: expected no error, got %v", tc.name, err)
		}
	}
}
