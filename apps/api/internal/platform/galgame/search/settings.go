package search

import (
	"fmt"
	"log/slog"

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
			// intro_* are listed here so they're ALLOWED as search targets,
			// but at runtime service.go restricts attributesToSearchOn to the
			// non-intro subset unless include_intro=true is passed. Their
			// position (last) is a safety net — if we ever forget the filter,
			// they still rank lowest due to attribute precedence.
			"intro_zh_cn", "intro_ja_jp", "intro_en_us", "intro_zh_tw",
		},
		FilterableAttributes: []string{
			"status",
			"user_id",
			"content_limit",
			"age_limit",
			"original_language",
			"released_year",
			// released_month (1-12) backs the discontinuous-month filter:
			// `released_month IN [3, 7, 12]`, layered (AND) on the year
			// range. Indexer populates it alongside released_year.
			"released_month",
			// released_ts (Unix seconds) enables month-precision range
			// filters via the `released_from` / `released_to` query
			// params accepting YYYY-MM. Indexer already populates this
			// field; adding it here just registers it for filter use.
			// Backward-compat: year-only YYYY input still works (handler
			// converts to ts range covering the full year). released_year
			// stays filterable for callers that want exact-year match.
			"released_ts",
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

// WarnIfIndexesEmpty queries each index's stats and emits a slog.Warn if it
// has zero documents. Intended to be called once at startup right after
// EnsureIndexes — it tells the operator they probably need to run
// `cmd/reindex-search` (e.g. fresh Meilisearch instance with no `data.ms`).
//
// Failures querying stats are not fatal — they just don't emit a warning.
func WarnIfIndexesEmpty(client *search.Client) {
	for _, uid := range []string{IndexGalgames, IndexTags, IndexOfficials} {
		stats, err := client.Index(uid).GetStats()
		if err != nil {
			slog.Warn("get index stats failed; cannot check for empty index",
				"index", client.IndexUID(uid), "err", err)
			continue
		}
		if stats.NumberOfDocuments == 0 {
			slog.Warn(
				"⚠️  search index is empty — search endpoints will return 0 hits "+
					"until you run `go run ./cmd/reindex-search` to backfill from Postgres "+
					"(this is expected on a fresh Meilisearch instance with no data.ms)",
				"index", client.IndexUID(uid),
			)
		}
	}
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
