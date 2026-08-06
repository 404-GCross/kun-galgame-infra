package imagerefs

import (
	"context"
	"fmt"
	"os"
	"sync"
	"testing"

	"api/internal/platform/catalog/migrate"
	"api/internal/platform/catalog/model"
	"api/internal/platform/catalog/seed"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	glogger "gorm.io/gorm/logger"
)

// TestKindsCoversEveryImageColumn is the tripwire for the failure this package
// exists to prevent: a seventh image column added to the schema without an
// entry here is invisible to the keep-alive sweep, and the loss arrives a year
// later. If this test fails because a kind was added, add the column
// everywhere (registry, this list) — never just relax the assertion.
func TestKindsCoversEveryImageColumn(t *testing.T) {
	assert.Equal(t, []string{
		KindWorkCover, KindWorkScreenshot, KindCharacterBust,
		KindCharacterFigure, KindLabelLogo, KindPersonPhoto,
	}, Kinds())
}

// TestDetachSetMatchesColumnNullability pins the half of the spec a schema
// change breaks silently: writing NULL into a NOT NULL column errors loudly,
// but writing '' into a nullable one leaves a row that no longer references an
// image yet still passes the "hash present" filter nowhere — it just reads as
// an empty-string hash forever.
func TestDetachSetMatchesColumnNullability(t *testing.T) {
	want := map[string]string{
		KindWorkCover: "", KindWorkScreenshot: "", // the row IS the reference
		KindCharacterBust: "NULL", KindCharacterFigure: "NULL", // *string columns
		KindLabelLogo: "''", KindPersonPhoto: "''", // NOT NULL DEFAULT ''
	}
	for _, s := range specs {
		assert.Equalf(t, want[s.Kind], s.DetachSet, "detach value for %s", s.Kind)
	}
}

// --- DB-backed ---

var (
	testOnce sync.Once
	testDB   *gorm.DB
	testErr  error
)

func openTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	testOnce.Do(func() {
		dsn := os.Getenv("TEST_DATABASE_DSN")
		if dsn == "" {
			dsn = "host=localhost port=5432 user=postgres password=postgres dbname=kun_catalog_test sslmode=disable"
		}
		db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: glogger.Default.LogMode(glogger.Silent)})
		if err != nil {
			testErr = fmt.Errorf("catalog test database unreachable: %w", err)
			return
		}
		if err := migrate.Run(db); err != nil {
			testErr = fmt.Errorf("catalog migrate failed: %w", err)
			return
		}
		if err := seed.Run(db); err != nil {
			testErr = fmt.Errorf("catalog seed failed: %w", err)
			return
		}
		testDB = db
	})
	if testErr != nil {
		t.Skip(testErr.Error())
	}
	return testDB
}

const (
	hashShared = "1111111111111111111111111111111111111111111111111111111111111111"
	hashOther  = "2222222222222222222222222222222222222222222222222222222222222222"
	hashDead   = "3333333333333333333333333333333333333333333333333333333333333333"
)

// fixture writes one row of every kind pointing at hashShared, plus decoys:
// a second work cover on another hash, and a SOFT-DELETED character/label/person
// whose images must not count as references.
func fixture(t *testing.T, db *gorm.DB) (workID, charID, labelID, personID int64) {
	t.Helper()
	for _, tbl := range []string{
		"catalog_work_cover", "catalog_work_screenshot", "catalog_work",
		"catalog_character", "catalog_label", "catalog_person",
	} {
		require.NoError(t, db.Exec("TRUNCATE "+tbl+" RESTART IDENTITY CASCADE").Error)
	}

	work := &model.CatalogWork{MediumID: 1, OLang: "ja", DisplayName: "作品", Status: model.WorkStatusStub}
	require.NoError(t, db.Create(work).Error)
	require.NoError(t, db.Create(&model.CatalogWorkCover{WorkID: work.ID, ImageHash: hashShared, SourceID: 1}).Error)
	require.NoError(t, db.Create(&model.CatalogWorkCover{WorkID: work.ID, ImageHash: hashOther, SourceID: 1}).Error)
	require.NoError(t, db.Create(&model.CatalogWorkScreenshot{WorkID: work.ID, ImageHash: hashShared, SourceID: 1}).Error)

	shared := hashShared
	char := &model.CatalogCharacter{DisplayName: "角色", ImageHash: &shared, FigureHash: &shared}
	require.NoError(t, db.Create(char).Error)
	label := &model.CatalogLabel{DisplayName: "会社", LogoHash: hashShared}
	require.NoError(t, db.Create(label).Error)
	person := &model.CatalogPerson{DisplayName: "人物", PhotoHash: hashShared}
	require.NoError(t, db.Create(person).Error)

	// Soft-deleted decoys: a live row is the only thing that holds a reference.
	deadChar := &model.CatalogCharacter{DisplayName: "亡角色", ImageHash: &shared}
	require.NoError(t, db.Create(deadChar).Error)
	require.NoError(t, db.Delete(deadChar).Error)
	deadLabel := &model.CatalogLabel{DisplayName: "亡会社", LogoHash: hashShared}
	require.NoError(t, db.Create(deadLabel).Error)
	require.NoError(t, db.Delete(deadLabel).Error)
	deadPerson := &model.CatalogPerson{DisplayName: "亡人物", PhotoHash: hashShared}
	require.NoError(t, db.Create(deadPerson).Error)
	require.NoError(t, db.Delete(deadPerson).Error)

	return work.ID, char.ID, label.ID, person.ID
}

