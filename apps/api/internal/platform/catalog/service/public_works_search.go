// public_works_search.go — the works PRODUCT search face
// (GET /v1/catalog/works/search, A2-1d / refs/proj/126 D5).
//
// This is a second, deliberately separate door from GET /v1/catalog/search: that
// one is the four-family entity autocomplete (20 hits, index projection, wire
// frozen) and stays untouched. This one is the product search a galgame site
// renders a results page with — page/limit pagination, the full works filter
// set, an opt-in facet distribution, five sort lanes.
//
// ── the three rules that shape everything below ─────────────────────────────
//
//  1. ONE GATE. Every filter is compiled into the Meilisearch expression, so
//     `total`, the facet distribution and `items` are three views of the SAME
//     candidate set. The deprecated face left content_limit out of its Meili
//     filter and applied it in SQL instead — its `total` counted rows the caller
//     could never receive, and sfw pagination lost rows silently. This face is
//     written to make that shape unrepresentable.
//
//  2. IDS FROM MEILI, BYTES FROM POSTGRES. Meilisearch supplies ranked ids and
//     counts, nothing else; every item is re-hydrated through the works list's
//     own enrichWorkListItems, so a search row is byte-identical to a browse row
//     (include= blocks and all) and no Meilisearch document field can leak onto
//     the wire (裁定 4).
//
//  3. PUBLIC TOKENS ONLY. The facet vocabulary is the FILTER PARAMETER names
//     (tag_id, content_rating, …), not the index attribute names, and the
//     content_rating distribution is re-keyed to the public string vocabulary.
package service

import (
	"context"
	stderrors "errors"
	"strconv"
	"strings"
	"time"

	"api/internal/platform/catalog/dto"
	"api/internal/platform/catalog/model"
	catsearch "api/internal/platform/catalog/search"
)

// ErrSearchUnavailable is returned when the face is reached without a search
// indexer wired (a deployment misconfiguration, not caller error).
var ErrSearchUnavailable = stderrors.New("catalog: works search indexer not configured")

// WithWorksSearch wires the Meilisearch indexer the product search runs on.
// Returns the service for fluent chaining, matching WithImageMeta. nil leaves
// the face unavailable rather than silently degrading to an unfiltered list —
// a search endpoint that quietly stops searching is the failure class this
// whole wave exists to avoid.
func (s *PublicService) WithWorksSearch(idx *catsearch.Indexer) *PublicService {
	s.worksSearch = idx
	return s
}

// WorksSearchFilter is the product search's request shape. Every filter field
// is the works LIST field of the same name, with the same meaning — a consumer
// moves a query between the two faces by changing the path only.
type WorksSearchFilter struct {
	Q             string // free text; empty = a filter-only browse
	ContentRating *int16 // model.ContentRating* — nil = all (r18 still needs NSFW)
	Claimed       *bool  // true = claimed only; false = bodyless only; nil = both
	LabelID       int64
	// TagIDs are canonical tag ids ANDed together (A2-1e) — the works list's
	// tag_id semantics verbatim.
	TagIDs         []int64
	EngineID       int64
	SeriesID       int64
	ReleasedAfter  int64 // composed ordinal (y*10000+m*100+d), inclusive; 0 = unbounded
	ReleasedBefore int64
	OLang          PublicOLang
	NSFW           bool
	Sort           string   // relevance (default) | released_desc | released_asc | updated | popularity
	Facets         []string // public filter-parameter tokens; empty = no distribution
	Page           int      // 1-based
	Limit          int
	Include        WorksListInclude
	// SearchIntro widens free-text matching from titles to synopses (A2-1f).
	// Default false = the A2-1d behavior byte for byte.
	SearchIntro bool
}

// worksSearchFacetAttr maps a public facet token — which is the FILTER
// PARAMETER's own name — to the works index attribute it distributes over. This
// map IS the closed vocabulary: a token outside it is a 400 at the handler.
var worksSearchFacetAttr = map[string]string{
	"content_rating": "content_rating",
	"olang":          "olang",
	"claimed":        "claimed",
	"tag_id":         "tag_ids",
	"label_id":       "label_ids",
	"engine_id":      "engine_ids",
	"series_id":      "series_ids",
	"source":         "source_keys",
}

