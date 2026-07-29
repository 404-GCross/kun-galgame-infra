package search

import (
	"context"
	"math"
	"strings"

	"api/internal/infrastructure/search"
)

// EntityDoc is the Meilisearch document shared by every catalog index.
// A name lives in exactly ONE of the language-bucketed fields (invariant 1);
// fields that don't apply to a given entity are omitted.
type EntityDoc struct {
	ID           string   `json:"id"`          // prefixed: n{id} / c{id} / b{id} / w{id} / t{id}
	EntityType   string   `json:"entity_type"` // credit_name | character | label | work | tag
	NameJa       string   `json:"name_ja,omitempty"`
	NameZh       string   `json:"name_zh,omitempty"`
	NameOther    string   `json:"name_other,omitempty"`
	Latin        string   `json:"latin,omitempty"`
	AliasesJa    []string `json:"aliases_ja,omitempty"`
	AliasesZh    []string `json:"aliases_zh,omitempty"`
	AliasesOther []string `json:"aliases_other,omitempty"`
	// Sources is the anchor list "sourceKey:externalID"; SourceKeys is the
	// distinct source keys (filterable — "which source knows this entity").
	Sources    []string `json:"sources"`
	SourceKeys []string `json:"source_keys"`
	// PersonID: filterable so the review UI can find orphan names (person_id
	// absent). link_visibility is intentionally NOT indexed — names have no
	// person link yet, and a hidden link's aggregation filtering happens when a
	// person page is assembled, not on the name index.
	PersonID *int64 `json:"person_id,omitempty"`
	Kind     *int16 `json:"kind,omitempty"` // label kind
	// ContentRating is set on WORKS docs only (wave 105): 0=all_ages
	// 1=sensitive 2=r18 — filterable so the public face's nsfw switch can
	// exclude r18 hits server-side. Absent on entity docs.
	ContentRating *int16  `json:"content_rating,omitempty"`
	Popularity    float64 `json:"popularity"` // log-damped credit count (works: log-damped collect/download count)

	// ── A2-1d: the works product-search axes ────────────────────────────────
	//
	// Every field below is set on WORKS docs only. They exist so the product
	// search face (GET /v1/catalog/works/search) can push its WHOLE filter set
	// into Meilisearch — which is what makes `total`, the facet distribution
	// and `items` share one gate. Filtering any of these during the DB
	// re-hydration instead would resurrect the deprecated face's content_limit
	// trap (an unfiltered total beside filtered items).
	//
	// None of them ever reaches the wire: the hits are re-hydrated from
	// Postgres into the works-list item shape (裁定 4).

	// Claimed mirrors the works list's `claimed` filter (a product site owns
	// this registry row). A pointer so an explicit false is still indexed —
	// omitempty on a non-pointer bool would erase the bodyless half.
	Claimed *bool `json:"claimed,omitempty"`
	// OLang is the registry's original-language tag in the UPSTREAM BCP-47
	// spelling (ja / zh-Hans / en …), never the product locale form — the same
	// value the calendar's olang gate matches on.
	OLang string `json:"olang,omitempty"`
	// TagIDs are CANONICAL tag ids (catalog_work_tag resolved through
	// catalog_tag_source_map), so the index speaks the same id space the
	// public tag_id filter does. Label / engine / series ids are the edge
	// tables' ids verbatim.
	TagIDs    []int64 `json:"tag_ids,omitempty"`
	LabelIDs  []int64 `json:"label_ids,omitempty"`
	EngineIDs []int64 `json:"engine_ids,omitempty"`
	SeriesIDs []int64 `json:"series_ids,omitempty"`
	// ReleasedOrd is the COMPOSED DATE ORDINAL (y*10000 + m*100 + d, unknown
	// month/day = 0) of the work's earliest year-carrying release — NOT a Unix
	// timestamp, despite the released_ts spelling the deprecated face used.
	// The ordinal is what makes released_after / released_before mean here
	// exactly what they mean on the works list: both sides compare the same
	// number, so a month-precision work (2024-06 → 20240600) sorts and filters
	// identically on both faces. A Unix timestamp would have to invent a day
	// for it and would then disagree at the month boundary.
	//
	// Absent (not zero) when the work has no dated release, so Meilisearch
	// places it LAST in both sort directions and no released_* bound matches
	// it — the works list's `NULL >= bound` behaviour, reproduced.
	ReleasedOrd int64 `json:"released_ord,omitempty"`
	// UpdatedTS is catalog_work.updated_at in Unix seconds (genuinely a
	// timestamp) — the sort=updated lane.
	UpdatedTS int64 `json:"updated_ts,omitempty"`
	// Tier is set on TAG docs only (A2-1d, the A2-1b account): the canonical
	// tag's display tier, alongside the shared Kind.
	Tier *int16 `json:"tier,omitempty"`
}

// SetName routes a name into its language bucket by the row's lang (invariant
// 1: never mix zh/ja in one field).
func (d *EntityDoc) SetName(lang, name string) {
	switch bucket(lang) {
	case "zh":
		d.NameZh = name
	case "ja":
		d.NameJa = name
	default:
		d.NameOther = name
	}
}

// SetNameOrAlias fills the name bucket for the row's lang if still empty,
// otherwise appends to the bucket's aliases (works docs: display_name wins
// its bucket, official titles win theirs, everything else is an alias).
func (d *EntityDoc) SetNameOrAlias(lang, name string) {
	switch bucket(lang) {
	case "zh":
		if d.NameZh == "" {
			d.NameZh = name
			return
		}
	case "ja":
		if d.NameJa == "" {
			d.NameJa = name
			return
		}
	default:
		if d.NameOther == "" {
			d.NameOther = name
			return
		}
	}
	d.AddAlias(lang, name)
}

// AddAlias routes an alias into its language bucket.
func (d *EntityDoc) AddAlias(lang, name string) {
	switch bucket(lang) {
	case "zh":
		d.AliasesZh = append(d.AliasesZh, name)
	case "ja":
		d.AliasesJa = append(d.AliasesJa, name)
	default:
		d.AliasesOther = append(d.AliasesOther, name)
	}
}

func bucket(lang string) string {
	switch {
	case strings.HasPrefix(lang, "zh"):
		return "zh"
	case strings.HasPrefix(lang, "ja"), lang == "":
		return "ja" // catalog imports default to the source (Japanese) language
	default:
		return "other"
	}
}

// Popularity dampens a raw credit count logarithmically so a prolific creator
// nudges, never dominates, ranking (doc 13: 热度 = credit 数 log 阻尼).
func Popularity(creditCount int) float64 { return math.Log1p(float64(creditCount)) }

// Indexer pushes entity documents.
type Indexer struct{ client *search.Client }

func NewIndexer(client *search.Client) *Indexer { return &Indexer{client: client} }

// UpsertBatch pushes documents to one index in a single API call.
func (i *Indexer) UpsertBatch(ctx context.Context, uid string, docs []EntityDoc) error {
	if len(docs) == 0 {
		return nil
	}
	_, err := i.client.Index(uid).AddDocumentsWithContext(ctx, docs, nil)
	return err
}

// Count returns the current document count of an index.
func (i *Indexer) Count(uid string) (int64, error) {
	stats, err := i.client.Index(uid).GetStats()
	if err != nil {
		return 0, err
	}
	return stats.NumberOfDocuments, nil
}
