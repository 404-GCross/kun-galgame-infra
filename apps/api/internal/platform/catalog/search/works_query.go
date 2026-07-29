// works_query.go — the Meilisearch half of the works PRODUCT search face
// (A2-1d, refs/proj/126 D5): page-based pagination, an opt-in facet
// distribution, five sort lanes and the relevance floor.
//
// It deliberately returns only ids + counts. The wire items are re-hydrated
// from Postgres into the works-list item shape, so a Meilisearch document field
// never becomes a public field (裁定 4) and the search row is byte-identical to
// the browse row a consumer already renders.
//
// ── why the whole filter set is pushed down here ────────────────────────────
//
// The deprecated wiki search filtered `content_limit` in SQL while leaving it
// out of the Meilisearch filter, so `total` counted rows the caller could never
// receive and sfw pagination silently lost rows. Every filter of this face is
// compiled into ONE Meilisearch expression instead, which is what makes
// `total`, the facet distribution and the page share a single gate by
// construction rather than by discipline.
package search

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"

	"github.com/meilisearch/meilisearch-go"
)

// worksSearchScoreThreshold is the minimum _rankingScore (0..1) a hit must
// clear. Carried over verbatim from the deprecated galgame search, where it was
// calibrated against real queries: without it a CJK query segmented into common
// tokens drags in a long tail of titles that merely share a token.
//
// It applies to TEXT queries only — an empty query has nothing to score, and a
// threshold there would empty out the filter-only browse case.
const worksSearchScoreThreshold = 0.4

// WorksQuery is one product-search request against the works index. Filter is a
// SERVER-BUILT Meilisearch expression (never client text); Sort is a Meilisearch
// sort expression ("" = the default ranking rules, i.e. relevance).
type WorksQuery struct {
	Q      string
	Filter string
	Sort   string
	Facets []string
	Page   int // 1-based
	Limit  int
	// SearchIntro widens the matched attribute set from the title family to the
	// whole searchable list, i.e. synopses too (A2-1f). False — the default —
	// pins attributesToSearchOn to WorksTitleSearchable, which is what keeps the
	// result set byte-identical to A2-1d's now that the index CONTAINS intro
	// fields. Never leave it to Meilisearch's default (the full list): that
	// would silently widen every existing caller's query.
	SearchIntro bool
}

// WorksResult is one page of the product search: the work ids in ranked order,
// the EXACT total over the same filter, and the facet distribution (nil unless
// facets were requested). Facet values are Meilisearch's raw string keys — the
// public projection re-keys the ones that have a public vocabulary.
type WorksResult struct {
	IDs    []int64
	Total  int64
	Facets map[string]map[string]int64
}

// SearchWorks runs one product search over the works index.
func (i *Indexer) SearchWorks(ctx context.Context, q WorksQuery) (WorksResult, error) {
	page, limit := q.Page, q.Limit
	if page < 1 {
		page = 1
	}
	if limit < 1 {
		limit = 20
	}
	text := sanitizeQuery(q.Q)

	req := &meilisearch.SearchRequest{
		Page:        int64(page),
		HitsPerPage: int64(limit),
		// Require ALL query terms (the deprecated face's ruling ①): a CJK title
		// that is not in the corpus must return nothing rather than the
		// common-token noise a Frequency strategy surfaces.
		MatchingStrategy: meilisearch.All,
	}
	if q.Filter != "" {
		req.Filter = q.Filter
	}
	if !q.SearchIntro {
		// Explicit restriction, not an omission — see WorksQuery.SearchIntro.
		req.AttributesToSearchOn = WorksTitleSearchable
	}
	if len(q.Facets) > 0 {
		req.Facets = q.Facets
	}
	switch {
	case text != "":
		req.RankingScoreThreshold = worksSearchScoreThreshold
		if q.Sort != "" {
			req.Sort = []string{q.Sort}
		}
	case q.Sort != "":
		req.Sort = []string{q.Sort}
		req.MatchingStrategy = meilisearch.Last
	default:
		// No text to rank on and no explicit order: relevance degenerates to the
		// popularity tiebreaker, which is the browse order the entity search
		// already uses for an empty query.
		req.Sort = []string{"popularity:desc"}
		req.MatchingStrategy = meilisearch.Last
	}

	resp, err := i.client.Index(IndexWorks).SearchWithContext(ctx, text, req)
	if err != nil {
		return WorksResult{}, err
	}

	out := WorksResult{IDs: make([]int64, 0, len(resp.Hits))}
	for _, h := range resp.Hits {
		var d struct {
			ID string `json:"id"`
		}
		if err := h.DecodeInto(&d); err != nil {
			continue
		}
		if id, ok := WorkDocIDToWorkID(d.ID); ok {
			out.IDs = append(out.IDs, id)
		}
	}
	out.Total = resp.TotalHits
	if out.Total == 0 {
		out.Total = resp.EstimatedTotalHits
	}
	if len(resp.FacetDistribution) > 0 {
		var dist map[string]map[string]int64
		if err := json.Unmarshal(resp.FacetDistribution, &dist); err == nil {
			out.Facets = dist
		}
	}
	return out, nil
}

// WorkDocID renders a catalog work id as its works-index primary key.
func WorkDocID(workID int64) string { return "w" + strconv.FormatInt(workID, 10) }

// WorkDocIDToWorkID is the inverse; ok=false for anything that is not a
// well-formed works-index key.
func WorkDocIDToWorkID(docID string) (int64, bool) {
	if !strings.HasPrefix(docID, "w") {
		return 0, false
	}
	id, err := strconv.ParseInt(docID[1:], 10, 64)
	if err != nil || id <= 0 {
		return 0, false
	}
	return id, true
}

// EscapeFilterValue makes a value safe inside a single-quoted Meilisearch
// filter string. Every caller-supplied value that reaches a filter goes through
// here — the numeric filters are int64 by the time they arrive, so this covers
// the only string axis (olang).
func EscapeFilterValue(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	return strings.ReplaceAll(s, `'`, `\'`)
}