// WorksSearchFacetTokens lists the legal facets= tokens in a stable order (the
// handler quotes it in its 400 message, the spec documents it).
//
// The numeric scalars (released_ord, updated_ts, popularity) are deliberately
// absent: a distribution over 20240614-style ordinals or over Unix seconds is
// one bucket per value, which is a listing, not a facet.
var WorksSearchFacetTokens = []string{
	"content_rating", "olang", "claimed", "tag_id", "label_id", "engine_id", "series_id", "source",
}

// IsWorksSearchFacet reports whether a token is in the closed facet vocabulary.
func IsWorksSearchFacet(tok string) bool {
	_, ok := worksSearchFacetAttr[tok]
	return ok
}

// WorksSearchSortRule maps a public sort token to its Meilisearch sort
// expression. ok=false for a token outside the closed vocabulary; "" with
// ok=true means relevance (no sort — the ranking rules decide).
//
// `popularity` replaces the deprecated face's `view` lane: that was the wiki's
// own page-view counter, which the catalog registry has no counterpart for.
// popularity is the cross-population signal the works index has carried since
// wave 105 — log1p(max(bangumi collect shelf, DLsite download count)) — so this
// lane promotes the tiebreaker that already decides ties into the primary key.
var worksSearchSortRules = map[string]string{
	"":              "",
	"relevance":     "",
	"released_desc": "released_ord:desc",
	"released_asc":  "released_ord:asc",
	"updated":       "updated_ts:desc",
	"popularity":    "popularity:desc",
}

// WorksSearchSortTokens lists the legal sort= tokens in a stable order.
var WorksSearchSortTokens = []string{"relevance", "released_desc", "released_asc", "updated", "popularity"}

// WorksSearchSortRule resolves a sort token; ok=false = outside the vocabulary.
func WorksSearchSortRule(sort string) (rule string, ok bool) {
	rule, ok = worksSearchSortRules[strings.TrimSpace(sort)]
	return rule, ok
}

// WorksSearch serves one page of the product search.
func (s *PublicService) WorksSearch(ctx context.Context, f WorksSearchFilter) (dto.PublicWorksSearchData, error) {
	if s.worksSearch == nil {
		return dto.PublicWorksSearchData{}, ErrSearchUnavailable
	}
	page, limit := f.Page, f.Limit
	if page < 1 {
		page = 1
	}
	limit = clampBrowseLimit(limit)

	text := f.Q
	docID := ""
	// The v\d+ short-circuit (裁定 5): a query that IS a VNDB work id means the
	// caller wants that one work, not a full-text guess — full-text would
	// prefix-bleed (v1965 also matches v19650). Resolving it through the exact
	// anchor registry and then pinning the document keeps ONE code path: the
	// caller's filters, the facet distribution and the envelope are unchanged,
	// the candidate set is simply at most one work. An unresolvable id is an
	// empty result, never a 404 — this is a search face, not a lookup face.
	if vid := normalizeVNDBID(f.Q); vid != "" {
		workID, err := s.lookupEntityID(ctx, "vndb", vid, model.EntityTypeWork)
		if err != nil {
			return dto.PublicWorksSearchData{}, err
		}
		// An unresolvable id still goes through the normal query, pinned to a
		// document id no work can have (ids are positive). That keeps ONE code
		// path, so the miss comes back as a fully-formed envelope — total 0,
		// empty facet buckets — instead of a special-cased shape.
		text, docID = "", catsearch.WorkDocID(workID)
	}

	rule, _ := WorksSearchSortRule(f.Sort)
	res, err := s.worksSearch.SearchWorks(ctx, catsearch.WorksQuery{
		Q:           text,
		Filter:      f.meiliFilter(docID),
		Sort:        rule,
		Facets:      worksSearchMeiliFacets(f.Facets),
		Page:        page,
		Limit:       limit,
		SearchIntro: f.SearchIntro,
	})
	if err != nil {
		return dto.PublicWorksSearchData{}, err
	}

	items, err := s.hydrateWorkIDs(ctx, res.IDs, f.NSFW, f.Include)
	if err != nil {
		return dto.PublicWorksSearchData{}, err
	}
	return dto.PublicWorksSearchData{
		Total: res.Total, Page: page, Limit: limit, Items: items,
		Facets: projectWorksSearchFacets(f.Facets, res.Facets),
	}, nil
}

