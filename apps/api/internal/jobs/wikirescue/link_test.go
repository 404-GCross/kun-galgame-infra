package wikirescue

import "testing"

// TestClassifyLink pins the link→(source, external_id) recipe. The cases that
// matter most are the DEGRADATIONS: a URL whose host we model but whose id we
// cannot extract must fall back to the generic web source carrying the full
// URL, never mint a wrong id in a real source's namespace.
func TestClassifyLink(t *testing.T) {
	cases := []struct {
		name   string
		in     string
		source string
		extID  string
		ok     bool
	}{
		{"x.com handle", "https://x.com/kunlab_official", "twitter", "kunlab_official", true},
		{"twitter.com handle with path", "https://twitter.com/Foo_Bar/status/123", "twitter", "foo_bar", true},
		{"twitter www + trailing slash", "https://www.twitter.com/AbcDef/", "twitter", "abcdef", true},
		{"twitter @ prefix", "https://x.com/@handle", "twitter", "handle", true},
		// x.com/i/... is a site feature, not a handle — taking "i" as an
		// identity would be a fabricated account.
		{"twitter reserved path degrades", "https://x.com/i/flow/login", "web", "https://x.com/i/flow/login", true},

		{"cien creator", "https://ci-en.dlsite.com/creator/12345", "cien", "12345", true},
		{"cien non-creator degrades", "https://ci-en.dlsite.com/article/999", "web", "https://ci-en.dlsite.com/article/999", true},

		{"steam app", "https://store.steampowered.com/app/1011940/Some_Game/", "steam", "1011940", true},
		// A curator id lives in a DIFFERENT namespace than an appid; writing it
		// as source=steam would collide with a real game's id.
		{"steam curator degrades", "https://store.steampowered.com/curator/36069420", "web", "https://store.steampowered.com/curator/36069420", true},

		{"pixiv users", "https://www.pixiv.net/users/8888", "pixiv", "8888", true},
		{"pixiv en users", "https://www.pixiv.net/en/users/8888", "pixiv", "8888", true},
		{"pixiv member.php", "https://www.pixiv.net/member.php?id=777", "pixiv", "777", true},

		{"dlsite workno", "https://www.dlsite.com/maniax/work/=/product_id/RJ123456.html", "dlsite", "RJ123456", true},
		{"dlsite lowercase workno normalizes", "https://www.dlsite.com/pro/work/=/product_id/vj012345.html", "dlsite", "VJ012345", true},
		{"dlsite circle page degrades", "https://www.dlsite.com/maniax/circle/profile/=/maker_id/RG12345.html", "web", "https://www.dlsite.com/maniax/circle/profile/=/maker_id/RG12345.html", true},

		// Both DMM URL shapes carry the same product-code namespace the
		// existing source-15 refs use.
		{"dmm cid", "https://www.dmm.co.jp/mono/pcgame/-/detail/=/cid=1699apc14801/", "dmm", "1699apc14801", true},
		{"dmm dlsoft detail path", "https://dlsoft.dmm.co.jp/detail/akbs_0125/", "dmm", "akbs_0125", true},
		{"dmm other page degrades", "http://dlsoft.dmm.co.jp/original/saikyo/index/", "web", "http://dlsoft.dmm.co.jp/original/saikyo/index/", true},

		{"unknown host is web", "https://key.visualarts.gr.jp/", "web", "https://key.visualarts.gr.jp/", true},
		// A leading space is real in the data (one baidu link) — trimming is
		// what keeps it from being rejected as a non-URL.
		{"leading whitespace trimmed", "  https://pan.baidu.com/s/abc?pwd=1234", "web", "https://pan.baidu.com/s/abc?pwd=1234", true},
		{"archive wrapper unwrapped", "https://web.archive.org/web/2019/https://example.com/game", "web", "https://example.com/game", true},

		{"not a url", "example.com/no-scheme", "", "", false},
		{"empty", "", "", "", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src, ext, ok := classifyLink(tc.in)
			if ok != tc.ok {
				t.Fatalf("ok = %v, want %v (in=%q)", ok, tc.ok, tc.in)
			}
			if !tc.ok {
				return
			}
			if src != tc.source {
				t.Errorf("source = %q, want %q (in=%q)", src, tc.source, tc.in)
			}
			if ext != tc.extID {
				t.Errorf("external_id = %q, want %q (in=%q)", ext, tc.extID, tc.in)
			}
		})
	}
}

// TestClassifyLinkNeverEmptyExternalID guards the invariant that a classified
// link always carries a non-empty external_id — an empty one would violate the
// NOT NULL column and, worse, create a nameless anchor.
func TestClassifyLinkNeverEmptyExternalID(t *testing.T) {
	inputs := []string{
		"https://x.com/a", "https://ci-en.dlsite.com/creator/1",
		"https://store.steampowered.com/app/1/", "https://www.pixiv.net/users/1",
		"https://www.dlsite.com/x/RJ111111", "https://dlsoft.dmm.co.jp/detail/x_0001/",
		"https://example.org/", "https://web.archive.org/web/1/https://a.example/b",
	}
	for _, in := range inputs {
		if _, ext, ok := classifyLink(in); ok && ext == "" {
			t.Errorf("classifyLink(%q) returned an empty external_id", in)
		}
	}
}
