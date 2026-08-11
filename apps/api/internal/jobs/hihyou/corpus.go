package hihyou

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// The corpus is the upstream response stored verbatim, one file per cv. It is
// the boundary between the two binaries: harvest-hihyou only writes it and
// import-hihyou-weekly only reads it, so iterating the segmentation never costs
// bilibili another request (and never risks a -509 mid-run rewriting a file).
type Corpus struct{ Dir string }

func (c Corpus) articleDir() string { return filepath.Join(c.Dir, "article") }
func (c Corpus) indexDir() string   { return filepath.Join(c.Dir, "index") }

func (c Corpus) ArticlePath(cv int64) string {
	return filepath.Join(c.articleDir(), fmt.Sprintf("cv%d.json", cv))
}

func (c Corpus) IndexPath(page int) string {
	return filepath.Join(c.indexDir(), fmt.Sprintf("p%d.json", page))
}

func (c Corpus) Mkdirs() error {
	for _, d := range []string{c.articleDir(), c.indexDir()} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
	}
	return nil
}

// Write replaces a file atomically. A partially written article is worse than a
// missing one: it parses as code != 0 and looks exactly like a rate-limited
// response, so the next pass would "retry" a file that was actually complete.
func (c Corpus) Write(path string, body []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, body, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Has reports whether a usable article is already on disk. A stored -509
// envelope counts as absent, which is what makes a second-chance pass a matter
// of re-running the same command.
func (c Corpus) Has(cv int64) bool {
	b, err := os.ReadFile(c.ArticlePath(cv))
	if err != nil {
		return false
	}
	a, err := ParseArticle(b)
	return err == nil && a.Code == 0
}

type CorpusEntry struct {
	Path    string
	Article *Article
}

// Load returns every complete article in the corpus, sorted by issue number,
// plus the paths of the incomplete ones.
func (c Corpus) Load() ([]CorpusEntry, []string, error) {
	paths, err := filepath.Glob(filepath.Join(c.articleDir(), "cv*.json"))
	if err != nil {
		return nil, nil, err
	}
	var out []CorpusEntry
	var bad []string
	for _, p := range paths {
		b, err := os.ReadFile(p)
		if err != nil {
			return nil, nil, err
		}
		a, err := ParseArticle(b)
		if err != nil || a.Code != 0 {
			bad = append(bad, filepath.Base(p))
			continue
		}
		out = append(out, CorpusEntry{Path: p, Article: a})
	}
	sort.Slice(out, func(i, j int) bool {
		ni, _ := IssueNumber(out[i].Article.Data.Title)
		nj, _ := IssueNumber(out[j].Article.Data.Title)
		if ni != nj {
			return ni < nj
		}
		return out[i].Article.Data.ID < out[j].Article.Data.ID
	})
	sort.Strings(bad)
	return out, bad, nil
}