// meiliFilter compiles the whole filter set into one Meilisearch expression.
// Fixed operators, values bound as numbers or escaped strings — no caller text
// ever reaches the expression unquoted.
//
// The index population (LIVE, non-deleted, galgame medium) is baked into the
// index itself by cmd/reindex-catalog, so it needs no clause here; the gates
// that vary per caller are all below.
func (f WorksSearchFilter) meiliFilter(docID string) string {
	var clauses []string
	if docID != "" {
		clauses = append(clauses, "id = '"+catsearch.EscapeFilterValue(docID)+"'")
	}
	if !f.NSFW {
		clauses = append(clauses, "content_rating != "+strconv.Itoa(int(model.ContentRatingR18)))
	}
	if f.ContentRating != nil {
		clauses = append(clauses, "content_rating = "+strconv.Itoa(int(*f.ContentRating)))
	}
	if f.Claimed != nil {
		clauses = append(clauses, "claimed = "+strconv.FormatBool(*f.Claimed))
	}
	for _, tagID := range f.TagIDs {
		// One clause per tag, ANDed: `array = value` is Meilisearch's "contains"
		// on a multi-valued attribute, so repeating it is exactly the works
		// list's chain of EXISTS sub-selects.
		clauses = append(clauses, "tag_ids = "+strconv.FormatInt(tagID, 10))
	}
	for _, e := range []struct {
		attr string
		id   int64
	}{
		{"label_ids", f.LabelID}, {"engine_ids", f.EngineID}, {"series_ids", f.SeriesID},
	} {
		if e.id > 0 {
			clauses = append(clauses, e.attr+" = "+strconv.FormatInt(e.id, 10))
		}
	}
	if f.ReleasedAfter > 0 {
		clauses = append(clauses, "released_ord >= "+strconv.FormatInt(f.ReleasedAfter, 10))
	}
	if f.ReleasedBefore > 0 {
		clauses = append(clauses, "released_ord <= "+strconv.FormatInt(f.ReleasedBefore, 10))
	}
	if olang := f.OLang.meiliFilter(); olang != "" {
		clauses = append(clauses, olang)
	}
	return strings.Join(clauses, " AND ")
}

// worksSearchMeiliFacets translates the requested public tokens to index
// attributes, preserving caller order and dropping duplicates. Unknown tokens
// cannot arrive (the handler 400s them) and are ignored defensively.
func worksSearchMeiliFacets(tokens []string) []string {
	if len(tokens) == 0 {
		return nil
	}
	out := make([]string, 0, len(tokens))
	seen := make(map[string]bool, len(tokens))
	for _, t := range tokens {
		attr, ok := worksSearchFacetAttr[t]
		if !ok || seen[attr] {
			continue
		}
		seen[attr] = true
		out = append(out, attr)
	}
	return out
}

