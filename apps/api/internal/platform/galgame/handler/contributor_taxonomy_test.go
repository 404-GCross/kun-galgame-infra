// contributor_taxonomy_test.go — A2-1g: the submission form's taxonomy picker
// on /internal.
//
// The property that matters is SAMENESS: this door and the staff door must be
// the same query behind a different gate, because one frontend picker component
// renders both. So the central case does not assert a hand-written expectation
// — it asserts that the two doors return byte-identical payloads for the same
// query. A future edit that "improves" one of them fails here.
package handler

import (
	"encoding/json"
	"fmt"
	"testing"

	"api/internal/platform/galgame/model"
	"api/internal/platform/galgame/service"

	"github.com/gofiber/fiber/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// contribApp mounts the contributor picker with the same stub auth layer
// staffApp uses; userID 0 stands for an anonymous caller.
func contribApp(db *gorm.DB, userID uint, roles []string) *fiber.App {
	h := NewContributorTaxonomyHandler(taxPickerFor(db))
	app := fiber.New()
	app.Use(func(c fiber.Ctx) error {
		if userID != 0 {
			c.Locals("user_id", userID)
			c.Locals("user_roles", roles)
		}
		return c.Next()
	})
	app.Get("/internal/galgame/taxonomy/:family/search", h.Search)
	return app
}

// TestContributorTaxonomySearchMatchesTheStaffDoor is the wave's core case:
// every family answers, and the payload is IDENTICAL to the staff door's for
// the same query — one query, two gates.
func TestContributorTaxonomySearchMatchesTheStaffDoor(t *testing.T) {
	db := openStaffTestDB(t)
	seedStaffTaxonomy(t, db)

	// A plain contributor: signed in, NO taxonomy.edit_any.
	contrib := contribApp(db, 42, []string{"user"})
	staff := staffApp(db, 7, []string{"admin"})

	for _, tc := range []struct{ family, q, staffURL string }{
		{"tag", "純愛", "/api/tag/search"},
		{"official", "みるく", "/api/official/search"},
		{"engine", "KiriKiri", "/api/engine/search"},
		{"series", "a2-1e", "/api/series/search"},
		// The unfiltered small-facet behaviour must match too.
		{"engine", "", "/api/engine/search"},
		{"series", "", "/api/series/search"},
	} {
		t.Run(tc.family+"/"+tc.q, func(t *testing.T) {
			cCode, cBody := staffGet(t, contrib,
				fmt.Sprintf("/internal/galgame/taxonomy/%s/search?q=%s", tc.family, tc.q))
			require.Equal(t, 200, cCode, "a signed-in contributor must be served")

			sCode, sBody := staffGet(t, staff, tc.staffURL+"?q="+tc.q)
			require.Equal(t, 200, sCode)

			cJSON, err := json.Marshal(cBody["data"])
			require.NoError(t, err)
			sJSON, err := json.Marshal(sBody["data"])
			require.NoError(t, err)
			assert.JSONEq(t, string(sJSON), string(cJSON),
				"the two doors must return the same payload — one query, two gates")
		})
	}

	// And the rows really are the {id,name} picker shape.
	_, body := staffGet(t, contrib, "/internal/galgame/taxonomy/tag/search?q=純愛")
	items := body["data"].(map[string]any)["items"].([]any)
	require.Len(t, items, 1)
	assert.ElementsMatch(t, []string{"id", "name"}, mapKeys(items[0].(map[string]any)))
}

// TestContributorTaxonomySearchGate pins the three auth states — and
// specifically that edit_any is NOT required, which is the entire reason this
// door exists (the staff door 403s the very contributors who need the picker).
func TestContributorTaxonomySearchGate(t *testing.T) {
	db := openStaffTestDB(t)
	seedStaffTaxonomy(t, db)
	const url = "/internal/galgame/taxonomy/tag/search?q=純愛"

	code, _ := staffGet(t, contribApp(db, 0, nil), url)
	assert.Equal(t, 401, code, "anonymous → 401")

	code, body := staffGet(t, contribApp(db, 42, []string{"user"}), url)
	assert.Equal(t, 200, code, "a signed-in contributor with NO edit_any must be served")
	assert.NotEmpty(t, body["data"].(map[string]any)["items"])

	code, _ = staffGet(t, contribApp(db, 7, []string{"admin"}), url)
	assert.Equal(t, 200, code, "an editor is served by the same door too")

	// Cross-check the contrast this wave is about: the SAME caller is refused
	// by the staff door.
	code, _ = staffGet(t, staffApp(db, 42, []string{"user"}), "/api/tag/search?q=純愛")
	assert.Equal(t, 403, code, "the staff door still requires edit_any — that is why /internal exists")
}

// TestContributorTaxonomySearchRejectsUnknownFamily: family is OUR closed
// vocabulary, so a typo is a caller mistake worth failing loudly — an empty
// list would read as "no matches" and send a developer hunting for data bugs.
func TestContributorTaxonomySearchRejectsUnknownFamily(t *testing.T) {
	db := openStaffTestDB(t)
	app := contribApp(db, 42, []string{"user"})

	for _, family := range []string{"tags", "label", "Tag", "nonsense"} {
		code, body := staffGet(t, app, "/internal/galgame/taxonomy/"+family+"/search?q=x")
		assert.Equal(t, 400, code, family)
		assert.Equal(t, "family must be one of tag, official, engine, series", body["message"], family)
	}
}

// TestContributorTaxonomyBlankQueryPerFamily documents the deliberate
// asymmetry: the open-ended vocabularies want a query, the small curated ones
// hydrate as flat lists.
func TestContributorTaxonomyBlankQueryPerFamily(t *testing.T) {
	db := openStaffTestDB(t)
	seedStaffTaxonomy(t, db)
	app := contribApp(db, 42, []string{"user"})

	for _, tc := range []struct {
		family string
		empty  bool
	}{
		{"tag", true}, {"official", true}, // 3k / 24k rows: type something
		{"engine", false}, {"series", false}, // small curated sets: serve them
	} {
		code, body := staffGet(t, app, "/internal/galgame/taxonomy/"+tc.family+"/search?q=%20")
		require.Equal(t, 200, code, tc.family)
		items := body["data"].(map[string]any)["items"].([]any)
		if tc.empty {
			assert.Empty(t, items, "%s: a blank query must not dump an open-ended vocabulary", tc.family)
		} else {
			assert.NotEmpty(t, items, "%s: a small curated facet is served unfiltered", tc.family)
		}
	}
}

// TestContributorTaxonomyBlankQueryEnumeratesWholeFamily pins the FLAT-LIST
// promise for the curated families past the search cap: "serve them unfiltered"
// means the whole family, not its first TaxonomyPickerLimit rows. Engine really
// runs 189 rows in production, so a 50-row cap on this path would drop ~3/4 of
// the picker and read as "that engine is missing from the wiki".
//
// The counterpart assertion is on the same fixture: the term-search path still
// caps, because that one IS a response-size guard.
func TestContributorTaxonomyBlankQueryEnumeratesWholeFamily(t *testing.T) {
	db := openStaffTestDB(t)
	seedStaffTaxonomy(t, db)

	// Comfortably past the 50-row search cap, the way the real facet is.
	const seeded = 60
	require.NoError(t, db.Exec("TRUNCATE galgame_engine RESTART IDENTITY CASCADE").Error)
	for i := 0; i < seeded; i++ {
		require.NoError(t, db.Create(&model.GalgameEngine{
			Name: fmt.Sprintf("enumfuse-%03d", i), Alias: []byte(`[]`),
		}).Error)
	}

	app := contribApp(db, 42, []string{"user"})

	code, body := staffGet(t, app, "/internal/galgame/taxonomy/engine/search?q=")
	require.Equal(t, 200, code)
	items := body["data"].(map[string]any)["items"].([]any)
	assert.Greater(t, len(items), service.TaxonomyPickerLimit,
		"a blank engine query must not stop at the search cap")
	assert.Len(t, items, seeded, "a blank engine query enumerates the WHOLE family")

	// Same rows, now reached through the term path: the cap applies there.
	code, body = staffGet(t, app, "/internal/galgame/taxonomy/engine/search?q=enumfuse")
	require.Equal(t, 200, code)
	items = body["data"].(map[string]any)["items"].([]any)
	assert.Len(t, items, service.TaxonomyPickerLimit,
		"a term search is still capped at TaxonomyPickerLimit")
}
