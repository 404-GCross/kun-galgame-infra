package main

import "testing"

func TestSourceURL(t *testing.T) {
	cases := []struct{ in, want string }{
		// moyu: prefer the full-size original over the stored "-mini" thumbnail.
		{"https://image.moyu.moe/user/avatar/user_1/avatar-mini.avif", "https://image.moyu.moe/user/avatar/user_1/avatar.avif"},
		// kungal + anything else: unchanged.
		{"https://image.kungal.com/avatar/user_8/avatar.webp", "https://image.kungal.com/avatar/user_8/avatar.webp"},
		// only the moyu host gets rewritten, and only the first "-mini.".
		{"https://example.com/x-mini.avif", "https://example.com/x-mini.avif"},
	}
	for _, c := range cases {
		if got := sourceURL(c.in); got != c.want {
			t.Errorf("sourceURL(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestSniff(t *testing.T) {
	cases := []struct {
		name string
		b    []byte
		want string
	}{
		{"webp", []byte("RIFF\x00\x00\x00\x00WEBPVP8 "), "webp"},
		{"png", []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\x00"), "png"},
		{"jpeg", []byte{0xFF, 0xD8, 0xFF, 0xE0, 0, 0}, "jpeg"},
		{"gif", []byte("GIF89a\x00\x00"), "gif"},
		// minimal ISO-BMFF ftyp box with the avif major brand
		{"avif", []byte("\x00\x00\x00\x20ftypavifmif1miafMA1B"), "avif"},
		{"avis", []byte("\x00\x00\x00\x20ftypavismif1miafMA1B"), "avif"},
		{"unknown", []byte("not an image at all"), "unknown"},
		{"tooShort", []byte{0x00}, "unknown"},
	}
	for _, c := range cases {
		if got := sniff(c.b); got != c.want {
			t.Errorf("sniff(%s) = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestExtOf(t *testing.T) {
	cases := []struct{ in, want string }{
		{"https://image.moyu.moe/a/b/avatar.avif", "avif"},
		{"https://image.kungal.com/avatar/user_8/avatar.webp", "webp"},
		{"https://x.com/a.PNG?v=2", "png"},
		{"https://x.com/no-extension", "(none)"},
	}
	for _, c := range cases {
		if got := extOf(c.in); got != c.want {
			t.Errorf("extOf(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
