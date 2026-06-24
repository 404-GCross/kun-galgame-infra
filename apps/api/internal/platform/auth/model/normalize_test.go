package model

import "testing"

func TestNormalizeEmail(t *testing.T) {
	cases := map[string]string{
		"Kun@kungal.com":     "kun@kungal.com",
		"  KUN@Kungal.COM  ": "kun@kungal.com",
		"already@low.com":    "already@low.com",
		"":                   "",
		"  ":                 "",
	}
	for in, want := range cases {
		if got := NormalizeEmail(in); got != want {
			t.Errorf("NormalizeEmail(%q) = %q, want %q", in, got, want)
		}
	}
}
