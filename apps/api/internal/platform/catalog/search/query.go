package search

import (
	"context"
	"strings"

	"github.com/meilisearch/meilisearch-go"
)

func IndexForType(t string) (uid string, ok bool) {
	switch t {
	case "names":
		return IndexCreditNames, true
	case "characters":
		return IndexCharacters, true
	case "labels":
		return IndexLabels, true
	case "works":
		return IndexWorks, true
	case "tags":
		return IndexTags, true
	default:
		return "", false
	}
}

func LocalesForUI(uid, locale string) []string {
	if uid == IndexWorks {
		return nil
	}
	switch locale {
	case "zh":
		return []string{"cmn"}
	case "ja":
		return []string{"jpn"}
	default:
		return nil
	}
}

type SearchResult struct {
	Hits  []EntityDoc
	Total int64
}

func (i *Indexer) SearchEntities(ctx context.Context, uid, q string, locales []string, limit int, filter string) (SearchResult, error) {
	req := &meilisearch.SearchRequest{
		HitsPerPage:      int64(limit),
		Locales:          locales,
		MatchingStrategy: meilisearch.All,
	}
	if filter != "" {
		req.Filter = filter
	}
	q = sanitizeQuery(q)
	if q == "" {
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

const queryOperators = "-\"－＂"

func sanitizeQuery(q string) string {
	if strings.ContainsAny(q, queryOperators) {
		q = strings.Map(func(r rune) rune {
			if strings.ContainsRune(queryOperators, r) {
				return ' '
			}
			return r
		}, q)
	}
	return strings.TrimSpace(q)
}