func kindCounts(refs []Ref) map[string]int {
	out := map[string]int{}
	for _, r := range refs {
		out[r.Kind]++
	}
	return out
}

// TestCollectSeesEveryKindAndSkipsSoftDeleted: the full sweep reads all six
// columns, and the soft-deleted decoys contribute nothing.
func TestCollectSeesEveryKindAndSkipsSoftDeleted(t *testing.T) {
	db := openTestDB(t)
	fixture(t, db)

	refs, err := Collect(context.Background(), db)
	require.NoError(t, err)
	assert.Equal(t, map[string]int{
		KindWorkCover: 2, KindWorkScreenshot: 1, KindCharacterBust: 1,
		KindCharacterFigure: 1, KindLabelLogo: 1, KindPersonPhoto: 1,
	}, kindCounts(refs))
	for _, r := range refs {
		assert.Emptyf(t, r.Label, "the full sweep skips the label joins (%s)", r.Kind)
	}
}

// TestCollectByHashCarriesLabels: the console's answer names the entity a
// human would recognize, and a work's cover is labelled by its work, not by
// the cover row.
func TestCollectByHashCarriesLabels(t *testing.T) {
	db := openTestDB(t)
	workID, charID, labelID, personID := fixture(t, db)

	refs, err := CollectByHash(context.Background(), db, hashShared)
	require.NoError(t, err)
	assert.Equal(t, map[string]int{
		KindWorkCover: 1, KindWorkScreenshot: 1, KindCharacterBust: 1,
		KindCharacterFigure: 1, KindLabelLogo: 1, KindPersonPhoto: 1,
	}, kindCounts(refs))

	byKind := map[string]Ref{}
	for _, r := range refs {
		byKind[r.Kind] = r
	}
	assert.Equal(t, workID, byKind[KindWorkCover].EntityID)
	assert.Equal(t, "作品", byKind[KindWorkCover].Label)
	assert.Equal(t, "作品", byKind[KindWorkScreenshot].Label)
	assert.Equal(t, charID, byKind[KindCharacterBust].EntityID)
	assert.Equal(t, "角色", byKind[KindCharacterFigure].Label)
	assert.Equal(t, labelID, byKind[KindLabelLogo].EntityID)
	assert.Equal(t, personID, byKind[KindPersonPhoto].EntityID)

	none, err := CollectByHash(context.Background(), db, hashDead)
	require.NoError(t, err)
	assert.Empty(t, none, "an unreferenced hash answers with an empty list, not an error")
}

// TestDistinctHashesDedupesAndSorts: refping's contract — one entry per hash,
// stable order, no empty strings from the NOT NULL DEFAULT '' columns.
func TestDistinctHashesDedupesAndSorts(t *testing.T) {
	db := openTestDB(t)
	fixture(t, db)

	hashes, err := DistinctHashes(context.Background(), db)
	require.NoError(t, err)
	assert.Equal(t, []string{hashShared, hashOther}, hashes)
}

// TestDetachReleasesEveryKind: detach empties the reference set for one hash
// and leaves every other hash untouched — the covers/screenshots by deleting
// their rows, the single-slot columns by writing their own "no image" value.
func TestDetachReleasesEveryKind(t *testing.T) {
	db := openTestDB(t)
	_, charID, labelID, personID := fixture(t, db)
	ctx := context.Background()

	removed, err := Detach(ctx, db, hashShared)
	require.NoError(t, err)
	assert.Equal(t, map[string]int64{
		KindWorkCover: 1, KindWorkScreenshot: 1, KindCharacterBust: 1,
		KindCharacterFigure: 1, KindLabelLogo: 1, KindPersonPhoto: 1,
	}, removed)

	refs, err := CollectByHash(ctx, db, hashShared)
	require.NoError(t, err)
	assert.Empty(t, refs, "nothing references the hash after a detach")

	// The other cover survived: detach is hash-scoped, not work-scoped.
	hashes, err := DistinctHashes(ctx, db)
	require.NoError(t, err)
	assert.Equal(t, []string{hashOther}, hashes)

	// The entities themselves survive — only the image slot was released.
	var char model.CatalogCharacter
	require.NoError(t, db.First(&char, charID).Error)
	assert.Nil(t, char.ImageHash)
	assert.Nil(t, char.FigureHash)
	var label model.CatalogLabel
	require.NoError(t, db.First(&label, labelID).Error)
	assert.Equal(t, "", label.LogoHash)
	var person model.CatalogPerson
	require.NoError(t, db.First(&person, personID).Error)
	assert.Equal(t, "", person.PhotoHash)

	// Detaching an unreferenced hash is a no-op, not an error.
	again, err := Detach(ctx, db, hashShared)
	require.NoError(t, err)
	for kind, n := range again {
		assert.Zerof(t, n, "second detach touched %s", kind)
	}
}
