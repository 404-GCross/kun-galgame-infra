package charportraits

import "testing"

func TestChRelPath(t *testing.T) {
	cases := []struct {
		id      string
		want    string
		wantErr bool
	}{
		// Bucket = id mod 100, zero-padded to 2 digits (must match the ops
		// rsync --files-from formula byte-for-byte).
		{"ch175652", "ch/52/175652.jpg", false},
		{"ch76359", "ch/59/76359.jpg", false},
		{"ch5", "ch/05/5.jpg", false},     // single digit → 05
		{"ch100", "ch/00/100.jpg", false}, // multiple of 100 → 00
		{"ch12", "ch/12/12.jpg", false},
		{"chXYZ", "", true}, // non-numeric → error
		{"", "", true},
	}
	for _, c := range cases {
		got, err := chRelPath(c.id)
		if c.wantErr {
			if err == nil {
				t.Errorf("chRelPath(%q): expected error, got %q", c.id, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("chRelPath(%q): unexpected error: %v", c.id, err)
			continue
		}
		if got != c.want {
			t.Errorf("chRelPath(%q) = %q, want %q", c.id, got, c.want)
		}
	}
}