// projectWorksSearchFacets re-keys Meilisearch's distribution back onto the
// public vocabulary: the outer key becomes the FILTER PARAMETER name the
// consumer would pass back, and content_rating's inner keys become the public
// strings (all_ages | sensitive | r18) instead of the enum ints — the same rule
// every other content_rating on this face follows.
func projectWorksSearchFacets(tokens []string, dist map[string]map[string]int64) map[string]map[string]int64 {
	if len(tokens) == 0 || len(dist) == 0 {
		return nil
	}
	out := make(map[string]map[string]int64, len(tokens))
	for _, t := range tokens {
		attr, ok := worksSearchFacetAttr[t]
		if !ok {
			continue
		}
		values, ok := dist[attr]
		if !ok {
			continue
		}
		bucket := make(map[string]int64, len(values))
		for k, n := range values {
			if t == "content_rating" {
				if rating, err := strconv.Atoi(k); err == nil {
					if key := contentRatingKey(int16(rating)); key != "" {
						bucket[key] = n
						continue
					}
				}
			}
			bucket[k] = n
		}
		out[t] = bucket
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// normalizeVNDBID returns the lowercased VNDB id when q is EXACTLY a VNDB work
// id ("v" followed by one or more digits, case-insensitive), else "". Semantics
// carried over from the deprecated galgame search — the wire form is different
// (that face matched a vndb_id column on its own documents; this one resolves
// the exact anchor registry), the detection rule is the same.
func normalizeVNDBID(q string) string {
	q = strings.TrimSpace(q)
	if len(q) < 2 || (q[0] != 'v' && q[0] != 'V') {
		return ""
	}
	for i := 1; i < len(q); i++ {
		if q[i] < '0' || q[i] > '9' {
			return ""
		}
	}
	return strings.ToLower(q)
}

// hydrateWorkIDs turns Meilisearch's ranked ids into wire items, preserving the
// ranked order (a single WHERE id IN (…) plus a Go-side reorder — never one
// query per hit).
//
// The population predicate and the nsfw gate are re-applied here as a STALENESS
// GUARD, not as a second gate: both already ran inside Meilisearch, which is
// also what `total` counted. If the index is momentarily ahead of the registry
// (a work delisted or re-rated since the last reindex) the page comes back one
// row short rather than serving a row this caller must not see. That is the
// opposite of the deprecated face's trap, where the SQL filter applied a rule
// the Meilisearch filter deliberately never had.
func (s *PublicService) hydrateWorkIDs(ctx context.Context, ids []int64, nsfw bool, inc WorksListInclude) ([]dto.PublicWorkListItem, error) {
	if len(ids) == 0 {
		return []dto.PublicWorkListItem{}, nil
	}
	where := []string{"w.deleted_at IS NULL", "w.status = ?", "w.medium_id = ?", "w.id IN ?"}
	args := []any{model.WorkStatusLive, galgameMediumID, ids}
	if !nsfw {
		where = append(where, "w.content_rating <> ?")
		args = append(args, model.ContentRatingR18)
	}
	var rows []struct {
		ID          int64
		MediumID    int16
		DisplayName string
		// Explicit column tag: GORM snake-cases the field to o_lang, which
		// matches no result column (the W1 works-list trap, fixed in 0dce1f36).
		OLang         string `gorm:"column:olang"`
		ContentRating int16
		Site          *string
		ProductWorkID *int64
		ClaimState    *int16 `gorm:"column:claim_state"`
		UpdatedAt     time.Time
	}
	q := `SELECT w.id, w.medium_id, w.display_name, w.olang, w.content_rating, w.site, w.product_work_id, w.claim_state, w.updated_at
		FROM catalog_work w WHERE ` + strings.Join(where, " AND ")
	if err := s.db.WithContext(ctx).Raw(q, args...).Scan(&rows).Error; err != nil {
		return nil, err
	}

	byID := make(map[int64]workListSourceRow, len(rows))
	for _, r := range rows {
		byID[r.ID] = workListSourceRow{
			ID: r.ID, MediumID: r.MediumID, DisplayName: r.DisplayName, OLang: r.OLang,
			ContentRating: r.ContentRating, Site: r.Site, ProductWorkID: r.ProductWorkID,
			ClaimState: r.ClaimState, UpdatedAt: r.UpdatedAt.UTC().Format(time.RFC3339),
		}
	}
	src := make([]workListSourceRow, 0, len(ids))
	for _, id := range ids {
		if row, ok := byID[id]; ok {
			src = append(src, row)
		}
	}
	return s.enrichWorkListItems(ctx, src, nsfw, inc)
}
