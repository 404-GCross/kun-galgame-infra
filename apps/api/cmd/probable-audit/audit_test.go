package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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

// The apply-path integration test runs against a real catalog Postgres (the
// service_test.go convention). Scan/export loaders read staging schemas
// (galgame / games / src_bangumi) that migrate-catalog does not own, so their
// classification + sampling logic is unit-tested directly and the SQL loaders
// are validated by the real-data manual run recorded in the step report.

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
		fmt.Fprintf(os.Stderr, "SKIP: catalog migration failed: %v\n", err)
		os.Exit(0)
	}
	if err := seed.Run(db); err != nil {
		fmt.Fprintf(os.Stderr, "SKIP: catalog seeding failed: %v\n", err)
		os.Exit(0)
	}
	testDB = db
	os.Exit(m.Run())
}

// --- pure logic ------------------------------------------------------------

func TestCorrobState(t *testing.T) {
	assert.Equal(t, corrobAgree, corrobState("v100", "V100"))      // case-fold equal
	assert.Equal(t, corrobContradict, corrobState("v100", "v999")) // both present, differ
	assert.Equal(t, corrobExtNoVNDB, corrobState("v100", ""))      // work has one, ext none
	assert.Equal(t, corrobWorkNoVNDB, corrobState("", "v100"))     // work has none
	assert.Equal(t, corrobWorkNoVNDB, corrobState("", ""))         // neither
}

func TestYearBucketAndExtract(t *testing.T) {
	assert.Equal(t, "0", yearBucket(2004, 2004))
	assert.Equal(t, "1", yearBucket(2004, 2005))
	assert.Equal(t, ">=2", yearBucket(2004, 2010))
	assert.Equal(t, "missing", yearBucket(0, 2004))

	assert.Equal(t, "v20632", extractVNDB("https://vndb.org/v20632"))
	assert.Equal(t, "v100", extractVNDB(" V100 "))
	assert.Equal(t, "", extractVNDB("r37334")) // a release id is not a vn id
	assert.Equal(t, "", extractVNDB(""))
	assert.Equal(t, 2004, yearPrefix("2004-04-28"))
	assert.Equal(t, 0, yearPrefix("n/a"))
}

// stratum classification is the scan's four-way judgment on already-loaded refs.
func TestStratumClassification(t *testing.T) {
	cases := []struct {
		ref  probableRef
		want string
	}{
		{probableRef{Rule: ruleRosetta, Corrob: corrobAgree}, "rosetta"},
		{probableRef{Rule: ruleRosetta, Corrob: corrobContradict}, "contradiction"},
		{probableRef{Rule: ruleTitleYear, Corrob: corrobExtNoVNDB, AlsoRosetta: true}, "ts-rosetta-corrob"},
		{probableRef{Rule: ruleTitleYear, Corrob: corrobExtNoVNDB, AlsoRosetta: false}, "ts-bangumi-only"},
		{probableRef{Rule: ruleTitleYear, Corrob: corrobContradict, AlsoRosetta: true}, "contradiction"},
		// step-29: eg-dlsite mint refs — the work is unclaimed (work-no-vndb),
		// so it is its own stratum, never the title-strict fallback.
		{probableRef{Rule: ruleEGDLsite, Corrob: corrobWorkNoVNDB}, "eg-dlsite"},
	}
	for _, c := range cases {
		assert.Equal(t, c.want, c.ref.stratum())
	}
}

func TestDeterministicSample(t *testing.T) {
	pool := make([]probableRef, 0, 1000)
	for i := range 1000 {
		pool = append(pool, probableRef{WorkID: int64(i), SourceID: sourceEG, ExternalID: fmt.Sprint(i), Rule: ruleRosetta})
	}
	a := deterministicSample(append([]probableRef(nil), pool...), 150)
	b := deterministicSample(append([]probableRef(nil), pool...), 150)
	require.Len(t, a, 150)
	require.Equal(t, keysOf(a), keysOf(b), "same pool → identical sample regardless of input order")

	// Shuffled input order must not change the chosen set.
	shuf := append([]probableRef(nil), pool...)
	shuf[0], shuf[999] = shuf[999], shuf[0]
	c := deterministicSample(shuf, 150)
	assert.Equal(t, keysOf(a), keysOf(c))

	// Fewer than n → all of them.
	small := pool[:10]
	assert.Len(t, deterministicSample(append([]probableRef(nil), small...), 150), 10)
}

func keysOf(rs []probableRef) []string {
	out := make([]string, len(rs))
	for i, r := range rs {
		out[i] = r.key()
	}
	return out
}

func TestParseReceipt(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "r.tsv")
	content := strings.Join([]string{
		strings.Join(tsvHeader, "\t"),
		row("rosetta", 5, "1234", 42, "OK", "looks right"), // mixed case decision
		"", // blank line ignored
		row("ts-bangumi-only", 3, "55", 7, "wrong", "homonym"),
	}, "\n")
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))

	rows, err := parseReceipt(path)
	require.NoError(t, err)
	require.Len(t, rows, 2)
	assert.Equal(t, "ok", rows[0].Decision) // lower-cased
	assert.Equal(t, int16(5), rows[0].SourceID)
	assert.Equal(t, int64(42), rows[0].WorkID)
	assert.Equal(t, "1234", rows[0].ExternalID)
	assert.Equal(t, "wrong", rows[1].Decision)
	assert.Equal(t, "homonym", rows[1].Notes)

	// Missing a required column is a hard error.
	bad := filepath.Join(dir, "bad.tsv")
	require.NoError(t, os.WriteFile(bad, []byte("stratum\tdecision\nx\tok\n"), 0o644))
	_, err = parseReceipt(bad)
	require.Error(t, err)
}

