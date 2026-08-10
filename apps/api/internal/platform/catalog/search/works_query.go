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
	"log/slog"
	"strconv"
	"strings"

	"github.com/meilisearch/meilisearch-go"
)

const worksSearchScoreThreshold = 0.4

type WorksQuery struct {
	Q           string
	Filter      string
	Sort        string
	Facets      []string
	Page        int
	Limit       int
	SearchIntro bool
}

type WorksResult struct {
	IDs    []int64
	Total  int64
	Facets map[string]map[string]int64
}

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
		Page:             int64(page),
		HitsPerPage:      int64(limit),
		MatchingStrategy: meilisearch.All,
	}
	if q.Filter != "" {
		req.Filter = q.Filter
	}
	if !q.SearchIntro {
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
			slog.Warn("works search: undecodable hit dropped",
				"raw_id", string(h["id"]), "err", err)
			continue
		}
		id, ok := WorkDocIDToWorkID(d.ID)
		if !ok {
			slog.Warn("works search: hit with unparseable doc id dropped", "doc_id", d.ID)
			continue
		}
		out.IDs = append(out.IDs, id)
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

func WorkDocID(workID int64) string { return "w" + strconv.FormatInt(workID, 10) }

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

func EscapeFilterValue(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	return strings.ReplaceAll(s, `'`, `\'`)
}
