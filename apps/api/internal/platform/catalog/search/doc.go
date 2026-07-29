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

	// ── A2-1f: the works intro text ─────────────────────────────────────────
	//
	// Set on WORKS docs only. Language-bucketed like the name fields for the
	// same reason (invariant 1 + the *_ja / *_zh localizedAttributes patterns):
	// a Japanese synopsis must tokenize under jpn and a Chinese one under cmn,
	// and one field may never mix the two.
	//
	// They sit LAST in the searchable list, so the `attribute` ranking rule can
	// never let a synopsis match outrank a title match — and they are searched
	// only when the caller passes search_intro=1 (the face restricts
	// attributesToSearchOn to the title family otherwise, which is what keeps
	// the default result set byte-identical to A2-1d's).
	//
	// Like every other field here they never reach the wire: hits are
	// re-hydrated from Postgres into the works-list item shape (裁定 4).
	IntroJa    string `json:"intro_ja,omitempty"`
	IntroZh    string `json:"intro_zh,omitempty"`
	IntroOther string `json:"intro_other,omitempty"`
}

// SetIntro appends a synopsis to its language bucket (invariant 1). Several
// source languages can share a bucket (zh-Hans and zh-Hant both land in zh);
// they are newline-joined rather than first-wins, because this field exists for
// RECALL — a phrase a user remembers may sit in either variant.
func (d *EntityDoc) SetIntro(lang, text string) {
	if text = strings.TrimSpace(text); text == "" {
		return
	}
	switch bucket(lang) {
	case "zh":
		d.IntroZh = appendIntro(d.IntroZh, text)
	case "ja":
		d.IntroJa = appendIntro(d.IntroJa, text)
	default:
		d.IntroOther = appendIntro(d.IntroOther, text)
	}
}

func appendIntro(cur, add string) string {
	if cur == "" {
		return add
	}
	return cur + "\n" + add
}

// TruncateIntros caps every intro bucket at IntroMaxRunes, on a RUNE boundary
// (a byte cut would produce invalid UTF-8 and Meilisearch would reject or
// mis-tokenize the document).
func (d *EntityDoc) TruncateIntros() {
	d.IntroJa = truncateRunes(d.IntroJa, IntroMaxRunes)
	d.IntroZh = truncateRunes(d.IntroZh, IntroMaxRunes)
	d.IntroOther = truncateRunes(d.IntroOther, IntroMaxRunes)
}

// IntroMaxRunes caps the indexed synopsis per language bucket.
//
// Chosen, not tuned: a wiki intro is 1-10 KB of markdown, and the whole
// searchable point of it is that a user recalls a PHRASE from the setup — which
// lives in the opening paragraphs, not in a route-by-route walkthrough at the
// end. 2,000 runes covers several paragraphs in CJK (where a rune is a word's
// worth of meaning) while bounding the index at roughly a few MB per language
// across the ~226k-work population, instead of the tens of MB an uncapped field
// would add. Truncation costs recall only for phrases deep inside a long
// synopsis; an uncapped field would cost it for everyone, via index size.
const IntroMaxRunes = 2000

func truncateRunes(s string, max int) string {
	if s == "" {
		return s
	}
	n := 0
	for i := range s {
		if n == max {
			return s[:i]
		}
		n++
	}
	return s
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