// --- apply path (real catalog DB) ------------------------------------------

func cleanCatalog(t *testing.T) {
	t.Helper()
	require.NoError(t, testDB.Exec(
		`TRUNCATE catalog_external_ref, catalog_match_rejection, catalog_work, catalog_revision RESTART IDENTITY CASCADE`).Error)
}

func seedWork(t *testing.T, id int64) {
	t.Helper()
	require.NoError(t, testDB.Exec(`INSERT INTO catalog_work
		(id, medium_id, olang, display_name, content_rating, status, extra, field_provenance, display_nsfw)
		VALUES (?, 1, 'ja', 'W', 0, 0, '{}', '{}', false)`, id).Error)
}

func seedProbable(t *testing.T, workID int64, source int16, ext string) {
	t.Helper()
	require.NoError(t, testDB.Exec(`INSERT INTO catalog_external_ref
		(entity_type, entity_id, source_id, external_id, link_kind, matched_by)
		VALUES (5, ?, ?, ?, 1, 'rule:eg-vndb-rosetta')`, workID, source, ext).Error)
}

func linkKindOf(t *testing.T, workID int64, source int16, ext string) *int16 {
	t.Helper()
	var k *int16
	require.NoError(t, testDB.Raw(
		`SELECT link_kind FROM catalog_external_ref WHERE entity_type=5 AND entity_id=? AND source_id=? AND external_id=?`,
		workID, source, ext).Scan(&k).Error)
	return k
}

func writeReceipt(t *testing.T, lines ...string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "receipt.tsv")
	body := append([]string{strings.Join(tsvHeader, "\t")}, lines...)
	require.NoError(t, os.WriteFile(path, []byte(strings.Join(body, "\n")+"\n"), 0o644))
	return path
}

// row builds a minimal receipt line addressing (source, ext, work) with a verdict.
func row(stratum string, source int16, ext string, work int64, decision, notes string) string {
	f := make([]string, len(tsvHeader))
	set := func(name, v string) {
		for i, h := range tsvHeader {
			if h == name {
				f[i] = v
			}
		}
	}
	set("stratum", stratum)
	set("source_id", fmt.Sprint(source))
	set("external_id", ext)
	set("work_id", fmt.Sprint(work))
	set("decision", decision)
	set("notes", notes)
	return strings.Join(f, "\t")
}

// The receipt's ok/wrong verdicts are exactly equivalent to the admin bucket:
// ok promotes probable→exact (verified), wrong deletes the ref and records
// permanent negative knowledge; unsure and blank do nothing. Dry-run writes
// nothing; a re-run is idempotent.
func TestApplyReceiptEquivalence(t *testing.T) {
	if testDB == nil {
		t.Skip("no catalog test DB")
	}
	cleanCatalog(t)
	ctx := context.Background()
	a := &auditor{catalog: testDB}

	seedWork(t, 10)
	seedWork(t, 20)
	seedWork(t, 30)
	seedProbable(t, 10, sourceEG, "111")      // → ok
	seedProbable(t, 20, sourceBangumi, "222") // → wrong
	seedProbable(t, 30, sourceEG, "333")      // → unsure

	receipt := writeReceipt(t,
		row("rosetta", sourceEG, "111", 10, "ok", ""),
		row("ts-bangumi-only", sourceBangumi, "222", 20, "wrong", "homonym, different game"),
		row("rosetta", sourceEG, "333", 30, "unsure", ""),
	)

	// Dry-run writes nothing.
	require.NoError(t, a.runApply(ctx, receipt, false, 9))
	assert.Equal(t, int16(1), *linkKindOf(t, 10, sourceEG, "111"), "dry leaves it probable")

	// --run applies.
	require.NoError(t, a.runApply(ctx, receipt, true, 9))

	// ok → exact + verified bookkeeping (the ConfirmRef contract).
	var okRef model.CatalogExternalRef
	require.NoError(t, testDB.Where("entity_id=? AND source_id=? AND external_id=?", 10, sourceEG, "111").First(&okRef).Error)
	assert.Equal(t, model.LinkKindExact, okRef.LinkKind)
	require.NotNil(t, okRef.VerifiedBy)
	assert.Equal(t, int64(9), *okRef.VerifiedBy)

	// wrong → ref gone + permanent negative knowledge with a reason (the 21 tie).
	assert.Nil(t, linkKindOf(t, 20, sourceBangumi, "222"), "wrong ref deleted")
	var rej model.CatalogMatchRejection
	require.NoError(t, testDB.Where("entity_id=? AND source_id=? AND external_id=?", 20, sourceBangumi, "222").First(&rej).Error)
	assert.Contains(t, rej.Reason, "homonym")
	require.NotNil(t, rej.RejectedBy)

	// unsure → untouched.
	assert.Equal(t, int16(1), *linkKindOf(t, 30, sourceEG, "333"))

	// Idempotent: a second --run over the same receipt errors on nothing.
	require.NoError(t, a.runApply(ctx, receipt, true, 9))
	assert.Equal(t, model.LinkKindExact, *linkKindOf(t, 10, sourceEG, "111"))
	assert.Nil(t, linkKindOf(t, 20, sourceBangumi, "222"))
}
