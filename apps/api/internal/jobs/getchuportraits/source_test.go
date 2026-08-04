package getchuportraits

import (
	"context"
	"fmt"
	"os"
	"testing"

	"api/internal/jobs/getchuchars"
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

// Integration test against a real Postgres: the catalog Gold schema plus
// stand-ins for the crawler's staging tables. In production those are two
// databases; here one DSN plays both parts, which is faithful because the job
// never joins across them — it reads the staging side into a map.
var testDB *gorm.DB

// TestMain gates the DB-backed tests PER TEST (dbtest.Skip) rather than exiting
// the package: pick_test.go and pool_test.go hold pure functions, and a
// package-level exit would report them as `ok` while running none of them.
func TestMain(m *testing.M) {
	dsn, ok := dbtest.DSN()
	if !ok {
		fmt.Fprintln(os.Stderr, "SKIP: no TEST_DATABASE_DSN — DB-backed getchuportraits tests will skip individually")
		os.Exit(m.Run())
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
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
	// The staging stand-ins. Only the columns this job reads matter; the real
	// tables (kun-getchu-api) carry more.
	for _, ddl := range []string{
		`CREATE TABLE IF NOT EXISTS item_characters (
			getchu_id text NOT NULL, ordinal int NOT NULL, name text NOT NULL,
			nameplate_url text, PRIMARY KEY (getchu_id, ordinal))`,
		`CREATE TABLE IF NOT EXISTS item_images (
			getchu_id text NOT NULL, kind text NOT NULL, ordinal int NOT NULL,
			url text NOT NULL, local_path text, PRIMARY KEY (getchu_id, kind, ordinal))`,
	} {
		if err := db.Exec(ddl).Error; err != nil {
			fmt.Fprintf(os.Stderr, "FAIL: staging stand-in: %v\n", err)
			os.Exit(1)
		}
	}
	testDB = db
	os.Exit(m.Run())
}

// requireDB skips only when there is genuinely no database — dbtest.Skip is the
// no-DSN branch, not a conditional guard, so calling it unguarded skips a
// suite that was handed a perfectly good DSN.
func requireDB(t *testing.T) {
	t.Helper()
	if testDB == nil {
		dbtest.Skip(t)
	}
}

func reset(t *testing.T) {
	t.Helper()
	require.NoError(t, testDB.Exec(`TRUNCATE item_characters, item_images`).Error)
	require.NoError(t, testDB.Exec(`DELETE FROM catalog_character`).Error)
}

// mkChar inserts a catalog character, with or without a portrait already.
func mkChar(t *testing.T, name string, hash *string) int64 {
	t.Helper()
	c := model.CatalogCharacter{DisplayName: name, ImageHash: hash}
	require.NoError(t, testDB.Create(&c).Error)
	return c.ID
}

// mkPlate stages a roster row and, when mirrored, the image row that proves its
// bytes were downloaded.
func mkPlate(t *testing.T, getchuID string, ordinal int, name, url string, mirrored bool) {
	t.Helper()
	require.NoError(t, testDB.Exec(
		`INSERT INTO item_characters (getchu_id, ordinal, name, nameplate_url) VALUES (?,?,?,?)`,
		getchuID, ordinal, name, url).Error)
	if url == "" {
		return
	}
	var local any
	if mirrored {
		local = "/crawler/mirror/" + getchuID + "/x.jpg"
	}
	require.NoError(t, testDB.Exec(
		`INSERT INTO item_images (getchu_id, kind, ordinal, url, local_path) VALUES (?,?,?,?,?)`,
		getchuID, "nameplate", ordinal, url, local).Error)
}

func cand(id int64, eds ...getchuchars.Edition) getchuchars.Candidate {
	return getchuchars.Candidate{
		CharacterID: id, GetchuID: eds[0].GetchuID, Ordinal: eds[0].Ordinal, Editions: eds,
	}
}

func ed(getchuID string, ordinal int) getchuchars.Edition {
	return getchuchars.Edition{GetchuID: getchuID, Ordinal: ordinal}
}

// The core admission rule: fill-missing. A character that already has a
// portrait is skipped and counted, never overwritten — Getchu is the fallback
// for this facet, and a VNDB portrait outranks it.
func TestSelectCandidatesSkipsCharactersThatAlreadyHaveAPortrait(t *testing.T) {
	requireDB(t)
	reset(t)
	existing := "deadbeef"
	withArt := mkChar(t, "有图", &existing)
	without := mkChar(t, "无图", nil)
	mkPlate(t, "100", 1, "有图", "https://g/brandnew/100/c100chara1.jpg", true)
	mkPlate(t, "100", 2, "无图", "https://g/brandnew/100/c100chara2.jpg", true)

	st := &Stats{}
	got, err := selectCandidates(context.Background(), testDB, testDB,
		[]getchuchars.Candidate{cand(withArt, ed("100", 1)), cand(without, ed("100", 2))}, st)
	require.NoError(t, err)

	require.Len(t, got, 1)
	assert.Equal(t, without, got[0].CharacterID)
	assert.Equal(t, "c100chara2.jpg", got[0].File)
	assert.Equal(t, 1, st.SkipHasImage)
	assert.Equal(t, 0, st.NoImage)
}

// "Offers art" and "the bytes are downloaded" are separate facts, and selection
// only asks the first. Requiring the second here made the pre-mirror dry run
// report a population of zero — which is the moment the number is most needed,
// because it IS the mirror worklist. Un-downloaded bytes surface later, as
// Missing.
func TestSelectCandidatesDoesNotRequireMirroredBytes(t *testing.T) {
	requireDB(t)
	reset(t)
	id := mkChar(t, "未镜像", nil)
	mkPlate(t, "100", 1, "未镜像", "https://g/brandnew/100/c100chara1.jpg", false)

	st := &Stats{}
	got, err := selectCandidates(context.Background(), testDB, testDB,
		[]getchuchars.Candidate{cand(id, ed("100", 1))}, st)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "c100chara1.jpg", got[0].File)
	assert.Equal(t, 0, st.NoImage)
}

// NoImage must stay meaningful: a character no edition of which offers a
// nameplate is genuinely unfillable, and is counted apart from one that is
// merely waiting on the mirror.
func TestSelectCandidatesCountsGenuinelyArtlessCharacters(t *testing.T) {
	requireDB(t)
	reset(t)
	id := mkChar(t, "无立绘", nil)
	mkPlate(t, "100", 1, "无立绘", "", false)

	st := &Stats{}
	got, err := selectCandidates(context.Background(), testDB, testDB,
		[]getchuchars.Candidate{cand(id, ed("100", 1))}, st)
	require.NoError(t, err)
	assert.Empty(t, got)
	assert.Equal(t, 1, st.NoImage)
}

// The Editions fallback, end to end through SQL: the richest edition carries no
// art, a second one does, and the character is still fillable from the second.
func TestSelectCandidatesFallsBackAcrossEditions(t *testing.T) {
	requireDB(t)
	reset(t)
	id := mkChar(t, "九條都", nil)
	mkPlate(t, "limited", 7, "九條都", "", false)
	mkPlate(t, "regular", 3, "九條都", "https://g/brandnew/regular/cregularchara3.jpg", true)

	st := &Stats{}
	got, err := selectCandidates(context.Background(), testDB, testDB,
		[]getchuchars.Candidate{cand(id, ed("limited", 7), ed("regular", 3))}, st)
	require.NoError(t, err)

	require.Len(t, got, 1)
	assert.Equal(t, "regular", got[0].GetchuID)
	assert.Equal(t, "cregularchara3.jpg", got[0].File)
	assert.Equal(t, 0, st.NoImage)
}

// A soft-deleted character must not be filled. loadPortraitless asks for the
// rows that NEED filling, so a character that disappeared between the match and
// the query falls out rather than staying in by omission.
func TestSelectCandidatesIgnoresDeletedCharacters(t *testing.T) {
	requireDB(t)
	reset(t)
	id := mkChar(t, "已删", nil)
	require.NoError(t, testDB.Exec(`UPDATE catalog_character SET deleted_at = now() WHERE id = ?`, id).Error)
	mkPlate(t, "100", 1, "已删", "https://g/brandnew/100/c100chara1.jpg", true)

	st := &Stats{}
	got, err := selectCandidates(context.Background(), testDB, testDB,
		[]getchuchars.Candidate{cand(id, ed("100", 1))}, st)
	require.NoError(t, err)
	assert.Empty(t, got)
	assert.Equal(t, 1, st.SkipHasImage)
}

// Only nameplates. The full-body `portrait` kind lives at the same URL shape
// and would silently pass if the kind filter were dropped — it belongs to a
// future wave and a different column.
func TestLoadNameplatesIgnoresOtherKinds(t *testing.T) {
	requireDB(t)
	reset(t)
	url := "https://g/brandnew/100/c100charab1.jpg"
	require.NoError(t, testDB.Exec(
		`INSERT INTO item_characters (getchu_id, ordinal, name, nameplate_url) VALUES ('100',1,'x',?)`, url).Error)
	require.NoError(t, testDB.Exec(
		`INSERT INTO item_images (getchu_id, kind, ordinal, url, local_path) VALUES ('100','portrait',1,?,'/m/x.jpg')`, url).Error)

	got, err := loadNameplates(context.Background(), testDB)
	require.NoError(t, err)
	assert.Empty(t, got, "a full-body portrait must not be picked up as a bust")
}
