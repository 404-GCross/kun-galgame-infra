package labellogos

// The candidate / audit queries are raw SQL against the catalog schema, so the
// only way to know they are right is to run them. Like the catalog refping
// test, this file is gated on TEST_CATALOG_DATABASE_DSN and skips when unset (a
// bare `go test ./...` still passes). Point it at a THROWAWAY database — it
// truncates the tables it touches. Never at kun_catalog.
//
//	TEST_CATALOG_DATABASE_DSN="host=127.0.0.1 port=... user=... dbname=throwaway sslmode=disable" \
//	  go test ./internal/jobs/labellogos/ -run TestDB

import (
	"context"
	"encoding/csv"
	"os"
	"path/filepath"
	"testing"

	"api/internal/platform/catalog/model"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

const (
	testBangumiSource int16 = 3
	testCienSource    int16 = 14
)

// openTestDB migrates the tables these queries touch and wipes them clean.
func openTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := os.Getenv("TEST_CATALOG_DATABASE_DSN")
	if dsn == "" {
		t.Skip("TEST_CATALOG_DATABASE_DSN not set")
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: gormlogger.Default.LogMode(gormlogger.Silent)})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(
		&model.CatalogSource{}, &model.CatalogOrg{}, &model.CatalogLabel{}, &model.CatalogExternalRef{}))
	require.NoError(t, db.Exec(`TRUNCATE catalog_label, catalog_external_ref RESTART IDENTITY CASCADE`).Error)
	require.NoError(t, db.Exec(
		`INSERT INTO catalog_source (id, key, trust_tier) VALUES (?,'bangumi',1), (?,'cien',2)
		 ON CONFLICT (id) DO NOTHING`, testBangumiSource, testCienSource).Error)
	return db
}

// mkLabel creates a label and returns its id.
func mkLabel(t *testing.T, db *gorm.DB, name, logo string) int64 {
	t.Helper()
	l := &model.CatalogLabel{DisplayName: name, LogoHash: logo}
	require.NoError(t, db.Create(l).Error)
	return l.ID
}

func mkAnchor(t *testing.T, db *gorm.DB, labelID int64, source int16, extID string, kind int16) {
	t.Helper()
	require.NoError(t, db.Create(&model.CatalogExternalRef{
		EntityType: model.EntityTypeLabel, EntityID: labelID, SourceID: source,
		ExternalID: extID, LinkKind: kind, MatchedBy: "import:test",
	}).Error)
}

func TestDBLoadCandidates(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	want := mkLabel(t, db, "wants a logo", "")
	mkAnchor(t, db, want, testBangumiSource, "100", model.LinkKindExact)

	filled := mkLabel(t, db, "already has one", "deadbeef")
	mkAnchor(t, db, filled, testBangumiSource, "101", model.LinkKindExact)

	probable := mkLabel(t, db, "probable anchor only", "")
	mkAnchor(t, db, probable, testBangumiSource, "102", model.LinkKindProbable)

	otherSource := mkLabel(t, db, "cien only", "")
	mkAnchor(t, db, otherSource, testCienSource, "200", model.LinkKindExact)

	unanchored := mkLabel(t, db, "no anchor at all", "")
	_ = unanchored

	deleted := mkLabel(t, db, "soft deleted", "")
	mkAnchor(t, db, deleted, testBangumiSource, "103", model.LinkKindExact)
	require.NoError(t, db.Delete(&model.CatalogLabel{}, deleted).Error)

	// Two exact anchors on one label: DISTINCT ON keeps the lowest external id,
	// so chunking stays one-row-per-label.
	twice := mkLabel(t, db, "double anchored", "")
	mkAnchor(t, db, twice, testBangumiSource, "301", model.LinkKindExact)
	mkAnchor(t, db, twice, testBangumiSource, "300", model.LinkKindExact)

	got, err := loadCandidates(ctx, db, testBangumiSource)
	require.NoError(t, err)

	byID := map[int64]string{}
	for _, c := range got {
		byID[c.LabelID] = c.ExternalID
	}
	assert.Equal(t, map[int64]string{want: "100", twice: "300"}, byID,
		"only live, logo-less labels with an EXACT anchor for THIS source, one row each")

	// The cien lane sees its own anchor and nothing else.
	gotCien, err := loadCandidates(ctx, db, testCienSource)
	require.NoError(t, err)
	require.Len(t, gotCien, 1)
	assert.Equal(t, otherSource, gotCien[0].LabelID)

	// Provenance travels with the candidate so the write path can merge it.
	require.NoError(t, db.Exec(
		`UPDATE catalog_label SET field_provenance = '{"display_name":[{"source":"vndb","at":"2020-01-01T00:00:00Z"}]}'::jsonb WHERE id = ?`,
		want).Error)
	got, err = loadCandidates(ctx, db, testBangumiSource)
	require.NoError(t, err)
	for _, c := range got {
		if c.LabelID == want {
			assert.Contains(t, string(c.FieldProvenance), "vndb")
		}
	}
}

