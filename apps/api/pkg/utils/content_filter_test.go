package utils

import "testing"

func TestParseContentLimit(t *testing.T) {
	cases := []struct {
		name        string
		raw         string
		defaultWhen string
		want        string
	}{
		{"sfw passes through", "sfw", "sfw", "sfw"},
		{"sfw passes through with batch default", "sfw", "", "sfw"},
		{"nsfw passes through", "nsfw", "sfw", "nsfw"},
		{"nsfw passes through with batch default", "nsfw", "", "nsfw"},

		{"all → empty regardless of default", "all", "sfw", ""},
		{"all → empty with batch default", "all", "", ""},

		{"empty + browse default sfw", "", "sfw", "sfw"},
		{"empty + batch default empty", "", "", ""},

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
