package search

import (
	"context"
	"strings"

	"github.com/meilisearch/meilisearch-go"
)

// IndexForType maps a public entity-search type (names|characters|labels) to
// its index uid. ok=false for an unknown type.
func IndexForType(t string) (uid string, ok bool) {
	switch t {
	case "names":
		return IndexCreditNames, true
	case "characters":
		return IndexCharacters, true
	case "labels":
		return IndexLabels, true
	default:
		return "", false
	}
}

// LocalesForUI maps a coarse UI locale (zh|ja|en) to the Meilisearch query
// locales the SERVER pins (doc 13 invariant 2 — the client never supplies raw
// Meili locales). en / unknown → nil (the default pipeline handles latin).
func LocalesForUI(locale string) []string {
	switch locale {
	case "zh":
		return []string{"cmn"}
	case "ja":
		return []string{"jpn"}
	default:
		return nil
	}
}

// SearchResult is one entity hit plus the total count.
type SearchResult struct {
	Hits  []EntityDoc
	Total int64
}

// SearchEntities queries one entity index. locales is server-set (invariant 2);
// an empty query returns the top entities by popularity. limit is applied as-is
// (the handler caps it).
func (i *Indexer) SearchEntities(ctx context.Context, uid, q string, locales []string, limit int) (SearchResult, error) {
	req := &meilisearch.SearchRequest{
		HitsPerPage:      int64(limit),
		Locales:          locales,
		MatchingStrategy: meilisearch.All,
	}
	q = sanitizeQuery(q)
	if q == "" {
		// No text to rank on → surface the most-credited entities first.
		req.Sort = []string{"popularity:desc"}
		req.MatchingStrategy = meilisearch.Last
	}
	resp, err := i.client.Index(uid).SearchWithContext(ctx, q, req)
	if err != nil {
		return SearchResult{}, err
	}
	hits := make([]EntityDoc, 0, len(resp.Hits))
	for _, h := range resp.Hits {
		var d EntityDoc
		if err := h.DecodeInto(&d); err == nil {
			hits = append(hits, d)
		}
	}
	total := resp.TotalHits
	if total == 0 {
		total = resp.EstimatedTotalHits
	}
	return SearchResult{Hits: hits, Total: total}, nil
}

// Name returns the entity's display name from whichever language bucket holds
// it (the buckets are mutually exclusive — invariant 1).
func (d EntityDoc) Name() string {
	switch {
	case d.NameJa != "":
		return d.NameJa
	case d.NameZh != "":
		return d.NameZh
	default:
		return d.NameOther
	}
}

// sanitizeQuery strips Meili query operators (- and ") that would otherwise be
// parsed as negation/phrase — the same guard the galgame search applies.
func sanitizeQuery(q string) string {
	if strings.ContainsAny(q, "-\"") {
		q = strings.Map(func(r rune) rune {
			if r == '-' || r == '"' {
				return ' '
			}
			return r
		}, q)
	}
	return strings.TrimSpace(q)
}
