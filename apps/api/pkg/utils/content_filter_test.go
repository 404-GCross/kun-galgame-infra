package utils

import "testing"

func TestParseContentLimit(t *testing.T) {
	cases := []struct {
		name        string
		raw         string
		defaultWhen string
		want        string
	}{
		// Recognized values pass through unchanged regardless of default.
		{"sfw passes through", "sfw", "sfw", "sfw"},
		{"sfw passes through with batch default", "sfw", "", "sfw"},
		{"nsfw passes through", "nsfw", "sfw", "nsfw"},
		{"nsfw passes through with batch default", "nsfw", "", "nsfw"},

		// "all" is the explicit opt-in to "no filter".
		{"all → empty regardless of default", "all", "sfw", ""},
		{"all → empty with batch default", "all", "", ""},

		// Empty falls back to caller-chosen default.
		{"empty + browse default sfw", "", "sfw", "sfw"},
		{"empty + batch default empty", "", "", ""},

		// Unknown values must NOT silently bypass the filter — they fall
		// back to the safe caller default, never to "no filter". This is
		// the safe-by-default guarantee the call sites depend on.
		{"unknown + browse default", "garbage", "sfw", "sfw"},
		{"unknown + batch default", "garbage", "", ""},
		{"case-sensitive (SFW upper rejected)", "SFW", "sfw", "sfw"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := ParseContentLimit(c.raw, c.defaultWhen)
			if got != c.want {
				t.Fatalf("ParseContentLimit(%q, %q) = %q, want %q",
					c.raw, c.defaultWhen, got, c.want)
			}
		})
	}
}
