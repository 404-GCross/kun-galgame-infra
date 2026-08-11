// Package imagerefs is the ONE registry of every kun_news column holding an
// image hash. A column missing from it is invisible to the daily refping sweep,
// and nothing fails when you forget: the upload succeeds, the feed renders, and
// the bytes are collected about thirteen months later when the image service's
// TTL elapses — the "refping site-scope GC fuse" class that once froze 66k
// galgame images and lost getchu's 立绘. Adding an image column in news scope
// means adding it HERE in the same change.
package imagerefs

import (
	"context"
	"fmt"
	"strings"

	"gorm.io/gorm"
)

type columnSpec struct {
	Table  string
	Column string
}

var specs = []columnSpec{
	{Table: "news_item", Column: "banner_hash"},
	{Table: "news_item_image", Column: "image_hash"},
}

// DistinctHashes returns every news-scope image hash currently referenced,
// including rows the public face hides (withdrawn / dead): an item we pulled
// from the feed is still an item whose bytes we must not let the GC eat while
// the moderation decision is reversible.
func DistinctHashes(ctx context.Context, db *gorm.DB) ([]string, error) {
	branches := make([]string, 0, len(specs))
	for _, s := range specs {
		branches = append(branches, fmt.Sprintf(
			"SELECT DISTINCT t.%s AS hash FROM %s t WHERE t.%s <> ''",
			s.Column, s.Table, s.Column))
	}
	var hashes []string
	q := "SELECT DISTINCT hash FROM (\n" + strings.Join(branches, "\nUNION ALL\n") + "\n) u"
	if err := db.WithContext(ctx).Raw(q).Scan(&hashes).Error; err != nil {
		return nil, err
	}
	return hashes, nil
}

// Columns reports the registered (table, column) pairs so a test can assert the
// registry covers every hash-bearing column the schema actually has.
func Columns() [][2]string {
	out := make([][2]string, 0, len(specs))
	for _, s := range specs {
		out = append(out, [2]string{s.Table, s.Column})
	}
	return out
}
