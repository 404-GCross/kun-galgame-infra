// Package hihyou turns Galgame 批评's Gal周报 column into individual news items.
//
// The authorisation is one sentence from 雪阿宜 — 「可以，注明出处就行」 — so the
// attribution and the column link on news_source are the shape of the permission,
// not decoration. Unlike ymgalnews, the body text here IS authorised: she granted
// the weekly's contents, split into items.
package hihyou

import (
	"encoding/json"
	"html"
	"regexp"
	"strings"
)

// Article is the payload of api.bilibili.com/x/article/view?id=<cv>. The reader
// takes the structured paragraphs, never data.content: the flat text has no
// heading markers left in it, which is the entire basis of the segmentation.
type Article struct {
	Code int         `json:"code"`
	Msg  string      `json:"message"`
	Data ArticleData `json:"data"`
}

type ArticleData struct {
	ID          int64  `json:"id"`
	Title       string `json:"title"`
	PublishTime int64  `json:"publish_time"`
	Opus        struct {
		Content struct {
			Paragraphs []Paragraph `json:"paragraphs"`
		} `json:"content"`
	} `json:"opus"`
}

const (
	// A paragraph carrying words. 1 and 9 both appear across the generations and
	// carry the same node shape.
	paraText    = 1
	paraPicture = 2
	// para_type 3 is a decorative horizontal rule, not content: the whole corpus
	// holds seven distinct URLs for 1,106 occurrences. An earlier prototype read
	// the images out of THIS type and skipped type 2, so it reported 842 "item
	// images" that were all separator rules and dropped all 8,275 real ones.
	paraRule    = 3
	paraTextAlt = 9
)

type Paragraph struct {
	ParaType int `json:"para_type"`
	Text     struct {
		Nodes []struct {
			Word struct {
				Words    string `json:"words"`
				FontSize int    `json:"font_size"`
				Style    struct {
					Bold bool `json:"bold"`
				} `json:"style"`
			} `json:"word"`
		} `json:"nodes"`
	} `json:"text"`
	Pic struct {
		Pics []struct {
			URL string `json:"url"`
		} `json:"pics"`
	} `json:"pic"`
}

type word struct {
	text string
	size int
	bold bool
}

func (p Paragraph) words() []word {
	out := make([]word, 0, len(p.Text.Nodes))
	for _, n := range p.Text.Nodes {
		if strings.TrimSpace(n.Word.Words) == "" {
			continue
		}
		out = append(out, word{text: n.Word.Words, size: n.Word.FontSize, bold: n.Word.Style.Bold})
	}
	return out
}

// text joins the whole paragraph before unescaping. A title routinely arrives
// split across nodes — 期126 sends 「《」 + 「Clover Memory&#39;s」 + 「》情报公开」 —
// and reading node-by-node yields an item whose title is 「《」.
func (p Paragraph) text() string {
	var b strings.Builder
	for _, w := range p.words() {
		b.WriteString(w.text)
	}
	return strings.TrimSpace(html.UnescapeString(b.String()))
}

// pictures normalises both generations of URL to https. The corpus mixes
// `https://…/bfs/article/` (early) with `http://…/bfs/new_dyn/` (current), and
// protocol-relative `//` on the decorative rules.
func (p Paragraph) pictures() []string {
	var out []string
	for _, pic := range p.Pic.Pics {
		u := strings.TrimSpace(pic.URL)
		switch {
		case u == "":
			continue
		case strings.HasPrefix(u, "//"):
			u = "https:" + u
		case strings.HasPrefix(u, "http://"):
			u = "https://" + strings.TrimPrefix(u, "http://")
		}
		out = append(out, u)
	}
	return out
}

// issueRe must not hard-code the 期 character: 期147 is the one article in 217
// titled 【Gal周报147】, and a regex requiring it drops that whole issue silently.
var issueRe = regexp.MustCompile(`Gal周报\s*(\d+)期?`)

// IssueNumber reports the weekly's issue number and whether the title is a
// Gal周报 at all — the column also carries reviews and translations we have no
// authorisation to split.
func IssueNumber(title string) (int, bool) {
	m := issueRe.FindStringSubmatch(title)
	if m == nil {
		return 0, false
	}
	n := 0
	for _, r := range m[1] {
		n = n*10 + int(r-'0')
	}
	return n, n > 0
}

func ParseArticle(b []byte) (*Article, error) {
	var a Article
	if err := json.Unmarshal(b, &a); err != nil {
		return nil, err
	}
	return &a, nil
}
