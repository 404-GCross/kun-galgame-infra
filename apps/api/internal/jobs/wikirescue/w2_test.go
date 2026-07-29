package wikirescue

import (
	"context"
	"fmt"
	"os"
	"testing"

	catalogmigrate "api/internal/platform/catalog/migrate"
	"api/internal/platform/catalog/model"
	"api/internal/platform/catalog/seed"
	imagemodel "api/internal/platform/image/model"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// testDB is the catalog+galgame pool (the two families share one physical
// database everywhere since 2026-07-29); testImagesDB is the kun_images stand-in
// that the image-usage step writes ownership rows into.
var (
	testDB       *gorm.DB
	testImagesDB *gorm.DB
)

// TestMain provisions both pools. An EXPLICIT TEST_DATABASE_DSN that cannot be
// reached is a hard failure, not a skip: a silently skipped package still reports
// "ok", which is exactly how a wave convinces itself it has test evidence when it
// has none. Only the unset-DSN case (a developer with no local Postgres) skips.
func TestMain(m *testing.M) {
	dsn, explicit := os.LookupEnv("TEST_DATABASE_DSN")
	if !explicit {
		dsn = "host=localhost port=5432 user=postgres dbname=kun_catalog_test sslmode=disable"
	}
	db, err := open(dsn)
	if err != nil {
		fail(explicit, "connect to test database: %v", err)
	}
	if err := catalogmigrate.Run(db); err != nil {
		fail(explicit, "catalog migrate: %v", err)
	}
	if err := seed.Run(db); err != nil {
		fail(explicit, "catalog seed: %v", err)
	}

	// The image tables live in their own database in every real deployment; the
	// step only ever sees them through a second handle, so pointing that handle
	// at the same test database exercises the identical code path.
	imagesDSN := os.Getenv("TEST_IMAGES_DATABASE_DSN")
	if imagesDSN == "" {
		imagesDSN = dsn
	}
	imagesDB, err := open(imagesDSN)
	if err != nil {
		fail(explicit, "connect to images test database: %v", err)
	}
	if err := imagesDB.AutoMigrate(&imagemodel.Image{}, &imagemodel.ImageSiteUsage{}); err != nil {
		fail(explicit, "images migrate: %v", err)
	}

	testDB, testImagesDB = db, imagesDB
	os.Exit(m.Run())
}

func open(dsn string) (*gorm.DB, error) {
	return gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
}

func fail(explicit bool, format string, args ...any) {
	if explicit {
		fmt.Fprintf(os.Stderr, "FATAL: "+format+"\n", args...)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "SKIP: "+format+"\n", args...)
	os.Exit(0)
}

// hash builds a 64-character test hash. The image tables store hashes as
// char(64), so a short string would be space-padded and stop comparing equal to
// what the catalog side (text) holds — the fixtures use the real width.
func hash(n int) string { return fmt.Sprintf("%064d", n) }

// resetW2 clears everything the W2 steps read or write. The galgame tables are
// stubs shared with the catalog handler tests, which re-create their own rows on
// entry, so truncating them here is safe under -p 1.
func resetW2(t *testing.T) {
	t.Helper()
	ensureGalgameMediaStubs(t)
	for _, tbl := range []string{
		"galgame_cover", "galgame_screenshot", "galgame",
		"catalog_work_cover", "catalog_work_screenshot", "catalog_character",
	} {
		require.NoError(t, testDB.Exec("TRUNCATE "+tbl+" CASCADE").Error)
	}
	for _, tbl := range []string{"images", "image_site_usage"} {
		require.NoError(t, testImagesDB.Exec("TRUNCATE "+tbl+" CASCADE").Error)
	}
}

// ensureGalgameMediaStubs provisions the three wiki tables the W2 steps read.
// They mirror the production column shape the steps depend on; the real tables
// carry more columns, none of which these steps touch.
func ensureGalgameMediaStubs(t *testing.T) {
	t.Helper()
	require.NoError(t, testDB.Exec(`CREATE TABLE IF NOT EXISTS galgame (
		id bigint PRIMARY KEY,
		catalog_work_id bigint,
		user_id int NOT NULL DEFAULT 0,
		intro_en_us text NOT NULL DEFAULT '',
		intro_ja_jp text NOT NULL DEFAULT '',
		intro_zh_cn text NOT NULL DEFAULT '',
		intro_zh_tw text NOT NULL DEFAULT ''
	)`).Error)
	require.NoError(t, testDB.Exec(`CREATE TABLE IF NOT EXISTS galgame_cover (
		galgame_id bigint NOT NULL,
		image_hash text NOT NULL,
		sort_order int NOT NULL DEFAULT 0,
		kind text NOT NULL DEFAULT '',
		portrait_pinned boolean NOT NULL DEFAULT false,
		sexual smallint NOT NULL DEFAULT 0,
		violence smallint NOT NULL DEFAULT 0,
		source text NOT NULL DEFAULT '',
		PRIMARY KEY (galgame_id, image_hash)
	)`).Error)
	require.NoError(t, testDB.Exec(`CREATE TABLE IF NOT EXISTS galgame_screenshot (
		galgame_id bigint NOT NULL,
		image_hash text NOT NULL,
		sort_order bigint NOT NULL DEFAULT 0,
		caption text NOT NULL DEFAULT '',
		sexual smallint NOT NULL DEFAULT 0,
		violence smallint NOT NULL DEFAULT 0,
		source text NOT NULL DEFAULT '',
		PRIMARY KEY (galgame_id, image_hash)
	)`).Error)
}

// mkWork creates a catalog work and returns its id.
func mkWork(t *testing.T, name string) int64 {
	t.Helper()
	w := model.CatalogWork{MediumID: 1, OLang: "ja", DisplayName: name}
	require.NoError(t, testDB.Create(&w).Error)
	return w.ID
}

// mkGalgame inserts a wiki body row; workID 0 means unanchored.
func mkGalgame(t *testing.T, id, workID int64) {
	t.Helper()
	require.NoError(t, testDB.Exec(
		`INSERT INTO galgame (id, catalog_work_id, user_id) VALUES (?, ?, 0)`, id, workID).Error)
}

func mkCover(t *testing.T, galgameID int64, h string, sortOrder int, kind string, portrait bool, sexual, violence int16, source string) {
	t.Helper()
	require.NoError(t, testDB.Exec(`INSERT INTO galgame_cover
		(galgame_id, image_hash, sort_order, kind, portrait_pinned, sexual, violence, source)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		galgameID, h, sortOrder, kind, portrait, sexual, violence, source).Error)
}

func mkShot(t *testing.T, galgameID int64, h string, sortOrder int, caption string, sexual, violence int16, source string) {
	t.Helper()
	require.NoError(t, testDB.Exec(`INSERT INTO galgame_screenshot
		(galgame_id, image_hash, sort_order, caption, sexual, violence, source)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		galgameID, h, sortOrder, caption, sexual, violence, source).Error)
}

// newRunner wires a runner against the test pools.
func newRunner(t *testing.T, apply bool) *Runner {
	t.Helper()
	r, err := New(testDB, testDB, Opts{Apply: apply, Step: "all"})
	require.NoError(t, err)
	return r.WithImages(testImagesDB)
}

// sourceID resolves a catalog_source key from the seed.
func sourceID(t *testing.T, key string) int16 {
	t.Helper()
	id, err := resolveSourceID(testDB, key)
	require.NoError(t, err)
	return id
}

// TestParseSteps pins the selector the W2 wave runs through: a comma-separated
// selection, canonical ordering regardless of how it was typed, and a loud error
// on an unknown key (a typo must not silently run a subset).
func TestParseSteps(t *testing.T) {
	all, err := ParseSteps("all")
	require.NoError(t, err)
	assert.Equal(t, steps, all)

	empty, err := ParseSteps("")
	require.NoError(t, err)
	assert.Equal(t, steps, empty, "empty means all, matching the flag default")

	one, err := ParseSteps("m")
	require.NoError(t, err)
	assert.Equal(t, []string{"m"}, one)

	sel, err := ParseSteps(" O , M , n ,m")
	require.NoError(t, err)
	assert.Equal(t, []string{"m", "n", "o"}, sel, "canonical order, deduplicated, case-insensitive")

	_, err = ParseSteps("m,z")
	assert.Error(t, err, "an unknown step must fail the whole run")
	_, err = ParseSteps(",")
	assert.Error(t, err)
}

// TestCoverProjection pins step m: every anchored galgame_cover row projects with
// its shape intact, the source string maps to the right catalog_source (including
// the unsourced banner-migration rows and an unknown value), an unanchored row is
// parked rather than dropped, an existing native row is never overwritten, and a
// second pass writes nothing.
func TestCoverProjection(t *testing.T) {
	resetW2(t)
	ctx := context.Background()

	workA, workB := mkWork(t, "cover-a"), mkWork(t, "cover-b")
	mkGalgame(t, 9101, workA)
	mkGalgame(t, 9102, workB)
	mkGalgame(t, 9103, 0) // unanchored → parked

	mkCover(t, 9101, hash(1), 0, "main", true, 1, 2, "") // banner migration → galgame_wiki
	mkCover(t, 9101, hash(2), 1, "pkgfront", false, 0, 0, "vndb")
	mkCover(t, 9101, hash(3), 2, "dig", false, 0, 0, "upscale")
	mkCover(t, 9102, hash(4), 0, "", false, 0, 0, "bangumi")
	mkCover(t, 9102, hash(5), 1, "", false, 0, 0, "something-new") // unknown → galgame_wiki
	mkCover(t, 9103, hash(6), 0, "", false, 0, 0, "")

	// A native row that already holds one of the projected keys, under a foreign
	// source: fill-missing must leave it exactly as it is.
	require.NoError(t, testDB.Create(&model.CatalogWorkCover{
		WorkID: workB, ImageHash: hash(4), SortOrder: 9, Kind: "already",
		SourceID: sourceID(t, "dlsite")}).Error)

	dry, err := newRunner(t, false).runStep(ctx, "m")
	require.NoError(t, err)
	assert.Equal(t, 6, dry.Source)
	assert.Equal(t, 5, dry.Anchored)
	assert.Equal(t, 1, dry.Parked)
	assert.Equal(t, 5, dry.Planned)
	assert.Zero(t, dry.Written, "dry run writes nothing")
	var afterDry int64
	require.NoError(t, testDB.Model(&model.CatalogWorkCover{}).Count(&afterDry).Error)
	assert.EqualValues(t, 1, afterDry, "only the pre-existing row")

	st, err := newRunner(t, true).runStep(ctx, "m")
	require.NoError(t, err)
	assert.Equal(t, 5, st.Planned)
	assert.Equal(t, 4, st.Written, "the pre-existing (work, hash) is skipped")
	assert.Equal(t, 2, st.Touched, "both anchored works received a row")

	var rows []model.CatalogWorkCover
	require.NoError(t, testDB.Find(&rows).Error)
	require.Len(t, rows, 5)
	byHash := make(map[string]model.CatalogWorkCover, len(rows))
	for _, row := range rows {
		byHash[row.ImageHash] = row
	}
	pinned := byHash[hash(1)]
	assert.Equal(t, workA, pinned.WorkID)
	assert.Equal(t, "main", pinned.Kind)
	assert.True(t, pinned.PortraitPinned, "the portrait pin carries over")
	assert.Zero(t, pinned.SortOrder)
	assert.EqualValues(t, 1, pinned.Sexual)
	assert.EqualValues(t, 2, pinned.Violence)
	assert.Equal(t, sourceID(t, "galgame_wiki"), pinned.SourceID, "unsourced = wiki curation")
	assert.Equal(t, sourceID(t, "vndb"), byHash[hash(2)].SourceID)
	assert.Equal(t, sourceID(t, "upscale"), byHash[hash(3)].SourceID)
	assert.Equal(t, sourceID(t, "dlsite"), byHash[hash(4)].SourceID, "the pre-existing row keeps its source")
	assert.Equal(t, "already", byHash[hash(4)].Kind, "and its whole shape")
	assert.EqualValues(t, 9, byHash[hash(4)].SortOrder)
	assert.Equal(t, sourceID(t, "galgame_wiki"), byHash[hash(5)].SourceID, "unknown source degrades to wiki")

	second, err := newRunner(t, true).runStep(ctx, "m")
	require.NoError(t, err)
	assert.Equal(t, 5, second.Planned)
	assert.Zero(t, second.Written, "second pass converges")
	assert.Zero(t, second.Touched, "and touches nothing")
}

// TestScreenshotProjection pins step n: the VNDB rows this wave adds project
// alongside the non-VNDB rows step b already rescued, a row that already landed
// keeps the attribution it landed with, and a second pass writes nothing.
func TestScreenshotProjection(t *testing.T) {
	resetW2(t)
	ctx := context.Background()

	work := mkWork(t, "shots")
	mkGalgame(t, 9111, work)
	mkShot(t, 9111, hash(11), 0, "opening", 1, 0, "vndb")
	mkShot(t, 9111, hash(12), 1, "user upload", 0, 0, "")

	// What step b left behind: the non-VNDB row, filed under galgame_wiki.
	require.NoError(t, testDB.Create(&model.CatalogWorkScreenshot{
		WorkID: work, ImageHash: hash(12), SortOrder: 1, Caption: "user upload",
		SourceID: sourceID(t, "galgame_wiki")}).Error)

	st, err := newRunner(t, true).runStep(ctx, "n")
	require.NoError(t, err)
	assert.Equal(t, 2, st.Source)
	assert.Equal(t, 2, st.Planned)
	assert.Equal(t, 1, st.Written, "the rescued row is skipped by the unique key")
	assert.Equal(t, 1, st.Touched)

	var rows []model.CatalogWorkScreenshot
	require.NoError(t, testDB.Order("sort_order").Find(&rows).Error)
	require.Len(t, rows, 2)
	assert.Equal(t, hash(11), rows[0].ImageHash)
	assert.Equal(t, "opening", rows[0].Caption)
	assert.EqualValues(t, 1, rows[0].Sexual)
	assert.Equal(t, sourceID(t, "vndb"), rows[0].SourceID)
	assert.Equal(t, sourceID(t, "galgame_wiki"), rows[1].SourceID)

	second, err := newRunner(t, true).runStep(ctx, "n")
	require.NoError(t, err)
	assert.Zero(t, second.Written)
	assert.Zero(t, second.Touched)
}

// TestImageUsageAdoption pins step o, the only step that writes to the images
// database. It has to be provably conservative: adopt exactly the catalog-face
// hashes that a live image row backs and the catalog site does not already own,
// read the owning client from the data instead of inventing one, leave every
// galgame_wiki row byte-identical, and converge on a second pass.
func TestImageUsageAdoption(t *testing.T) {
	resetW2(t)
	ctx := context.Background()

	const catalogClient = "catalog-client-abc"
	work := mkWork(t, "usage")

	// Catalog face: two cover hashes, one screenshot hash, one character portrait.
	require.NoError(t, testDB.Create(&model.CatalogWorkCover{
		WorkID: work, ImageHash: hash(21), SourceID: sourceID(t, "galgame_wiki")}).Error)
	require.NoError(t, testDB.Create(&model.CatalogWorkCover{
		WorkID: work, ImageHash: hash(22), SourceID: sourceID(t, "galgame_wiki")}).Error)
	require.NoError(t, testDB.Create(&model.CatalogWorkScreenshot{
		WorkID: work, ImageHash: hash(23), SourceID: sourceID(t, "galgame_wiki")}).Error)
	require.NoError(t, testDB.Exec(
		`INSERT INTO catalog_character (display_name, image_hash, created_at, updated_at)
		 VALUES ('portrait', ?, now(), now())`, hash(24)).Error)
	// Two references the step must refuse to adopt: a soft-deleted image and a
	// hash with no image row at all.
	require.NoError(t, testDB.Create(&model.CatalogWorkScreenshot{
		WorkID: work, ImageHash: hash(25), SourceID: sourceID(t, "galgame_wiki")}).Error)
	require.NoError(t, testDB.Create(&model.CatalogWorkScreenshot{
		WorkID: work, ImageHash: hash(26), SourceID: sourceID(t, "galgame_wiki")}).Error)
	// And a soft-deleted character portrait, which is NOT part of the face at all.
	require.NoError(t, testDB.Exec(
		`INSERT INTO catalog_character (display_name, image_hash, created_at, updated_at, deleted_at)
		 VALUES ('deleted', ?, now(), now(), now())`, hash(27)).Error)

	mkImage := func(h string, deleted bool) {
		del := "NULL"
		if deleted {
			del = "now()"
		}
		require.NoError(t, testImagesDB.Exec(fmt.Sprintf(
			`INSERT INTO images (hash, storage_key, mime, ext, width, height, size_bytes, variants,
			                     review_status, last_referenced_at, created_at, deleted_at, thumbhash)
			 VALUES (?, ?, 'image/webp', 'webp', 1, 1, 1, '[]', 1, now(), now(), %s, '')`, del),
			h, "k/"+h).Error)
	}
	for _, h := range []string{hash(21), hash(22), hash(23), hash(24), hash(27)} {
		mkImage(h, false)
	}
	mkImage(hash(25), true) // soft-deleted
	// hash(26) has no images row at all.

	mkUsage := func(h, site, client string) {
		require.NoError(t, testImagesDB.Exec(
			`INSERT INTO image_site_usage (hash, site, first_uploader_client, first_uploaded_at, upload_count, last_uploaded_at)
			 VALUES (?, ?, ?, now(), 7, now())`, h, site, client).Error)
	}
	// The catalog site already owns one hash — and that row is where the client id
	// comes from. Everything else is wiki-owned.
	mkUsage(hash(21), siteCatalog, catalogClient)
	for _, h := range []string{hash(21), hash(22), hash(23), hash(25), hash(26)} {
		mkUsage(h, "galgame_wiki", "galgame-wiki-admin")
	}

	dry, err := newRunner(t, false).runStep(ctx, StepImageUsage)
	require.NoError(t, err)
	assert.Equal(t, 6, dry.Source, "five work-media hashes + the live portrait")
	assert.Equal(t, 4, dry.Anchored, "hashes backed by a live image row")
	assert.Equal(t, 2, dry.Parked, "soft-deleted + no image row")
	assert.Equal(t, 3, dry.Planned, "hash 21 is already owned by catalog")
	assert.Zero(t, dry.Written)
	assert.Contains(t, dry.Note, "client="+catalogClient, "the client is read, not invented")
	var usageAfterDry int64
	require.NoError(t, testImagesDB.Table("image_site_usage").Count(&usageAfterDry).Error)
	assert.EqualValues(t, 6, usageAfterDry, "dry run writes nothing")

	st, err := newRunner(t, true).runStep(ctx, StepImageUsage)
	require.NoError(t, err)
	assert.Equal(t, 3, st.Planned)
	assert.Equal(t, 3, st.Written)

	var adopted []imagemodel.ImageSiteUsage
	require.NoError(t, testImagesDB.Where("site = ?", siteCatalog).Order("hash").Find(&adopted).Error)
	require.Len(t, adopted, 4)
	for _, u := range adopted {
		assert.Equal(t, catalogClient, u.FirstUploaderClient)
	}
	assert.Equal(t, hash(21), adopted[0].Hash)
	assert.EqualValues(t, 7, adopted[0].UploadCount, "the pre-existing row is never rewritten")
	assert.EqualValues(t, 1, adopted[1].UploadCount, "adopted rows start at one")
	assert.Equal(t, []string{hash(22), hash(23), hash(24)},
		[]string{adopted[1].Hash, adopted[2].Hash, adopted[3].Hash})

	// The wiki lane is untouched: same count, same client, same upload_count.
	var wiki []imagemodel.ImageSiteUsage
	require.NoError(t, testImagesDB.Where("site = ?", "galgame_wiki").Order("hash").Find(&wiki).Error)
	require.Len(t, wiki, 5)
	for _, u := range wiki {
		assert.Equal(t, "galgame-wiki-admin", u.FirstUploaderClient)
		assert.EqualValues(t, 7, u.UploadCount)
	}
	// And no image row was modified.
	var liveImages int64
	require.NoError(t, testImagesDB.Table("images").Where("deleted_at IS NULL").Count(&liveImages).Error)
	assert.EqualValues(t, 5, liveImages)

	second, err := newRunner(t, true).runStep(ctx, StepImageUsage)
	require.NoError(t, err)
	assert.Zero(t, second.Planned, "converged: nothing left to adopt")
	assert.Zero(t, second.Written)
}

// TestImageUsageRefusesWithoutCatalogClient pins the refusal that keeps the audit
// table honest: with no catalog-site row to read a client id from, the step must
// fail instead of writing an invented owner onto 174k images.
func TestImageUsageRefusesWithoutCatalogClient(t *testing.T) {
	resetW2(t)
	ctx := context.Background()

	work := mkWork(t, "no-client")
	require.NoError(t, testDB.Create(&model.CatalogWorkCover{
		WorkID: work, ImageHash: hash(31), SourceID: sourceID(t, "galgame_wiki")}).Error)

	_, err := newRunner(t, true).runStep(ctx, StepImageUsage)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "refusing to invent an owner")

	var n int64
	require.NoError(t, testImagesDB.Table("image_site_usage").Count(&n).Error)
	assert.Zero(t, n)
}

// TestImageUsageNeedsPool pins the wiring guard: a runner without the images pool
// must fail loudly rather than no-op its way to a clean-looking ledger.
func TestImageUsageNeedsPool(t *testing.T) {
	r, err := New(testDB, testDB, Opts{Apply: true, Step: StepImageUsage})
	require.NoError(t, err)
	_, err = r.runStep(context.Background(), StepImageUsage)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "WithImages")
}
