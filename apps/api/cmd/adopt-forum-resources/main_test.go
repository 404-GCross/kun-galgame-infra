package main

import "testing"

// originalFilename recovers the user's filename from a legacy toolset key
// `toolset/{tid}/{uid}_{base}_{salt}{ext}` (salt = 7 lowercase hex), and must
// fall back to the raw basename when the key doesn't match that shape.
func TestOriginalFilename(t *testing.T) {
	cases := []struct{ in, want string }{
		{"toolset/123/45_某工具_a1b2c3d.zip", "某工具.zip"},
		{"toolset/9/7_My_Tool_v2_0f1e2d3.rar", "My_Tool_v2.rar"}, // base keeps underscores
		{"toolset/1/2_x_abcdef0.7z", "x.7z"},
		{"toolset/1/weirdkey.zip", "weirdkey.zip"},                   // no salt shape → raw basename
		{"toolset/1/45_某工具_notHEX.zip", "45_某工具_notHEX.zip"}, // trailing not 7-lowercase-hex → fallback
	}
	for _, c := range cases {
		if got := originalFilename(c.in); got != c.want {
			t.Errorf("originalFilename(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
