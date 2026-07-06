package search

import (
	"context"
	"math"
	"strings"

	"api/internal/infrastructure/search"
)

// EntityDoc is the Meilisearch document shared by all three entity indexes.
// A name lives in exactly ONE of the language-bucketed fields (invariant 1);
// fields that don't apply to a given entity are omitted.
type EntityDoc struct {
	ID           string   `json:"id"`          // prefixed: n{id} / c{id} / b{id}
	EntityType   string   `json:"entity_type"` // credit_name | character | label
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
	PersonID   *int64  `json:"person_id,omitempty"`
	Kind       *int16  `json:"kind,omitempty"` // label kind
	Popularity float64 `json:"popularity"`     // log-damped credit count
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
