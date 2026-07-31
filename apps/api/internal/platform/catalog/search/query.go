package search

import (
	"context"
	"strings"

	"github.com/meilisearch/meilisearch-go"
)

// IndexForType maps a public entity-search type (names|characters|labels|
// works|tags) to its index uid. ok=false for an unknown type.
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

// LocalesForUI maps a coarse UI locale (zh|ja|en) to the Meilisearch query
// locales the SERVER pins (doc 13 invariant 2 — the client never supplies raw
// Meili locales). en / unknown → nil (the default pipeline handles latin).
//
// uid decides whether pinning is allowed at all: a query locale is only correct
// when the index pinned the SAME locale at write time. The works index pins
// nothing (wave 158 — see EnsureIndexes), so forcing the Chinese pipeline onto a
// Japanese title pasted into a zh UI would recreate the very miss that wave
// fixed. Pinning stays paired with the indexes that are pinned.
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

// SearchResult is one entity hit plus the total count.
type SearchResult struct {
	Hits  []EntityDoc
	Total int64
}

// SearchEntities queries one entity index. locales is server-set (invariant 2);
// an empty query returns the top entities by popularity. limit is applied as-is
// (the handler caps it). filter is a server-built Meili filter expression
// (wave 105: the public works nsfw gate) — "" for none; never client-supplied.
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

// queryOperators are the runes Meilisearch parses as query-string operators
// (leading '-' = negation, '"' = phrase delimiters) plus their FULLWIDTH
// compatibility forms, which Meilisearch normalizes to the ASCII operators
// before parsing them: '－' U+FF0D and '＂' U+FF02 behave exactly like '-' and
// '"'. Japanese titles use the fullwidth dash as a subtitle delimiter
// (`アヘ顔アクメ中毒 －人体改造で狂ってイク私を見ないで－`), so pasting such a title into the
// search box excluded the very work being searched — the same failure class the
// ASCII guard already covered, escaping through the fullwidth door.
//
// The Japanese long-vowel mark 'ー' (U+30FC) and the wave dash '～' (U+FF5E) are
// letters here, NOT operators, and are deliberately left alone.
const queryOperators = "-\"－＂"

// sanitizeQuery neutralizes those operators by mapping them to spaces (they are
// tokenizer separators anyway, so matching loses nothing).
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
