package service

import "testing"

func TestMimeAllowed(t *testing.T) {
	cases := []struct {
		name    string
		mime    string
		fname   string
		allowed []string
		want    bool
	}{
		{"empty allowlist allows anything", "application/octet-stream", "x.bin", nil, true},
		{"ext match with dot", "", "game.zip", []string{".zip", ".7z"}, true},
		{"ext match without dot in allowlist", "", "game.zip", []string{"zip"}, true},
		{"ext match case-insensitive", "", "GAME.ZIP", []string{".zip"}, true},
		{"mime match", "application/zip", "game", []string{"application/zip"}, true},
		{"mime match case-insensitive", "application/ZIP", "game", []string{"application/zip"}, true},
		{"denied ext", "", "game.rar", []string{".zip", ".7z"}, false},
		{"denied mime", "video/mp4", "clip", []string{"application/zip"}, false},
		{"blank entries ignored, still denies", "", "game.rar", []string{"", "  ", ".zip"}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := mimeAllowed(c.mime, c.fname, c.allowed); got != c.want {
				t.Errorf("mimeAllowed(%q,%q,%v) = %v, want %v", c.mime, c.fname, c.allowed, got, c.want)
			}
		})
	}
}

func TestExtForKey(t *testing.T) {
	cases := []struct{ in, want string }{
		{"normal.zip", ".zip"},
		{"GAME.RAR", ".rar"},
		{"archive.7z", ".7z"},
		{"战国兰斯.zip", ".zip"},        // CJK basename, clean ext
		{"no-extension", ""},          // no dot
		{"weird. zip", ""},            // space in ext → opaque
		{"trailingdot.", ""},          // empty ext after dot
		{"", ""},
	}
	for _, c := range cases {
		if got := extForKey(c.in); got != c.want {
			t.Errorf("extForKey(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
