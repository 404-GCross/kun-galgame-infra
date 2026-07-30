// Package search projects the catalog entity layer (credit names, characters,
// labels) into Meilisearch, applying the doc-13 cross-media search config
// matrix. Read-side only: it never writes Gold. Consumers (the admin
// review-queue entity finder, letmoe's staff picker, future NextMoe
// aggregation) query these indexes. The WORKS index (wave 105) is the one
// exception to doc 13 §4.3 ("works indexes are born with each medium's
// product"): the cross-source registry works (incl. bodyless, which no
// product face indexes) get their own catalog_works index so the public
// catalog face can search by title; the wiki's galgames index remains the
// wiki product's own.
package search

import (
	"fmt"
	"time"

	"api/internal/infrastructure/search"

	"github.com/meilisearch/meilisearch-go"
)

// Index UIDs (before the configured prefix). The catalog_ prefix keeps them
// clear of the wiki's galgames/tags/officials indexes.
const (
	IndexCreditNames = "catalog_credit_names"
	IndexCharacters  = "catalog_characters"
	IndexLabels      = "catalog_labels"
	IndexWorks       = "catalog_works"
	// IndexTags is the canonical cross-source tag vocabulary (A2-1d, settling
	// the A2-1b account). Same shape as the labels index; the extra `tier`
	// rides along so a consumer can narrow to core tags.
	IndexTags = "catalog_tags"
)

// localizedAttributes pins the CJK language per field pattern (doc 13 invariant
// 1): *_ja → Japanese, *_zh → Chinese, bypassing whatlang autodetection. A
// single field NEVER mixes zh/ja — the projection buckets by the row's lang.
// Latin/other fields are not declared (Meili's default pipeline suffices).
//
// Query-time `locales` discipline (invariant 2): a consumer's search endpoint
// must set locales SERVER-SIDE from the site/input, NEVER pass a client-
// supplied value through — that would silently override these index settings.
// (This step ships no query surface; the rule lives here for the consumers.)
func localizedAttributes() []*meilisearch.LocalizedAttributes {
	return []*meilisearch.LocalizedAttributes{
		{AttributePatterns: []string{"*_ja"}, Locales: []string{"jpn"}},
		{AttributePatterns: []string{"*_zh"}, Locales: []string{"cmn"}},
	}
}

// cjkTypoDisabled are the fields where edit-distance typo tolerance is
// meaningless (a wrong kanji is a different character, not an insertion) — doc
// 13 invariant 3. Latin fields keep the 4/8 default.
func cjkTypoDisabled(extra ...string) []string {
	return append([]string{"name_ja", "name_zh", "name_other", "aliases_ja", "aliases_zh", "aliases_other"}, extra...)
}

// entityRankingRules keeps popularity as the LAST, tiebreaker rule so it can
// never outrank an exact textual match (doc 13 invariant 7): words/typo/
// proximity/attribute/sort/exactness all decide first, popularity only breaks
// same-score ties.
var entityRankingRules = []string{
	"words", "typo", "proximity", "attribute", "sort", "exactness", "popularity:desc",
}

func creditNamesSettings() *meilisearch.Settings {
	return &meilisearch.Settings{
		SearchableAttributes: []string{"name_zh", "name_ja", "name_other", "aliases_zh", "aliases_ja", "aliases_other", "latin"},
		FilterableAttributes: []string{"entity_type", "source_keys", "person_id"},
		SortableAttributes:   []string{"popularity"},
		RankingRules:         entityRankingRules,
		LocalizedAttributes:  localizedAttributes(),
		TypoTolerance: &meilisearch.TypoTolerance{
			Enabled:             true,
			DisableOnAttributes: cjkTypoDisabled(),
		},
	}
}

func charactersSettings() *meilisearch.Settings {
	return &meilisearch.Settings{
		SearchableAttributes: []string{"name_zh", "name_ja", "name_other", "latin"},
		FilterableAttributes: []string{"entity_type", "source_keys"},
		SortableAttributes:   []string{"popularity"},
		RankingRules:         entityRankingRules,
		LocalizedAttributes:  localizedAttributes(),
		TypoTolerance:        &meilisearch.TypoTolerance{Enabled: true, DisableOnAttributes: cjkTypoDisabled()},
	}
}

func labelsSettings() *meilisearch.Settings {
	return &meilisearch.Settings{
		SearchableAttributes: []string{"name_zh", "name_ja", "name_other", "latin"},
		FilterableAttributes: []string{"entity_type", "source_keys", "kind"},
		SortableAttributes:   []string{"popularity"},
		RankingRules:         entityRankingRules,
		LocalizedAttributes:  localizedAttributes(),
		TypoTolerance:        &meilisearch.TypoTolerance{Enabled: true, DisableOnAttributes: cjkTypoDisabled()},
	}
}

// WorksFilterableAttributes is the works index's filterable set, exported
// because it IS the contract of the product-search face: every filter and every
// facet token the public face accepts must appear here (A2-1d).
//
// `id` is filterable so the `v\d+` query short-circuit can pin one document by
// its primary key without a second round trip; the numeric scalars are
// filterable for the released_* bounds and sortable for the sort lanes.
var WorksFilterableAttributes = []string{
	"id", "entity_type", "source_keys", "content_rating", "content_limit",
	"claimed", "claim_state", "olang", "tag_ids", "label_ids", "engine_ids", "series_ids",
	"released_ord",
}

// WorksSortableAttributes backs the five product-search sort lanes: relevance
// (no sort at all), released_desc / released_asc, updated and popularity.
var WorksSortableAttributes = []string{"popularity", "released_ord", "updated_ts"}

