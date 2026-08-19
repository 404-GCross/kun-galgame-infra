package main

import "testing"

func TestNormalizeYesNo(t *testing.T) {
	cases := map[string]string{
		"Yes":             "yes",
		"yes, it is.":     "yes",
		"No.":             "no",
		"none visible":    "no",
		"partially nude":  "yes",
		"Partially":       "yes",
		"partial nudity":  "yes",
		"maybe":           "",
		"":                "",
		"it is uncertain": "",
	}
	for in, want := range cases {
		if got := normalizeYesNo(in); got != want {
			t.Errorf("normalizeYesNo(%q) = %q, want %q", in, got, want)
		}
	}
}