// TestDBCollectAudit pins the falsification set: ONLY labels carrying an exact
// anchor to BOTH sources, which are the only rows where the bangumi > cien
// precedence decides anything.
func TestDBCollectAudit(t *testing.T) {
	db := openTestDB(t)

	both := mkLabel(t, db, "dual anchored", "")
	mkAnchor(t, db, both, testBangumiSource, "400", model.LinkKindExact)
	mkAnchor(t, db, both, testCienSource, "500", model.LinkKindExact)

	bangumiOnly := mkLabel(t, db, "bangumi only", "")
	mkAnchor(t, db, bangumiOnly, testBangumiSource, "401", model.LinkKindExact)

	mixedKind := mkLabel(t, db, "exact + probable", "")
	mkAnchor(t, db, mixedKind, testBangumiSource, "402", model.LinkKindExact)
	mkAnchor(t, db, mixedKind, testCienSource, "502", model.LinkKindProbable)

	gone := mkLabel(t, db, "dual but deleted", "")
	mkAnchor(t, db, gone, testBangumiSource, "403", model.LinkKindExact)
	mkAnchor(t, db, gone, testCienSource, "503", model.LinkKindExact)
	require.NoError(t, db.Delete(&model.CatalogLabel{}, gone).Error)

	pairs, err := collectAudit(context.Background(), db, registry{bangumi: testBangumiSource, cien: testCienSource})
	require.NoError(t, err)
	require.Len(t, pairs, 1)
	assert.Equal(t, both, pairs[0].LabelID)
	assert.Equal(t, "400", pairs[0].BangumiID)
	assert.Equal(t, "500", pairs[0].CienID)

	out := filepath.Join(t.TempDir(), "audit.csv")
	require.NoError(t, writeAudit(out, pairs))
	f, err := os.Open(out)
	require.NoError(t, err)
	defer f.Close()
	rows, err := csv.NewReader(f).ReadAll()
	require.NoError(t, err)
	require.Len(t, rows, 2)
	assert.Equal(t, []string{"label_id", "display_name", "bangumi_id", "cien_id", "logo_hash"}, rows[0])
}

// TestDBFillWritesLogoAndProvenance exercises the real UPDATE: the hash lands,
// provenance is merged under logo_hash without disturbing other fields, and a
// second lane cannot overwrite the first (the whole precedence guarantee).
func TestDBFillWritesLogoAndProvenance(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	id := mkLabel(t, db, "brand", "")
	require.NoError(t, db.Exec(
		`UPDATE catalog_label SET field_provenance = '{"display_name":[{"source":"vndb","at":"2020-01-01T00:00:00Z"}]}'::jsonb WHERE id = ?`,
		id).Error)

	root := t.TempDir()
	writeFile(t, filepath.Join(root, "600", "logo.png"))
	m, err := loadMirror(root, SourceBangumi)
	require.NoError(t, err)

	cands, err := loadCandidates(ctx, db, testBangumiSource)
	require.NoError(t, err)
	require.Empty(t, cands, "no anchor yet")
	mkAnchor(t, db, id, testBangumiSource, "600", model.LinkKindExact)
	cands, err = loadCandidates(ctx, db, testBangumiSource)
	require.NoError(t, err)
	require.Len(t, cands, 1)

	fake := &fakeUploader{hash: "1111111111111111111111111111111111111111111111111111111111111111"}
	r := &runner{db: db, cli: fake, source: SourceBangumi, mirror: m, stats: &Stats{}}
	res := r.fill(ctx, cands[0], true)
	assert.Equal(t, 1, res.uploaded)
	assert.Equal(t, fake.hash, res.hash)

	var row struct {
		LogoHash        string `gorm:"column:logo_hash"`
		FieldProvenance string `gorm:"column:field_provenance"`
	}
	require.NoError(t, db.Raw(`SELECT logo_hash, field_provenance::text AS field_provenance FROM catalog_label WHERE id = ?`, id).Scan(&row).Error)
	assert.Equal(t, fake.hash, row.LogoHash)
	assert.Contains(t, row.FieldProvenance, `"bangumi"`)
	assert.Contains(t, row.FieldProvenance, `"vndb"`, "other fields' provenance must survive")

	// A cien pass arriving later must NOT overwrite the bangumi logo. Replay the
	// stale candidate (as a racing run would hold it) through the cien lane.
	rootC := t.TempDir()
	writeFile(t, filepath.Join(rootC, "600", "avatar.png"))
	mc, err := loadMirror(rootC, SourceCien)
	require.NoError(t, err)
	rc := &runner{db: db, cli: &fakeUploader{hash: "2222222222222222222222222222222222222222222222222222222222222222"},
		source: SourceCien, mirror: mc, stats: &Stats{}}
	res = rc.fill(ctx, cands[0], true)
	assert.Equal(t, 1, res.raced, "losing the race is an ordinary outcome, not an error")

	require.NoError(t, db.Raw(`SELECT logo_hash, field_provenance::text AS field_provenance FROM catalog_label WHERE id = ?`, id).Scan(&row).Error)
	assert.Equal(t, fake.hash, row.LogoHash, "the first writer keeps the slot")

	// And the label is no longer a candidate for either lane.
	cands, err = loadCandidates(ctx, db, testBangumiSource)
	require.NoError(t, err)
	assert.Empty(t, cands, "a filled label is skipped before any byte read")
}
