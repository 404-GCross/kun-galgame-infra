package vndbintros

import (
	"context"
	"fmt"
	"os"
	"testing"

	"api/internal/platform/galgame/model"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// Integration tests against a real Postgres (the wiki tables self-migrate, as in
// CI's shared job database). Skipped when no test DB is reachable.
var testDB *gorm.DB

func TestMain(m *testing.M) {
	dsn := os.Getenv("TEST_DATABASE_DSN")
	if dsn == "" {
		dsn = "host=localhost port=5432 user=postgres password=postgres dbname=kun_galgame_wiki_test sslmode=disable"
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "SKIP: cannot connect to test database: %v\n", err)
		os.Exit(0)
	}
	if err := db.AutoMigrate(&model.GalgameSeries{}, &model.Galgame{}, &model.GalgameRevision{}); err != nil {
		fmt.Fprintf(os.Stderr, "SKIP: wiki automigrate failed: %v\n", err)
		os.Exit(0)
	}
	testDB = db
	os.Exit(m.Run())
}

func clean(t *testing.T) {
	t.Helper()
	for _, table := range []string{"galgame_revision", "galgame"} {
		require.NoError(t, testDB.Exec("TRUNCATE "+table+" RESTART IDENTITY CASCADE").Error)
	}
}

// seedGalgame inserts a galgame with a given vndb_id + intro_en_us plus a
// latest revision whose snapshot carries the same intro, so the Approach-B patch
// has a target to keep in sync.
func seedGalgame(t *testing.T, id int, vndbID, introEnUS string) {
	t.Helper()
	require.NoError(t, testDB.Create(&model.Galgame{
		ID: id, VNDBID: vndbID, IntroEnUS: introEnUS, UserID: 1,
	}).Error)
	require.NoError(t, testDB.Exec(`
		INSERT INTO galgame_revision (galgame_id, revision, action, user_id, snapshot, changed_fields, is_minor)
		VALUES (?, 1, 'created', 1, ?::jsonb, '[]'::jsonb, false)
	`, id, fmt.Sprintf(`{"intro_en_us": %q}`, introEnUS)).Error)
}

// fakeFetch models VNDB: ids present in the backing map are returned (incl. an
// existing VN whose description is ""), ids ABSENT are simply not returned (the
// api_missing case).
func fakeFetch(descs map[string]string) Fetcher {
	return func(_ context.Context, ids []string) (map[string]string, error) {
		out := map[string]string{}
		for _, id := range ids {
			if v, ok := descs[id]; ok {
				out[id] = v
			}
		}
		return out, nil
	}
}

func TestEnrichScenarios(t *testing.T) {
	if testDB == nil {
		t.Skip("no test DB")
	}
	clean(t)

	// #1 empty intro + plain description → filled verbatim.
	seedGalgame(t, 1, "v1", "")
	// #2 NON-EMPTY intro (user content) → excluded from candidates entirely;
	//    even though VNDB has a description for v2, it must never be touched.
	seedGalgame(t, 2, "v2", "用户已填的英文简介")
	// #3 empty intro, but VNDB doesn't return v3 → api_missing.
	seedGalgame(t, 3, "v3", "")
	// #4 empty intro, VNDB returns v4 with a null/empty description → no_description.
	seedGalgame(t, 4, "v4", "")
	// #5 empty intro, description is a bare attribution line → normalizes to
	//    empty → skipped_empty_after_norm (nothing written).
	seedGalgame(t, 5, "v5", "")
	// #6 empty intro, BBCode description with an inline trailer → normalized to
	//    clean Markdown (intronorm wiring).
	seedGalgame(t, 6, "v6", "")

	descs := map[string]string{
		"v1": "Plain synopsis.",
		"v2": "SHOULD NEVER BE WRITTEN — v2 has user content",
		"v4": "",
		"v5": "[From [url=https://example.com]shop[/url]]",
		"v6": "A [b]bold[/b] story. [From [url=https://example.com]src[/url]]",
		// v3 deliberately absent → api_missing.
	}

	stats, err := Run(context.Background(), testDB, fakeFetch(descs), Options{})
	require.NoError(t, err)

	// Self-proving accounting.
	assert.Equal(t, 5, stats.Candidates, "empty-intro vndb galgames (#2 excluded)")
	assert.Equal(t, 4, stats.Fetched)
	assert.Equal(t, 2, stats.Filled)
	assert.Equal(t, 1, stats.NoDescription)
	assert.Equal(t, 1, stats.SkippedEmptyAfterNorm)
	assert.Equal(t, 1, stats.APIMissing)
	assert.Equal(t, 0, stats.Failed)
	// Candidates == Filled + NoDescription + SkippedEmptyAfterNorm + APIMissing + Failed.
	assert.Equal(t, stats.Candidates,
		stats.Filled+stats.NoDescription+stats.SkippedEmptyAfterNorm+stats.APIMissing+stats.Failed)

	// #1 filled verbatim (already clean → intronorm returns it unchanged).
	assert.Equal(t, "Plain synopsis.", liveIntro(t, 1))
	// #6 normalized: BBCode stripped, inline attribution trailer removed.
	assert.Equal(t, "A bold story.", liveIntro(t, 6))

	// THE GUARD: #2's user content is untouched.
	assert.Equal(t, "用户已填的英文简介", liveIntro(t, 2))
	// Nothing written for the non-fill dispositions.
	assert.Equal(t, "", liveIntro(t, 3), "api_missing → no write")
	assert.Equal(t, "", liveIntro(t, 4), "no_description → no write")
	assert.Equal(t, "", liveIntro(t, 5), "skipped_empty_after_norm → no write")

	// Approach B: the latest revision snapshot matches the live column for #1.
	var snap string
	require.NoError(t, testDB.Raw(
		`SELECT snapshot->>'intro_en_us' FROM galgame_revision WHERE galgame_id = 1 AND revision = 1`).Scan(&snap).Error)
	assert.Equal(t, "Plain synopsis.", snap)

	// Idempotency: a second run fills nothing (the filled rows are no longer
	// candidates; only v3/v4/v5 remain, all non-fill).
	stats2, err := Run(context.Background(), testDB, fakeFetch(descs), Options{})
	require.NoError(t, err)
	assert.Equal(t, 3, stats2.Candidates)
	assert.Zero(t, stats2.Filled)
	assert.Equal(t, 1, stats2.NoDescription)
	assert.Equal(t, 1, stats2.SkippedEmptyAfterNorm)
	assert.Equal(t, 1, stats2.APIMissing)

	// Dry run reports the would-be fill without writing.
	require.NoError(t, testDB.Exec(`UPDATE galgame SET intro_en_us = '' WHERE id = 1`).Error)
	dry, err := Run(context.Background(), testDB, fakeFetch(descs), Options{DryRun: true})
	require.NoError(t, err)
	assert.Equal(t, 1, dry.Filled)
	assert.Equal(t, "", liveIntro(t, 1), "dry run must not write")
}

func liveIntro(t *testing.T, id int) string {
	t.Helper()
	var s string
	require.NoError(t, testDB.Raw(`SELECT intro_en_us FROM galgame WHERE id = ?`, id).Scan(&s).Error)
	return s
}
