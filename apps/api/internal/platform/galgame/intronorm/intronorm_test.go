package intronorm

import (
	"reflect"
	"testing"
)

func TestNormalizeEnglishIntro(t *testing.T) {
	cases := []struct {
		name      string
		in        string
		wantOut   string
		wantImgs  []string
		wantChang bool
	}{
		{
			name:      "clean intro untouched",
			in:        "A normal description.\n\nSecond paragraph.",
			wantOut:   "A normal description.\n\nSecond paragraph.",
			wantChang: false,
		},
		{
			name:      "existing markdown link preserved",
			in:        "See [the site](https://example.com) for more.",
			wantOut:   "See [the site](https://example.com) for more.",
			wantChang: false,
		},
		{
			name:      "pure CRLF is not a change",
			in:        "Line one.\r\nLine two.",
			wantOut:   "Line one.\r\nLine two.",
			wantChang: false,
		},
		{
			name:      "trailer with nested url removed",
			in:        "Some story text.\n\n[From [url=https://www.getchu.com/soft.phtml?id=666354]getchu[/url]]",
			wantOut:   "Some story text.",
			wantChang: true,
		},
		{
			name:      "trailer plain shop removed",
			in:        "...comes back with a different name, Shiori...\n\n[From ErogeShop]",
			wantOut:   "...comes back with a different name, Shiori...",
			wantChang: true,
		},
		{
			name:      "edited-from escaped trailer removed",
			in:        "In the afternoon they meet.\n\n\\[edited from \\[url=<http://example.org/x>]Li's Fun House\\[/url]\\]",
			wantOut:   "In the afternoon they meet.",
			wantChang: true,
		},
		{
			name:      "inline labeled url to markdown",
			in:        "Buy it on [url=https://store.steampowered.com/app/1]Steam[/url] today.",
			wantOut:   "Buy it on [Steam](https://store.steampowered.com/app/1) today.",
			wantChang: true,
		},
		{
			name:      "bare url to autolink",
			in:        "Homepage: [url]https://example.com[/url]",
			wantOut:   "Homepage: <https://example.com>",
			wantChang: true,
		},
		{
			name:      "spoiler and bold stripped to plain text",
			in:        "The hero is [b]strong[/b] but [spoiler]secretly the villain[/spoiler].",
			wantOut:   "The hero is strong but secretly the villain.",
			wantChang: true,
		},
		{
			name:      "markdown image removed and collected",
			in:        "Intro text.\n\n![cg](https://shared.akamai.steamstatic.com/x/CG1.png?t=1)\n\nMore.",
			wantOut:   "Intro text.\n\nMore.",
			wantImgs:  []string{"https://shared.akamai.steamstatic.com/x/CG1.png?t=1"},
			wantChang: true,
		},
		{
			name:      "bbcode image removed and collected",
			in:        "Look: [img]https://example.com/a.jpg[/img] end.",
			wantOut:   "Look:  end.",
			wantImgs:  []string{"https://example.com/a.jpg"},
			wantChang: true,
		},
		{
			name:      "combined: url + spoiler + trailer + image",
			in:        "Plot with [url=http://x.com]link[/url] and [spoiler]twist[/spoiler].\n\n![s](http://x.com/i.png)\n\n[From [url=http://x.com]X[/url]]",
			wantOut:   "Plot with [link](http://x.com) and twist.",
			wantImgs:  []string{"http://x.com/i.png"},
			wantChang: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, imgs, changed := NormalizeEnglishIntro(tc.in)
			if changed != tc.wantChang {
				t.Fatalf("changed = %v, want %v\nout=%q", changed, tc.wantChang, out)
			}
			if out != tc.wantOut {
				t.Fatalf("out = %q\nwant %q", out, tc.wantOut)
			}
			if len(imgs) != 0 || len(tc.wantImgs) != 0 {
				if !reflect.DeepEqual(imgs, tc.wantImgs) {
					t.Fatalf("images = %#v, want %#v", imgs, tc.wantImgs)
				}
			}
		})
	}
}

// Idempotence: normalizing an already-normalized intro must be a no-op.
func TestNormalizeEnglishIntro_Idempotent(t *testing.T) {
	inputs := []string{
		"Some story text.\n\n[From [url=https://x.com/a]getchu[/url]]",
		"Buy it on [url=https://x.com]Steam[/url].\n\n![cg](https://x.com/c.png)",
		"The hero is [b]strong[/b].",
	}
	for _, in := range inputs {
		once, _, _ := NormalizeEnglishIntro(in)
		twice, imgs, changed := NormalizeEnglishIntro(once)
		if changed {
			t.Errorf("second pass changed an already-clean intro:\nonce=%q\ntwice=%q", once, twice)
		}
		if twice != once {
			t.Errorf("not idempotent:\nonce=%q\ntwice=%q", once, twice)
		}
		if len(imgs) != 0 {
			t.Errorf("second pass found images %v in cleaned intro %q", imgs, once)
		}
	}
}
