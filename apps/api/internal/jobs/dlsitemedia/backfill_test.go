package dlsitemedia

import (
	"context"
	"fmt"
	"os"
	"testing"

	"api/internal/platform/catalog/migrate"
	"api/internal/platform/catalog/model"
	"api/internal/platform/catalog/seed"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

var testDB *gorm.DB

func TestMain(m *testing.M) {
	dsn := os.Getenv("TEST_DATABASE_DSN")
	if dsn == "" {
		dsn = "host=localhost port=5432 user=postgres password=postgres dbname=kun_catalog_test sslmode=disable"
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		fmt.Fprintf(os.Stderr, "SKIP: cannot connect to test database: %v\n", err)
		os.Exit(0)
	}
	if err := migrate.Run(db); err != nil {
		fmt.Fprintf(os.Stderr, "SKIP: catalog migrate failed: %v\n", err)
		os.Exit(0)
	}
	if err := seed.Run(db); err != nil {
		fmt.Fprintf(os.Stderr, "SKIP: catalog seed failed: %v\n", err)
		os.Exit(0)
	}
	testDB = db
	os.Exit(m.Run())
}

// TestIntroWritePath exercises the pure-DB intro path end to end: the XOR guard
// (a CLAIMED work is refused), the no-text skip, the verbatim ja/dlsite write,
// and idempotency (both the preloaded-exists skip AND the ON CONFLICT DO NOTHING
// guard). The cover/screenshot upload paths need the image service and are
// validated by the rehearsal apply, not here.
func TestIntroWritePath(t *testing.T) {
	db := testDB
	for _, tbl := range []string{"catalog_work_intro", "catalog_work"} {
		require.NoError(t, db.Exec("TRUNCATE "+tbl+" RESTART IDENTITY CASCADE").Error)
	}

	reg, err := resolveRegistry(context.Background(), db)
	require.NoError(t, err)
	require.NotZero(t, reg.dlsiteSource)

	mkWork := func(name string, site *string) int64 {
		w := model.CatalogWork{MediumID: reg.galgameMedium, OLang: "ja", DisplayName: name, Site: site}
		require.NoError(t, db.Create(&w).Error)
		return w.ID
	}
	claimed := "galgame_wiki"
	wBody := mkWork("bodyless-with-intro", nil)
	wEmpty := mkWork("bodyless-no-text", nil)
	wClaimed := mkWork("claimed", &claimed)

	cands := []candidate{
		{WorkID: wBody, Workno: "RJ000001", Site: nil},
		{WorkID: wEmpty, Workno: "RJ000002", Site: nil},
		{WorkID: wClaimed, Workno: "RJ000003", Site: &claimed},
	}
	metas := map[string]dlsiteMeta{
		"RJ000001": {Age: "3", Intro: "A bodyless doujin blurb."},
		"RJ000002": {Age: "1", Intro: ""}, // no prose
		"RJ000003": {Age: "3", Intro: "Claimed — must be bridged, never copied."},
	}

	ctx := context.Background()
	run := func(apply bool) *runner {
		exist, err := preloadExisting(ctx, db, []int64{wBody, wEmpty, wClaimed}, reg.dlsiteSource, langJa)
		require.NoError(t, err)
		r := &runner{db: db, sourceID: reg.dlsiteSource, exist: exist}
		for _, c := range cands {
			r.writeIntro(ctx, c, metas[c.Workno], apply)
		}
		return r
	}

	// --- dry run: classifies, writes nothing.
	r := run(false)
	assert.Equal(t, 1, r.c.introWould, "wBody would write")
	assert.Equal(t, 1, r.c.introNoText, "wEmpty no text")
	assert.Equal(t, 1, r.c.introRefused, "wClaimed refused by XOR guard")
	assert.Equal(t, 0, r.c.introWritten)
	var n int64
	require.NoError(t, db.Raw("SELECT count(*) FROM catalog_work_intro").Scan(&n).Error)
	assert.EqualValues(t, 0, n, "dry run writes nothing")

	// --- apply: writes exactly wBody's intro (ja, dlsite, verbatim).
	r = run(true)
	assert.Equal(t, 1, r.c.introWritten)
	assert.Equal(t, 1, r.c.introRefused)
	var row model.CatalogWorkIntro
	require.NoError(t, db.Where("work_id = ?", wBody).First(&row).Error)
	assert.Equal(t, "ja", row.Lang)
	assert.EqualValues(t, reg.dlsiteSource, row.SourceID)
	assert.Equal(t, "A bodyless doujin blurb.", row.Intro)
	// Claimed work never materialised (bridge-not-copy).
	require.NoError(t, db.Raw("SELECT count(*) FROM catalog_work_intro WHERE work_id = ?", wClaimed).Scan(&n).Error)
	assert.EqualValues(t, 0, n)

	// --- second apply via the preloaded-exists skip: zero new writes.
	r = run(true)
	assert.Equal(t, 0, r.c.introWritten)
	assert.Equal(t, 1, r.c.introExists, "preloaded exists → skip before write")

	// --- and the ON CONFLICT guard: force a write attempt with a STALE (empty)
	// exist map; the DB unique key still refuses the duplicate.
	rStale := &runner{db: db, sourceID: reg.dlsiteSource,
		exist: &existing{intro: map[int64]bool{}, cover: map[int64]bool{}, shot: map[int64]map[int]bool{}}}
	rStale.writeIntro(ctx, cands[0], metas["RJ000001"], true)
	assert.Equal(t, 0, rStale.c.introWritten, "ON CONFLICT refuses the duplicate")
	assert.Equal(t, 1, rStale.c.introExists)
	require.NoError(t, db.Raw("SELECT count(*) FROM catalog_work_intro").Scan(&n).Error)
	assert.EqualValues(t, 1, n, "still exactly one row")
}
