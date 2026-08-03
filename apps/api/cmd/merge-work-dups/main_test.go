package main

import (
	"context"
	"fmt"
	"os"
	"testing"

	"api/internal/platform/catalog/migrate"
	"api/internal/platform/catalog/model"
	"api/internal/platform/catalog/seed"
	"api/internal/testsupport/dbtest"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

var mergeTestDB *gorm.DB

func TestMain(m *testing.M) {
	dsn, ok := dbtest.DSN()
	if !ok {
		dbtest.SkipMain("merge-work-dups suite")
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		fmt.Fprintln(os.Stderr, "FAIL: cannot connect to the assigned test database")
		os.Exit(1)
	}
	if err := migrate.Run(db); err != nil {
		fmt.Fprintf(os.Stderr, "FAIL: catalog migration failed: %v\n", err)
		os.Exit(1)
	}
	if err := seed.Run(db); err != nil {
		fmt.Fprintf(os.Stderr, "FAIL: catalog seeding failed: %v\n", err)
		os.Exit(1)
	}
	mergeTestDB = db
	os.Exit(m.Run())
}

func TestSanityAcceptsKungalTargetAndBodylessSource(t *testing.T) {
	row := seedSanityCase(t, stringPointer("kungal"), nil)
	if err := sanity(context.Background(), mergeTestDB, row); err != nil {
		t.Fatalf("sanity rejected a kungal target with a bodyless source: %v", err)
	}
}

func TestSanityRejectsNonKungalTargets(t *testing.T) {
	for _, tc := range []struct {
		name string
		site *string
	}{
		{name: "former site", site: stringPointer("galgame_wiki")},
		{name: "missing site", site: nil},
		{name: "foreign site", site: stringPointer("moyu")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			row := seedSanityCase(t, tc.site, nil)
			err := sanity(context.Background(), mergeTestDB, row)
			if err == nil || err.Error() != "target not a claimed kungal work" {
				t.Fatalf("sanity error = %v, want target not a claimed kungal work", err)
			}
		})
	}
}

func TestSanityStillRejectsClaimedSource(t *testing.T) {
	row := seedSanityCase(t, stringPointer("kungal"), stringPointer("kungal"))
	err := sanity(context.Background(), mergeTestDB, row)
	if err == nil || err.Error() != "source is claimed (kungal) — refusing" {
		t.Fatalf("sanity error = %v, want claimed-source rejection", err)
	}
}

func seedSanityCase(t *testing.T, targetSite, sourceSite *string) tsvRow {
	t.Helper()
	if err := mergeTestDB.Exec(`TRUNCATE catalog_external_ref, catalog_work RESTART IDENTITY CASCADE`).Error; err != nil {
		t.Fatalf("truncate merge fixtures: %v", err)
	}
	var medium int16
	if err := mergeTestDB.Raw(`SELECT id FROM catalog_medium WHERE key = 'galgame'`).Scan(&medium).Error; err != nil {
		t.Fatalf("resolve galgame medium: %v", err)
	}
	if medium == 0 {
		t.Fatal("galgame medium is not seeded")
	}
	target := createSanityWork(t, medium, "target", targetSite, 81001)
	source := createSanityWork(t, medium, "source", sourceSite, 81002)
	const bid = "123456"
	ref := model.CatalogExternalRef{
		EntityType: model.EntityTypeWork,
		EntityID:   source,
		SourceID:   3,
		ExternalID: bid,
		LinkKind:   model.LinkKindExact,
		MatchedBy:  "test:w165",
	}
	if err := mergeTestDB.Create(&ref).Error; err != nil {
		t.Fatalf("seed source anchor: %v", err)
	}
	return tsvRow{targetWork: target, sourceWork: source, correctBid: bid}
}

func createSanityWork(t *testing.T, medium int16, name string, site *string, productID int64) int64 {
	t.Helper()
	var productWorkID *int64
	if site != nil {
		productWorkID = &productID
	}
	work := model.CatalogWork{
		MediumID:      medium,
		Site:          site,
		ProductWorkID: productWorkID,
		OLang:         "ja",
		DisplayName:   name,
		ContentRating: model.ContentRatingAllAges,
		Status:        model.WorkStatusLive,
	}
	if err := mergeTestDB.Create(&work).Error; err != nil {
		t.Fatalf("create %s work: %v", name, err)
	}
	return work.ID
}

func stringPointer(value string) *string { return &value }