// WorksTitleSearchable is the works index's TITLE family — the attribute set
// the product search restricts itself to unless the caller opts into synopsis
// matching (A2-1f).
//
// It is exported and used as a per-request `attributesToSearchOn` rather than
// being the index's whole searchable list, because the index must CONTAIN the
// intro fields for search_intro=1 to have anything to match. That makes the
// restriction load-bearing: with it absent, growing the searchable list would
// silently start matching synopses for every existing caller — the byte-freeze
// break this wave exists to avoid.
var WorksTitleSearchable = []string{
	"name_zh", "name_ja", "name_other",
	"aliases_zh", "aliases_ja", "aliases_other", "latin",
}

// worksIntroSearchable is the synopsis family, appended AFTER the title family
// in the index's searchable list. Order is semantics here: Meilisearch's
// `attribute` ranking rule ranks earlier attributes higher, so a title match can
// never lose to a synopsis match no matter how the two documents score
// elsewhere.
var worksIntroSearchable = []string{"intro_zh", "intro_ja", "intro_other"}

// worksSearchable is the full declared list (titles then intros).
func worksSearchable() []string {
	return append(append([]string{}, WorksTitleSearchable...), worksIntroSearchable...)
}

// worksMaxTotalHits raises Meilisearch's pagination ceiling above the whole
// galgame registry population (~226k live works and growing).
//
// This is load-bearing, not tuning. The default is 1000, and it caps
// `totalHits` as well as pagination depth — so the product search's `total`
// would report 1000 for every result set larger than that, breaking the wave's
// central promise that total is the size of the very set the caller can page
// through. Measured on the pinned v1.45 image against a 226k-document stand-in
// with this exact settings block (2026-07-29): filtered total exact
// (150,749 of 226,000), first page ~1 ms, page 5000 ~48 ms, four-facet
// distribution ~2 ms — the ceiling costs nothing at this population.
const worksMaxTotalHits int64 = 500_000

func worksSettings() *meilisearch.Settings {
	return &meilisearch.Settings{
		SearchableAttributes: worksSearchable(),
		FilterableAttributes: WorksFilterableAttributes,
		SortableAttributes:   WorksSortableAttributes,
		RankingRules:         entityRankingRules,
		LocalizedAttributes:  localizedAttributes(),
		// The intro fields join the CJK typo-disabled set for the same reason
		// the name fields are in it: a wrong kanji is a different character, not
		// an edit-distance neighbour — and over a 2,000-rune body, typo
		// tolerance is pure false-positive surface.
		TypoTolerance: &meilisearch.TypoTolerance{
			Enabled: true, DisableOnAttributes: cjkTypoDisabled(worksIntroSearchable...),
		},
		Pagination: &meilisearch.Pagination{MaxTotalHits: worksMaxTotalHits},
	}
}

func tagsSettings() *meilisearch.Settings {
	return &meilisearch.Settings{
		SearchableAttributes: []string{"name_zh", "name_ja", "name_other", "latin"},
		FilterableAttributes: []string{"entity_type", "source_keys", "kind", "tier"},
		SortableAttributes:   []string{"popularity"},
		RankingRules:         entityRankingRules,
		LocalizedAttributes:  localizedAttributes(),
		TypoTolerance:        &meilisearch.TypoTolerance{Enabled: true, DisableOnAttributes: cjkTypoDisabled()},
	}
}

func indexSpecs() []struct {
	uid      string
	settings *meilisearch.Settings
} {
	return []struct {
		uid      string
		settings *meilisearch.Settings
	}{
		{IndexCreditNames, creditNamesSettings()},
		{IndexCharacters, charactersSettings()},
		{IndexLabels, labelsSettings()},
		{IndexWorks, worksSettings()},
		{IndexTags, tagsSettings()},
	}
}

// EnsureIndexes creates the five catalog indexes (if missing) and PATCHes their
// settings to the doc-13 matrix. Idempotent; pushes no documents.
//
// This is the ONLY carrier of an index-settings change: cmd/reindex-catalog
// calls it unconditionally before it touches any lane, so re-running the
// reindex is what brings a deployed Meilisearch to the declared terminal state.
// There is deliberately no manual "go PATCH these settings" step to forget.
func EnsureIndexes(client *search.Client) error {
	for _, spec := range indexSpecs() {
		fullUID := client.IndexUID(spec.uid)
		// CreateIndex + UpdateSettings are async tasks; wait for each so the
		// index is queryable when this returns (a read like GetSettings does
		// not queue behind pending tasks).
		if task, err := client.Svc().CreateIndex(&meilisearch.IndexConfig{Uid: fullUID, PrimaryKey: "id"}); err != nil {
			if !isAlreadyExists(err) {
				return fmt.Errorf("create index %s: %w", fullUID, err)
			}
		} else if _, err := client.Svc().WaitForTask(task.TaskUID, 50*time.Millisecond); err != nil {
			return fmt.Errorf("wait create %s: %w", fullUID, err)
		}
		task, err := client.Index(spec.uid).UpdateSettings(spec.settings)
		if err != nil {
			return fmt.Errorf("update settings %s: %w", fullUID, err)
		}
		if _, err := client.Svc().WaitForTask(task.TaskUID, 50*time.Millisecond); err != nil {
			return fmt.Errorf("wait settings %s: %w", fullUID, err)
		}
	}
	return nil
}

func isAlreadyExists(err error) bool {
	if msErr, ok := err.(*meilisearch.Error); ok {
		return msErr.StatusCode == 409 || msErr.MeilisearchApiError.Code == "index_already_exists"
	}
	return false
}
