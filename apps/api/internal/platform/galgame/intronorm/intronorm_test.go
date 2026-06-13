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
		{
			name:      "inline-appended trailer with markdown link removed",
			in:        "He wins her heart.\n\n[From [Getchu](https://www.getchu.com/x)]",
			wantOut:   "He wins her heart.",
			wantChang: true,
		},
		{
			name:      "parenthesized attribution removed",
			in:        "A snowy love story.\n\n(edited from [Wikipedia](https://en.wikipedia.org/x))",
			wantOut:   "A snowy love story.",
			wantChang: true,
		},
		{
			name:      "markdown-prefixed trailer removed",
			in:        "Plastic memories.\n\n*[From [steam](https://store.steampowered.com/app/1)]",
			wantOut:   "Plastic memories.",
			wantChang: true,
		},
		{
			name:      "prose 'based on a true story' kept (no link)",
			in:        "It's loosely based on a true story. (Based on a true story.)",
			wantOut:   "It's loosely based on a true story. (Based on a true story.)",
			wantChang: false,
		},
		{
			name:      "mid-text 'Based on [novel](url)' kept",
			in:        "Based on [the novel](https://x.com/n) by Y, the game expands the plot.",
			wantOut:   "Based on [the novel](https://x.com/n) by Y, the game expands the plot.",
			wantChang: false,
		},
		{
			name:      "orphan url tag stripped to text",
			in:        "See [url=https://x.com]the site for details.",
			wantOut:   "See the site for details.",
			wantChang: true,
		},
		{
			name:      "mid-document attribution with content after removed",
			in:        "Main description here.\n\n[From [Hau](https://omochikaeri.wordpress.com/x)]\n\nEditor note added after the attribution.",
			wantOut:   "Main description here.\n\nEditor note added after the attribution.",
			wantChang: true,
		},
		{
			name:      "mid-document verb-less bracket kept",
			in:        "Para one.\n\n[A bracketed aside, not an attribution]\n\nPara two.",
			wantOut:   "Para one.\n\n[A bracketed aside, not an attribution]\n\nPara two.",
			wantChang: false,
		},
		{
			name:      "mid-document link-less from-phrase kept",
			in:        "Para one.\n\n[From now on everything changed]\n\nPara two.",
			wantOut:   "Para one.\n\n[From now on everything changed]\n\nPara two.",
			wantChang: false,
		},
		{
			name:      "malformed [/url>] closing converted",
			in:        "Buy on [url=https://x.com]Steam[/url>] now.",
			wantOut:   "Buy on [Steam](https://x.com) now.",
			wantChang: true,
		},
		{
			name:      "trailing malformed [From X[/url>]] removed",
			in:        "A great story.\n\n[From [url=<https://mangagamer.org/x>]MangaGamer[/url>]]",
			wantOut:   "A great story.",
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
		"He wins her heart.\n\n[From [Getchu](https://www.getchu.com/x)]",
		"A snowy love story.\n\n(edited from [Wikipedia](https://en.wikipedia.org/x))",
		"Plastic memories.\n\n*[From [steam](https://store.steampowered.com/app/1)]",
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
