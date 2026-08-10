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

var testDB *gorm.DB

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
	//
	// DROP first, deliberately. getchumedia stands in for item_images too, with
	// a DIFFERENT column set, and both share one test database: CREATE TABLE IF
	// NOT EXISTS would silently inherit whichever package ran earlier and then
	// fail on the columns it lacks. Owning the fixture outright makes the suite
	// independent of package order. Safe because DB-backed suites run with -p 1.
	for _, ddl := range []string{
		`DROP TABLE IF EXISTS item_characters`,
		`DROP TABLE IF EXISTS item_images`,
		`CREATE TABLE item_characters (
			getchu_id text NOT NULL, ordinal int NOT NULL, name text NOT NULL,
			nameplate_url text, portrait_url text, PRIMARY KEY (getchu_id, ordinal))`,
		`CREATE TABLE item_images (
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

func requireDB(t *testing.T) {
	t.Helper()
	if testDB == nil {
		dbtest.Skip(t)
	}
}

func reset(t *testing.T) {
	t.Helper()
	require.NoError(t, testDB.Exec(`TRUNCATE item_characters, item_images`).Error)
	require.NoError(t, testDB.Exec(`TRUNCATE catalog_character CASCADE`).Error)
}

func mkChar(t *testing.T, name string, hash *string) int64 {
	t.Helper()
	c := model.CatalogCharacter{DisplayName: name, ImageHash: hash}
	require.NoError(t, testDB.Create(&c).Error)
	return c.ID
}

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

func TestSelectCandidatesSkipsCharactersThatAlreadyHaveAPortrait(t *testing.T) {
	requireDB(t)
	reset(t)
	existing := "deadbeef"
	withArt := mkChar(t, "有图", &existing)
	without := mkChar(t, "无图", nil)
	mkPlate(t, "100", 1, "有图", "https://g/brandnew/100/c100chara1.jpg", true)
	mkPlate(t, "100", 2, "无图", "https://g/brandnew/100/c100chara2.jpg", true)

	st := &Stats{}
	got, err := selectCandidates(context.Background(), testDB, testDB, SlotBust,
		[]getchuchars.Candidate{cand(withArt, ed("100", 1)), cand(without, ed("100", 2))}, st)
	require.NoError(t, err)

	require.Len(t, got, 1)
	assert.Equal(t, without, got[0].CharacterID)
	assert.Equal(t, "c100chara2.jpg", got[0].File)
	assert.Equal(t, 1, st.SkipHasImage)
	assert.Equal(t, 0, st.NoImage)
}

func TestSelectCandidatesDoesNotRequireMirroredBytes(t *testing.T) {
	requireDB(t)
	reset(t)
	id := mkChar(t, "未镜像", nil)
	mkPlate(t, "100", 1, "未镜像", "https://g/brandnew/100/c100chara1.jpg", false)

	st := &Stats{}
	got, err := selectCandidates(context.Background(), testDB, testDB, SlotBust,
		[]getchuchars.Candidate{cand(id, ed("100", 1))}, st)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "c100chara1.jpg", got[0].File)
	assert.Equal(t, 0, st.NoImage)
}

func TestSelectCandidatesCountsGenuinelyArtlessCharacters(t *testing.T) {
	requireDB(t)
	reset(t)
	id := mkChar(t, "无立绘", nil)
	mkPlate(t, "100", 1, "无立绘", "", false)

	st := &Stats{}
	got, err := selectCandidates(context.Background(), testDB, testDB, SlotBust,
		[]getchuchars.Candidate{cand(id, ed("100", 1))}, st)
	require.NoError(t, err)
	assert.Empty(t, got)
	assert.Equal(t, 1, st.NoImage)
}

func TestSelectCandidatesFallsBackAcrossEditions(t *testing.T) {
	requireDB(t)
	reset(t)
	id := mkChar(t, "九條都", nil)
	mkPlate(t, "limited", 7, "九條都", "", false)
	mkPlate(t, "regular", 3, "九條都", "https://g/brandnew/regular/cregularchara3.jpg", true)

	st := &Stats{}
	got, err := selectCandidates(context.Background(), testDB, testDB, SlotBust,
		[]getchuchars.Candidate{cand(id, ed("limited", 7), ed("regular", 3))}, st)
	require.NoError(t, err)

	require.Len(t, got, 1)
	assert.Equal(t, "regular", got[0].GetchuID)
	assert.Equal(t, "cregularchara3.jpg", got[0].File)
	assert.Equal(t, 0, st.NoImage)
}

func TestSelectCandidatesIgnoresDeletedCharacters(t *testing.T) {
	requireDB(t)
	reset(t)
	id := mkChar(t, "已删", nil)
	require.NoError(t, testDB.Exec(`UPDATE catalog_character SET deleted_at = now() WHERE id = ?`, id).Error)
	mkPlate(t, "100", 1, "已删", "https://g/brandnew/100/c100chara1.jpg", true)

	st := &Stats{}
	got, err := selectCandidates(context.Background(), testDB, testDB, SlotBust,
		[]getchuchars.Candidate{cand(id, ed("100", 1))}, st)
	require.NoError(t, err)
	assert.Empty(t, got)
	assert.Equal(t, 1, st.SkipHasImage)
}

func TestLoadNameplatesIgnoresOtherKinds(t *testing.T) {
	requireDB(t)
	reset(t)
	url := "https://g/brandnew/100/c100charab1.jpg"
	require.NoError(t, testDB.Exec(
		`INSERT INTO item_characters (getchu_id, ordinal, name, nameplate_url) VALUES ('100',1,'x',?)`, url).Error)
	require.NoError(t, testDB.Exec(
		`INSERT INTO item_images (getchu_id, kind, ordinal, url, local_path) VALUES ('100','portrait',1,?,'/m/x.jpg')`, url).Error)

	got, err := loadPlates(context.Background(), testDB, SlotBust)
	require.NoError(t, err)
	assert.Empty(t, got, "a full-body portrait must not be picked up as a bust")
}

func mkFigure(t *testing.T, getchuID string, ordinal int, name, url string) {
	t.Helper()
	require.NoError(t, testDB.Exec(
		`INSERT INTO item_characters (getchu_id, ordinal, name, portrait_url) VALUES (?,?,?,?)
		 ON CONFLICT (getchu_id, ordinal) DO UPDATE SET portrait_url = EXCLUDED.portrait_url`,
		getchuID, ordinal, name, url).Error)
	require.NoError(t, testDB.Exec(
		`INSERT INTO item_images (getchu_id, kind, ordinal, url, local_path) VALUES (?,?,?,?,?)`,
		getchuID, "portrait", ordinal, url, "/crawler/mirror/"+getchuID+"/y.jpg").Error)
}

func TestSlotsAreIndependent(t *testing.T) {
	requireDB(t)
	reset(t)
	bust := "deadbeef"
	id := mkChar(t, "九條都", &bust)
	mkPlate(t, "100", 1, "九條都", "https://g/brandnew/100/c100chara1.jpg", true)
	mkFigure(t, "100", 1, "九條都", "https://g/brandnew/100/c100charab1.jpg")

	c := []getchuchars.Candidate{cand(id, ed("100", 1))}

	bustSt := &Stats{}
	gotBust, err := selectCandidates(context.Background(), testDB, testDB, SlotBust, c, bustSt)
	require.NoError(t, err)
	assert.Empty(t, gotBust, "the bust slot is already filled")
	assert.Equal(t, 1, bustSt.SkipHasImage)

	figSt := &Stats{}
	gotFig, err := selectCandidates(context.Background(), testDB, testDB, SlotFigure, c, figSt)
	require.NoError(t, err)
	require.Len(t, gotFig, 1, "the figure slot is empty and the page offers one")
	assert.Equal(t, "c100charab1.jpg", gotFig[0].File, "must take the full-body file, not the bust")
	assert.Equal(t, 0, figSt.SkipHasImage)
}

func TestFigureSlotDoesNotFallBackToTheBust(t *testing.T) {
	requireDB(t)
	reset(t)
	id := mkChar(t, "只有胸像", nil)
	mkPlate(t, "100", 1, "只有胸像", "https://g/brandnew/100/c100chara1.jpg", true)

	st := &Stats{}
	got, err := selectCandidates(context.Background(), testDB, testDB, SlotFigure,
		[]getchuchars.Candidate{cand(id, ed("100", 1))}, st)
	require.NoError(t, err)
	assert.Empty(t, got)
	assert.Equal(t, 1, st.NoImage)
}

func TestParseSlotRefusesToGuess(t *testing.T) {
	for _, bad := range []string{"", "portrait", "nameplate", "Bust"} {
		if _, err := ParseSlot(bad); err == nil {
			t.Errorf("ParseSlot(%q) accepted; the two slots write different columns and must not be guessed", bad)
		}
	}
	b, err := ParseSlot("bust")
	require.NoError(t, err)
	assert.Equal(t, "image_hash", b.TargetColumn)
	assert.Equal(t, "nameplate_url", b.StagingColumn)
	f, err := ParseSlot("figure")
	require.NoError(t, err)
	assert.Equal(t, "figure_hash", f.TargetColumn)
	assert.Equal(t, "portrait_url", f.StagingColumn)
}
