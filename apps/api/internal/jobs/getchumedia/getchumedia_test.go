package getchumedia

import (
	"context"
	"fmt"
	"os"
	"testing"

	"api/internal/platform/catalog/migrate"
	"api/internal/platform/catalog/model"
	"api/internal/platform/catalog/seed"
	"api/internal/testsupport/dbtest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// Integration tests for the candidate rule and the staging read. The UPLOAD
// half is not exercised here — it needs a live image service, and it is
// byte-for-byte the dlsitemedia recipe, which has its own coverage. What is
// specific to this lane, and therefore tested, is WHICH works it admits and
// WHICH staged images it considers.
var (
	testDB  *gorm.DB
	testDSN string
)

func TestMain(m *testing.M) {
	var ok bool
	testDSN, ok = dbtest.DSN()
	if !ok {
		fmt.Fprintln(os.Stderr, "SKIP: no TEST_DATABASE_DSN — DB-backed getchumedia tests will skip individually")
		os.Exit(m.Run())
	}
	db, err := gorm.Open(postgres.Open(testDSN), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		fmt.Fprintln(os.Stderr, "FAIL: cannot connect to the assigned test database")
		os.Exit(1)
	}
	if err := migrate.Run(db); err != nil {
		fmt.Fprintf(os.Stderr, "FAIL: catalog migrate failed: %v\n", err)
		os.Exit(1)
	}
	if err := seed.Run(db); err != nil {
		fmt.Fprintf(os.Stderr, "FAIL: catalog seed failed: %v\n", err)
		os.Exit(1)
	}
	// The crawler's staging table, reduced to the columns this job reads.
	//
	// DROP first, deliberately. Several packages stand in for the same staging
	// tables with DIFFERENT column sets, and they share one test database:
	// CREATE TABLE IF NOT EXISTS would silently inherit whichever package ran
	// earlier and then fail on the columns it lacks. Owning the fixture outright
	// makes the suite independent of package order. Safe because DB-backed
	// suites run with -p 1.
	if err := db.Exec(`DROP TABLE IF EXISTS item_images`).Error; err != nil {
		fmt.Fprintf(os.Stderr, "FAIL: drop staging item_images: %v\n", err)
		os.Exit(1)
	}
	if err := db.Exec(`CREATE TABLE item_images (
		getchu_id text, kind text, ordinal int, url text, local_path text, sha256 text)`).Error; err != nil {
		fmt.Fprintf(os.Stderr, "FAIL: staging item_images: %v\n", err)
		os.Exit(1)
	}
	testDB = db
	os.Exit(m.Run())
}

const (
	getchuSource  = int16(17)
	galgameMedium = int16(1)
	getchuRule    = "rule:vndb-extlink-getchu"
)

func clean(t *testing.T) {
	t.Helper()
	if testDB == nil {
		dbtest.Skip(t)
	}
	for _, table := range []string{"catalog_external_ref", "catalog_release", "item_images"} {
		require.NoError(t, testDB.Exec("TRUNCATE "+table+" CASCADE").Error)
	}
	for _, table := range []string{"catalog_work_screenshot", "catalog_work"} {
		require.NoError(t, testDB.Exec("TRUNCATE "+table+" RESTART IDENTITY CASCADE").Error)
	}
}

func mkWork(t *testing.T, name string, rating int16) int64 {
	t.Helper()
	w := model.CatalogWork{MediumID: galgameMedium, OLang: "ja", DisplayName: name, ContentRating: rating}
	require.NoError(t, testDB.Create(&w).Error)
	return w.ID
}

func mkAnchor(t *testing.T, workID int64, getchuID string, kind int16) {
	t.Helper()
	rel := model.CatalogRelease{WorkID: workID, Kind: 0}
	require.NoError(t, testDB.Create(&rel).Error)
	require.NoError(t, testDB.Exec(`
		INSERT INTO catalog_external_ref (entity_type, entity_id, source_id, external_id, link_kind, matched_by)
		VALUES (?,?,?,?,?,?)`,
		model.EntityTypeRelease, rel.ID, getchuSource, getchuID, kind, getchuRule).Error)
}

func mkStaged(t *testing.T, getchuID, kind string, ordinal int, url string, mirrored bool) {
	t.Helper()
	var local any
	if mirrored {
		local = "/mirror/" + getchuID + "/x.jpg"
	}
	require.NoError(t, testDB.Exec(
		`INSERT INTO item_images (getchu_id, kind, ordinal, url, local_path) VALUES (?,?,?,?,?)`,
		getchuID, kind, ordinal, url, local).Error)
}

func mkShot(t *testing.T, workID int64, hash string) {
	t.Helper()
	require.NoError(t, testDB.Create(&model.CatalogWorkScreenshot{
		WorkID: workID, ImageHash: hash, SourceID: getchuSource}).Error)
}

func candidates(t *testing.T) []candidate {
	t.Helper()
	got, err := loadCandidates(context.Background(), testDB, getchuSource, galgameMedium)
	require.NoError(t, err)
	return got
}

// --- tests ----------------------------------------------------------------

// Getchu is a FALLBACK for this facet: a work that already shows a gallery
// keeps it, whatever the source. Getting this backwards would splice store
// promo CG into curated galleries across the whole anchored population.
func TestOnlyWorksWithNoScreenshotAreAdmitted(t *testing.T) {
	clean(t)
	empty := mkWork(t, "no gallery", 2)
	mkAnchor(t, empty, "1001", model.LinkKindExact)
	full := mkWork(t, "has a gallery", 2)
	mkAnchor(t, full, "1002", model.LinkKindExact)
	mkShot(t, full, "deadbeef")

	got := candidates(t)
	require.Len(t, got, 1)
	assert.Equal(t, empty, got[0].WorkID)
}

// Only EXACT anchors. A probable ref is a guess, and this lane writes bytes.
func TestProbableAnchorIsNotAdmitted(t *testing.T) {
	clean(t)
	w := mkWork(t, "probable only", 2)
	mkAnchor(t, w, "2001", model.LinkKindProbable)
	assert.Empty(t, candidates(t))
}

// The rating a screenshot carries comes from the work, so the gallery can never
// disagree with the page it hangs on.
func TestCandidateCarriesTheWorksContentRating(t *testing.T) {
	clean(t)
	w := mkWork(t, "all ages", 0)
	mkAnchor(t, w, "3001", model.LinkKindExact)
	got := candidates(t)
	require.Len(t, got, 1)
	assert.EqualValues(t, 0, got[0].ContentRating)
}

// A work with several Getchu releases collects every anchor, because its
// samples are spread across them.
func TestMultipleAnchorsAreAllCollected(t *testing.T) {
	clean(t)
	w := mkWork(t, "two editions", 2)
	mkAnchor(t, w, "4001", model.LinkKindExact)
	mkAnchor(t, w, "4002", model.LinkKindExact)
	got := candidates(t)
	require.Len(t, got, 1)
	assert.ElementsMatch(t, []string{"4001", "4002"}, got[0].GetchuIDs)
}

// The staging read takes mirrored `sample` rows only: the other kinds are not
// screenshots, an unmirrored row has no bytes to upload, and `_s.jpg` is
// Getchu's thumbnail of the image beside it — uploading one would put a postage
// stamp in the gallery.
func TestLoadSamplesTakesOnlyMirroredFullSizeSamples(t *testing.T) {
	clean(t)
	mkStaged(t, "5001", "sample", 0, "https://www.getchu.com/brandnew/5001/c5001sample1.jpg", true)
	mkStaged(t, "5001", "sample", 1, "https://www.getchu.com/brandnew/5001/c5001sample2_s.jpg", true)
	mkStaged(t, "5001", "sample", 2, "https://www.getchu.com/brandnew/5001/c5001sample3.jpg", false)
	mkStaged(t, "5001", "package", 0, "https://www.getchu.com/brandnew/5001/c5001package.jpg", true)
	mkStaged(t, "5001", "portrait", 0, "https://www.getchu.com/brandnew/5001/c5001charab1.jpg", true)

	got, err := loadSamples(context.Background(), testDB)
	require.NoError(t, err)
	require.Len(t, got["5001"], 1)
	assert.Equal(t, "c5001sample1.jpg", got["5001"][0].File)
}

func TestPreloadHashes(t *testing.T) {
	clean(t)
	w := mkWork(t, "w", 2)
	mkShot(t, w, "aaa")
	mkShot(t, w, "bbb")

	got, err := preloadHashes(context.Background(), testDB, []int64{w})
	require.NoError(t, err)
	assert.True(t, got[w]["aaa"])
	assert.True(t, got[w]["bbb"])
	assert.False(t, got[w]["ccc"])
}

// Both DSNs are required, and applying without a mirror directory is refused
// before anything connects — bytes only ever come from the local mirror.
func TestRequiredInputs(t *testing.T) {
	_, err := Run(context.Background(), nil, Opts{GetchuDSN: "x"})
	require.Error(t, err)
	_, err = Run(context.Background(), nil, Opts{DSN: "x"})
	require.Error(t, err)
	_, err = Run(context.Background(), nil, Opts{DSN: "x", GetchuDSN: "y", Apply: true})
	require.ErrorContains(t, err, "--mirror-dir")
}
