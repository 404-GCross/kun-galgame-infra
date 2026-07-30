package service

import (
	"fmt"
	"os"
	"testing"

	catalogMigrate "api/internal/platform/catalog/migrate"
	"api/internal/platform/editing"
	"api/internal/platform/galgame/editquery"
	"api/internal/platform/galgame/editspec"
	"api/internal/platform/galgame/model"
	"api/internal/platform/galgame/repository"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

var (
	testDB     *gorm.DB
	testSvc    *GalgameService
	testEngine *editing.Engine
	testEditq  *editquery.Querier
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
		&model.GalgameTagEdge{},
		&model.GalgameOfficial{},
		&model.GalgameOfficialAlias{},
		&model.GalgameEngine{},
		&model.Galgame{},
		&model.GalgameAlias{},
		&model.GalgameTagRelation{},
		&model.GalgameOfficialRelation{},
		&model.GalgameEngineRelation{},
		&model.GalgameLink{},
		&model.GalgameCover{},
		&model.GalgameScreenshot{},
		&model.GalgamePR{},
		&model.GalgameRevision{},
		&model.TaxonomyRevision{},
		&model.GalgameContributor{},
		&model.GalgameMessage{},
		// Legacy table, still wiped by DeleteDraft — test DB must mirror the
		// production schema (migrate-catalog) or DeleteDraft 42P01s.
		&model.GalgameHistory{},
	); err != nil {
		fmt.Fprintf(os.Stderr, "SKIP: migration failed: %v\n", err)
		os.Exit(0)
	}

	// Replicate the partial unique indexes that migrate-catalog creates in
	// production:
	//   - uq_galgame_vndb_id_nonempty: vndb_id unique only when non-empty
	//     (multiple status=3 submissions can share vndb_id='').
	//   - idx_galgame_cover_pinned:    at most one cover per galgame may
	//     hold sort_order=0 (= the pinned banner).
	//   - idx_galgame_cover_portrait_pinned: at most one cover per galgame may
	//     hold portrait_pinned=true (= the pinned vertical banner).
	_ = db.Exec(`DROP INDEX IF EXISTS idx_galgame_vndb_id`).Error
	_ = db.Exec(`DROP INDEX IF EXISTS uni_galgame_vndb_id`).Error
	_ = db.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS uq_galgame_vndb_id_nonempty
		ON galgame(vndb_id) WHERE vndb_id <> ''`).Error
	_ = db.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS idx_galgame_cover_pinned
		ON galgame_cover(galgame_id) WHERE sort_order = 0`).Error
	_ = db.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS idx_galgame_cover_portrait_pinned
		ON galgame_cover(galgame_id) WHERE portrait_pinned`).Error

	testDB = db

	// E2a: the engine tables + strangler legacy columns live on the catalog
	// pool in production; in tests both pools are this one test DB.
	if err := editing.AutoMigrate(db); err != nil {
		fmt.Fprintf(os.Stderr, "SKIP: editing migration failed: %v\n", err)
		os.Exit(0)
	}
	if err := catalogMigrate.EditLegacyColumns(db); err != nil {
		fmt.Fprintf(os.Stderr, "SKIP: legacy columns failed: %v\n", err)
		os.Exit(0)
	}

	galgameRepo := repository.NewGalgameRepository(db)
	revisionRepo := repository.NewRevisionRepository(db)
	prRepo := repository.NewPRRepository(db)
	// No OAuth DB for tests — user lookups will return empty
	userRepo := repository.NewUserReadonlyRepository(db)

	// E2a: register galgame.game and wire the engine + wire bridge exactly
	// like the cmd/catalog assembly point does.
	reg := editing.NewRegistry()
	if err := editspec.RegisterGame(reg, db, nil, nil); err != nil {
		fmt.Fprintf(os.Stderr, "SKIP: RegisterGame failed: %v\n", err)
		os.Exit(0)
	}
	testEngine = editing.NewEngine(db, reg)
	testEditq = editquery.New(db)

	testSvc = NewGalgameService(galgameRepo, revisionRepo, prRepo, userRepo).
		WithEditing(testEngine, testEditq)

	code := m.Run()
	os.Exit(code)
}

func cleanTables(t *testing.T) {
	t.Helper()
	tables := []string{
		"galgame_message",
		"taxonomy_revision",
		// E2a: writes land in the engine tables now (old tables are frozen).
		"edit_proposal_amendment", "edit_revision", "edit_proposal",
		"galgame_revision", "galgame_pr", "galgame_contributor",
		"galgame_link", "galgame_alias",
		"galgame_cover", "galgame_screenshot",
		"galgame_tag_relation", "galgame_official_relation", "galgame_engine_relation",
		"galgame", "galgame_series",
		"galgame_tag", "galgame_tag_alias", "galgame_tag_edge",
		"galgame_official", "galgame_official_alias",
		"galgame_engine",
	}
	for _, table := range tables {
		testDB.Exec(fmt.Sprintf("TRUNCATE %s RESTART IDENTITY CASCADE", table))
	}
}

// Test helpers
//
// bridgeRevision / bridgeRevisionCount (the old-wire revision readers the
// surviving write-path tests assert against) live in
// wire_revision_testutil_test.go — a test-only reconstruction of the shape the
// production old-wire bridge served before it retired at E3b.

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
var testMessageRepo *repository.MessageRepository
var testTaxRevRepo *repository.TaxonomyRevisionRepository
var testSubmissionSvc *SubmissionService
var testAdminSvc *AdminService
var testTaxSvc *TaxonomyService

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
		testMessageRepo = repository.NewMessageRepository(testDB)
		testTaxRevRepo = repository.NewTaxonomyRevisionRepository(testDB)
		testSubmissionSvc = NewSubmissionService(testGalgameRepo, testMessageRepo).WithEditing(testEngine)
		testAdminSvc = NewAdminService(testGalgameRepo, testMessageRepo).WithEditing(testEngine)
		testTaxSvc = NewTaxonomyService(testTagRepo, testOfficialRepo, testEngineRepo, testSeriesRepo, testTaxRevRepo, testGalgameRepo)
	}
}
