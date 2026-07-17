package srcvndb

import (
	"fmt"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// --- hermetic COPY-unescape tests -----------------------------------------

func TestCopyUnescape(t *testing.T) {
	v, isNull := copyUnescape(`\N`)
	assert.True(t, isNull)
	assert.Equal(t, "", v)

	v, isNull = copyUnescape("plain")
	assert.False(t, isNull)
	assert.Equal(t, "plain", v)

	// Escaped newline/tab/backslash decode to their real bytes.
	v, isNull = copyUnescape(`Line1\nLine2\tX\\Y`)
	assert.False(t, isNull)
	assert.Equal(t, "Line1\nLine2\tX\\Y", v)
}

// --- integration (real Postgres, fixture dump) -----------------------------

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
	if err := EnsureSchema(db); err != nil {
		fmt.Fprintf(os.Stderr, "SKIP: ensure src_vndb schema failed: %v\n", err)
		os.Exit(0)
	}
	testDB = db
	os.Exit(m.Run())
}

func TestIngestFixtureAndIdempotency(t *testing.T) {
	report, err := Run(testDB, "testdata", "")
	require.NoError(t, err)

	// Read-row counts. images: 3 read, 1 non-ch (cv99) skipped → 2 loaded.
	assert.Equal(t, int64(2), report.PerFile["vn"].Rows)
	assert.Equal(t, int64(2), report.PerFile["chars"].Rows)
	assert.Equal(t, int64(3), report.PerFile["chars_names"].Rows)
	assert.Equal(t, int64(3), report.PerFile["chars_vns"].Rows)
	assert.Equal(t, int64(2), report.PerFile["images"].Rows)
	assert.Equal(t, int64(1), report.PerFile["images"].Skipped, "cv-prefix image dropped")

	count := func(table string) int64 {
		var n int64
		require.NoError(t, testDB.Table(table).Count(&n).Error)
		return n
	}
	assert.Equal(t, int64(2), count("src_vndb.vn"))
	assert.Equal(t, int64(2), count("src_vndb.chars"))
	assert.Equal(t, int64(2), count("src_vndb.images"), "only ch rows loaded")

	// vn: verbatim description, \N cover ids → empty, escaped newline decoded.
	var v1 VN
	require.NoError(t, testDB.First(&v1, "id = ?", "v1").Error)
	assert.Equal(t, "ja", v1.OLang)
	assert.Equal(t, "cv1", v1.Image)
	assert.Equal(t, "A test blurb about a cat god.", v1.Description)
	var v2 VN
	require.NoError(t, testDB.First(&v2, "id = ?", "v2").Error)
	assert.Equal(t, "", v2.Image, `\N cover id → empty`)
	assert.Equal(t, "Second\nline description.", v2.Description, "escaped newline decoded")

	// Numeric fields parse (the getInt16/present bug regression): c2.main_spoil=2,
	// image ch2 sexual=150 violence=200; \N → empty/zero.
	var c2 Char
	require.NoError(t, testDB.First(&c2, "id = ?", "c2").Error)
	assert.Equal(t, "c1", c2.Main, "instance_of base")
	assert.EqualValues(t, 2, c2.MainSpoil)
	assert.Equal(t, "", c2.Image, `\N image → empty`)

	var c1 Char
	require.NoError(t, testDB.First(&c1, "id = ?", "c1").Error)
	assert.Equal(t, "ch1", c1.Image)
	assert.Equal(t, "Line1\nLine2", c1.Description, "escaped newline decoded")

	var ch2 Image
	require.NoError(t, testDB.First(&ch2, "id = ?", "ch2").Error)
	assert.EqualValues(t, 150, ch2.SexualAvg)
	assert.EqualValues(t, 200, ch2.ViolenceAvg)

	// chars_names: \N latin → empty; spoil parsed on chars_vns.
	var enName CharName
	require.NoError(t, testDB.Where("id = ? AND lang = ?", "c1", "en").First(&enName).Error)
	assert.Equal(t, "", enName.Latin, `\N latin → empty`)
	var maxSpoil int16
	require.NoError(t, testDB.Raw(`SELECT max(spoil) FROM src_vndb.chars_vns`).Scan(&maxSpoil).Error)
	assert.EqualValues(t, 2, maxSpoil)

	// Re-run reproduces identical counts (whole-table replacement).
	_, err = Run(testDB, "testdata", "")
	require.NoError(t, err)
	assert.Equal(t, int64(2), count("src_vndb.chars"))
	assert.Equal(t, int64(3), count("src_vndb.chars_vns"))

	// --only reloads a single file.
	_, err = Run(testDB, "testdata", "chars")
	require.NoError(t, err)
	assert.Equal(t, int64(2), count("src_vndb.chars"))
}
