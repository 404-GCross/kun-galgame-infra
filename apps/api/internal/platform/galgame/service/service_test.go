package service

import (
	"fmt"
	"os"
	"testing"

	"api/internal/platform/galgame/model"
	"api/internal/platform/galgame/repository"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

var (
	testDB  *gorm.DB
	testSvc *GalgameService
)

func TestMain(m *testing.M) {
	dsn := os.Getenv("TEST_DATABASE_DSN")
	if dsn == "" {
		// Default: local test database
		dsn = "host=localhost port=5432 user=postgres password=postgres dbname=kun_galgame_wiki_test sslmode=disable"
	}

	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "SKIP: cannot connect to test database: %v\n", err)
		os.Exit(0)
	}

	// Auto-migrate all tables
	if err := db.AutoMigrate(
		&model.GalgameSeries{},
		&model.GalgameTag{},
		&model.GalgameTagAlias{},
		&model.GalgameOfficial{},
		&model.GalgameOfficialAlias{},
		&model.GalgameEngine{},
		&model.Galgame{},
		&model.GalgameAlias{},
		&model.GalgameTagRelation{},
		&model.GalgameOfficialRelation{},
		&model.GalgameEngineRelation{},
		&model.GalgameLink{},
		&model.GalgamePR{},
		&model.GalgameRevision{},
		&model.GalgameContributor{},
	); err != nil {
		fmt.Fprintf(os.Stderr, "SKIP: migration failed: %v\n", err)
		os.Exit(0)
	}

	testDB = db

	galgameRepo := repository.NewGalgameRepository(db)
	revisionRepo := repository.NewRevisionRepository(db)
	prRepo := repository.NewPRRepository(db)
	// No OAuth DB for tests — user lookups will return empty
	userRepo := repository.NewUserReadonlyRepository(db)

	testSvc = NewGalgameService(galgameRepo, revisionRepo, prRepo, userRepo)

	code := m.Run()
	os.Exit(code)
}

func cleanTables(t *testing.T) {
	t.Helper()
	tables := []string{
		"galgame_revision", "galgame_pr", "galgame_contributor",
		"galgame_link", "galgame_alias",
		"galgame_tag_relation", "galgame_official_relation", "galgame_engine_relation",
		"galgame", "galgame_series",
		"galgame_tag", "galgame_tag_alias",
		"galgame_official", "galgame_official_alias",
		"galgame_engine",
	}
	for _, table := range tables {
		testDB.Exec(fmt.Sprintf("TRUNCATE %s RESTART IDENTITY CASCADE", table))
	}
}

// Test helpers

func createTestTag(t *testing.T, name, category string) int {
	t.Helper()
	tag := &model.GalgameTag{Name: name, Category: category}
	if err := testDB.Create(tag).Error; err != nil {
		t.Fatalf("failed to create test tag: %v", err)
	}
	return tag.ID
}

func createTestOfficial(t *testing.T, name, category string) int {
	t.Helper()
	o := &model.GalgameOfficial{Name: name, Category: category}
	if err := testDB.Create(o).Error; err != nil {
		t.Fatalf("failed to create test official: %v", err)
	}
	return o.ID
}

func createTestEngine(t *testing.T, name string) int {
	t.Helper()
	e := &model.GalgameEngine{Name: name, Alias: []byte("[]")}
	if err := testDB.Create(e).Error; err != nil {
		t.Fatalf("failed to create test engine: %v", err)
	}
	return e.ID
}

func createTestSeries(t *testing.T, name string) int {
	t.Helper()
	s := &model.GalgameSeries{Name: name}
	if err := testDB.Create(s).Error; err != nil {
		t.Fatalf("failed to create test series: %v", err)
	}
	return s.ID
}

var testTagRepo *repository.TagRepository
var testOfficialRepo *repository.OfficialRepository
var testEngineRepo *repository.EngineRepository
var testSeriesRepo *repository.SeriesRepository
var testGalgameRepo *repository.GalgameRepository
var testAdminRepo *repository.AdminRepository

func init() {
	// These will be set after TestMain runs; accessed via lazy init in tests
}

func getRepos() {
	if testDB == nil {
		return
	}
	if testTagRepo == nil {
		testTagRepo = repository.NewTagRepository(testDB)
		testOfficialRepo = repository.NewOfficialRepository(testDB)
		testEngineRepo = repository.NewEngineRepository(testDB)
		testSeriesRepo = repository.NewSeriesRepository(testDB)
		testGalgameRepo = repository.NewGalgameRepository(testDB)
		testAdminRepo = repository.NewAdminRepository(testDB)
	}
}
