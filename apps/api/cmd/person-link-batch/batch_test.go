package main

import (
	"context"
	"fmt"
	"io"
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

// --- fold / classify (pure) ------------------------------------------------

func TestFoldAndAliases(t *testing.T) {
	assert.Equal(t, "test", foldName(" Te st (歌手) "))
	assert.Equal(t, "緒方剛志", foldName("緒方剛志(ぼうのうと)"))
	assert.Equal(t, []string{"ささきむつみ"}, parenAliases("藤宮博也(ささきむつみ)"))
	assert.Equal(t, []string{"今井楓人", "野村美月"}, parenAliases("村中志帆(今井楓人、野村美月)"))
	assert.Nil(t, parenAliases("no parens here"))
}

func TestClassify(t *testing.T) {
	cases := []struct {
		a, b, want string
	}{
		{"水樹奈々", "水樹奈々", ruleA1},                      // literal identical
		{"緒方剛志", "緒方剛志(ぼうのうと)", ruleA1},               // identical after dropping parens
		{"ささきむつみ", "藤宮博也(ささきむつみ)", ruleA2},            // paren alias names the other
		{"野村美月", "村中志帆(今井楓人、野村美月)", ruleA2},           // 、-separated alias list
		{"FAVORITE", "有限会社フェイバリット(有限会社FAVORITE)", ""}, // substring, NOT whole name → miss
		{"riya", "eufonius", ""}, // true pen name / unit → miss
		{"", "", ""},             // empty is never a match
	}
	for _, c := range cases {
		assert.Equalf(t, c.want, classify(c.a, c.b), "%q ↔ %q", c.a, c.b)
	}
}

// --- batch dispatch (real catalog DB) --------------------------------------

func cleanCatalog(t *testing.T) {
	t.Helper()
	require.NoError(t, testDB.Exec(
		`TRUNCATE catalog_match_candidate, catalog_credit_name, catalog_person, catalog_revision RESTART IDENTITY CASCADE`).Error)
}

func mkName(t *testing.T, name string) int64 {
	t.Helper()
	n := &model.CatalogCreditName{Name: name, Kind: model.CreditNameKindMain, LinkVisibility: model.LinkVisibilityPublic}
	require.NoError(t, testDB.Create(n).Error)
	return n.ID
}

func mkCandidate(t *testing.T, a, b int64) {
	t.Helper()
	lo, hi := a, b
	if lo > hi {
		lo, hi = hi, lo
	}
	require.NoError(t, testDB.Create(&model.CatalogMatchCandidate{
		EntityType: model.EntityTypeCreditName, AID: lo, BID: hi,
		Reason: model.CandidateReasonSharedExternalID, Status: model.CandidateStatusPending,
	}).Error)
}

func personIDOf(t *testing.T, nameID int64) *int64 {
	t.Helper()
	var n model.CatalogCreditName
	require.NoError(t, testDB.First(&n, nameID).Error)
	return n.PersonID
}

func candStatus(t *testing.T, a, b int64) int16 {
	t.Helper()
	var s int16
	require.NoError(t, testDB.Model(&model.CatalogMatchCandidate{}).
		Where("entity_type=1 AND a_id=? AND b_id=?", a, b).Select("status").Scan(&s).Error)
	return s
}

// A matching candidate is linked through DecideCandidate; a non-matching one is
// left untouched. Dry writes nothing; --run is idempotent; the auto-built link
// is fully reversible via DetachName.
func TestBatchLinksMechanicalOnly(t *testing.T) {
	if testDB == nil {
		t.Skip("no catalog test DB")
	}
	cleanCatalog(t)
	ctx := context.Background()

	a := mkName(t, "緒方剛志")
	b := mkName(t, "緒方剛志(ぼうのうと)") // A1 with a
	c := mkName(t, "riya")
	d := mkName(t, "eufonius") // no rule → untouched
	mkCandidate(t, a, b)
	mkCandidate(t, c, d)

	// Dry: classifies but writes nothing.
	st, err := run(ctx, testDB, io.Discard, 7, false, ruleSetShared)
	require.NoError(t, err)
	assert.Equal(t, 1, st.A1Hits)
	assert.Equal(t, 1, st.Unmatched)
	assert.Nil(t, personIDOf(t, a), "dry writes nothing")

	// Apply: the A1 pair links, the unmatched pair stays pending & orphan.
	st, err = run(ctx, testDB, io.Discard, 7, true, ruleSetShared)
	require.NoError(t, err)
	assert.Equal(t, 1, st.LinkedCreated)
	pa, pb := personIDOf(t, a), personIDOf(t, b)
	require.NotNil(t, pa)
	require.NotNil(t, pb)
	assert.Equal(t, *pa, *pb, "both names share one person")
	assert.Equal(t, model.CandidateStatusAccepted, candStatus(t, a, b))
	assert.Nil(t, personIDOf(t, c), "non-matching pair untouched")
	assert.Nil(t, personIDOf(t, d))
	assert.Equal(t, model.CandidateStatusPending, candStatus(t, c, d), "remaining candidate stays in the human queue")

	// Idempotent: a second --run sees only the still-pending unmatched pair
	// (the accepted candidate is filtered out at load), so it links nothing new.
	st, err = run(ctx, testDB, io.Discard, 7, true, ruleSetShared)
	require.NoError(t, err)
	assert.Zero(t, st.A1Hits+st.A2Hits+st.LinkedCreated+st.Errors, "re-run links nothing new")
	assert.Equal(t, 1, st.Unmatched)
	assert.Equal(t, model.CandidateStatusAccepted, candStatus(t, a, b), "the earlier link is intact")

	// Reversible: detaching both names removes the auto-built person.
	q := adminQueue(testDB)
	actor := int64(7)
	require.NoError(t, q.DetachName(ctx, a, &actor))
	require.NoError(t, q.DetachName(ctx, b, &actor))
	assert.Nil(t, personIDOf(t, a))
	assert.Nil(t, personIDOf(t, b))
	var persons int64
	require.NoError(t, testDB.Model(&model.CatalogPerson{}).Count(&persons).Error)
	assert.Zero(t, persons, "empty auto-linked person hard-deleted")
}

// The three-state rule flows through the batch: a name already on a different
// person yields needs_manual, never a force-merge.
func TestBatchNeedsManual(t *testing.T) {
	if testDB == nil {
		t.Skip("no catalog test DB")
	}
	cleanCatalog(t)
	ctx := context.Background()

	p1 := &model.CatalogPerson{DisplayName: "P1", FieldProvenance: []byte(`{}`)}
	p2 := &model.CatalogPerson{DisplayName: "P2", FieldProvenance: []byte(`{}`)}
	require.NoError(t, testDB.Create(p1).Error)
	require.NoError(t, testDB.Create(p2).Error)
	a := mkName(t, "藤宮博也(ささきむつみ)")
	b := mkName(t, "ささきむつみ") // A2 with a
	require.NoError(t, testDB.Model(&model.CatalogCreditName{}).Where("id=?", a).Update("person_id", p1.ID).Error)
	require.NoError(t, testDB.Model(&model.CatalogCreditName{}).Where("id=?", b).Update("person_id", p2.ID).Error)
	mkCandidate(t, a, b)

	st, err := run(ctx, testDB, io.Discard, 7, true, ruleSetShared)
	require.NoError(t, err)
	assert.Equal(t, 1, st.A2Hits)
	assert.Equal(t, 1, st.NeedsManual)
	assert.Zero(t, st.LinkedCreated+st.LinkedAttached, "different persons are never force-merged")
	lo, hi := a, b
	if lo > hi {
		lo, hi = hi, lo
	}
	assert.Equal(t, model.CandidateStatusNeedsManual, candStatus(t, lo, hi))
}
