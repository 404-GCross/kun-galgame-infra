package search

import (
	"fmt"

	"api/internal/infrastructure/search"

	"github.com/meilisearch/meilisearch-go"
)

// galgamesSettings returns the desired settings for the galgames index.
// Kept as a function (not a var) so we can vary based on runtime config later.
func galgamesSettings() *meilisearch.Settings {
	return &meilisearch.Settings{
		SearchableAttributes: []string{
			// Order matters — earlier attributes rank higher.
			"vndb_id",
			"name_zh_cn",
			"name_ja_jp",
			"name_en_us",
			"name_zh_tw",
			"aliases",
			"tag_names",
			"official_names",
			// intro_* intentionally omitted from defaults; surfaced via
			// attributesToSearchOn when ?include_intro=true.
		},
		FilterableAttributes: []string{
			"status",
			"content_limit",
			"age_limit",
			"original_language",
			"released_year",
			"tag_ids",
			"official_ids",
			"engine_ids",
			"series_id",
		},
		SortableAttributes: []string{
			"released_ts",
			"view",
			"updated_ts",
			"created_ts",
		},
		RankingRules: []string{
			"words",
			"typo",
			"proximity",
			"attribute",
			"sort",
			"exactness",
			"view:desc", // same-score tiebreaker — popular first
		},
		TypoTolerance: &meilisearch.TypoTolerance{
			Enabled: true,
			MinWordSizeForTypos: meilisearch.MinWordSizeForTypos{
				OneTypo:  4,
				TwoTypos: 8,
			},
			DisableOnAttributes: []string{"vndb_id"},
		},
		Faceting: &meilisearch.Faceting{
			MaxValuesPerFacet: 100,
		},
	}
}

func tagsSettings() *meilisearch.Settings {
	return &meilisearch.Settings{
		SearchableAttributes: []string{"name", "aliases"},
		FilterableAttributes: []string{"category"},
		SortableAttributes:   []string{"galgame_count"},
		RankingRules: []string{
			"words", "typo", "proximity", "attribute", "sort", "exactness",
			"galgame_count:desc",
		},
	}
}

func officialsSettings() *meilisearch.Settings {
	return &meilisearch.Settings{
		SearchableAttributes: []string{"name", "original", "aliases"},
		FilterableAttributes: []string{"category", "lang"},
		SortableAttributes:   []string{"galgame_count"},
		RankingRules: []string{
			"words", "typo", "proximity", "attribute", "sort", "exactness",
			"galgame_count:desc",
		},
	}
}

// EnsureIndexes makes sure all three indexes exist with the correct settings.
// Idempotent — safe to call on every startup. Does NOT push documents.
func EnsureIndexes(client *search.Client) error {
	type indexSpec struct {
		uid      string
		primary  string
		settings *meilisearch.Settings
	}

	specs := []indexSpec{
		{IndexGalgames, "id", galgamesSettings()},
		{IndexTags, "id", tagsSettings()},
		{IndexOfficials, "id", officialsSettings()},
	}

	for _, spec := range specs {
		fullUID := client.IndexUID(spec.uid)

		// Create index if missing. Meilisearch returns 409/index_already_exists
		// if it already exists — we treat both as success.
		_, err := client.Svc().CreateIndex(&meilisearch.IndexConfig{
			Uid:        fullUID,
			PrimaryKey: spec.primary,
		})
		if err != nil {
			// SDK wraps API errors; check if it's "already exists".
			if !isAlreadyExists(err) {
				return fmt.Errorf("create index %s: %w", fullUID, err)
			}
		}

		// Always PATCH settings — Meilisearch diffs internally and
		// only re-indexes what's needed.
		if _, err := client.Index(spec.uid).UpdateSettings(spec.settings); err != nil {
			return fmt.Errorf("update settings %s: %w", fullUID, err)
		}
	}

	return nil
}

// isAlreadyExists checks whether an error represents a duplicate-index attempt.
func isAlreadyExists(err error) bool {
	if err == nil {
		return false
	}
	if msErr, ok := err.(*meilisearch.Error); ok {
		return msErr.StatusCode == 409 ||
			msErr.MeilisearchApiError.Code == "index_already_exists"
	}
	return false
}
