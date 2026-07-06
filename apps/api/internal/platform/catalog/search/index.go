// Package search projects the catalog entity layer (credit names, characters,
// labels) into Meilisearch, applying the doc-13 cross-media search config
// matrix. Read-side only: it never writes Gold. Consumers (the admin
// review-queue entity finder, letmoe's staff picker, future NextMoe
// aggregation) query these indexes; works indexes are born with each medium's
// product (doc 13 §4.3), so they are deliberately NOT built here.
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
	}
}

// EnsureIndexes creates the three entity indexes (if missing) and PATCHes their
// settings to the doc-13 matrix. Idempotent; pushes no documents.
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
