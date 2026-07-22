package editing_test

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"testing"

	"api/internal/platform/editing"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// Integration tests against a real Postgres (the catalog service_test.go
// convention): TEST_DATABASE_DSN or a local default; missing database =
// skip the whole package. The engine tables come from editing.AutoMigrate —
// the exact production DDL — and the entity family is FAKED on purpose: a
// scratch edit_test_widget table plus an EntityTypeSpec registered the same
// way a real family would at its assembly point (charter ruling 1).

var (
	testDB  *gorm.DB
	testCtx = context.Background()
)

// editSuiteLockKey ("edts") serializes the suites that TRUNCATE or write the
// shared edit_* tables — this package, catalog/handler, and the galgameapp
// propose suite — whenever they run against one database (unified
// TEST_DATABASE_DSN). Distinct from every other suite key and from the
// engine's business classid 0x65646974 (which uses the two-int4 lock form)
// per the single-keyspace advisory-lock rule.
const editSuiteLockKey int64 = 0x65647473

func acquireEditSuiteLock(db *sql.DB) func() {
	if db == nil {
		return func() {}
	}
	ctx := context.Background()
	conn, err := db.Conn(ctx)
	if err != nil {
		return func() {}
	}
	if _, err := conn.ExecContext(ctx, "SELECT pg_advisory_lock($1)", editSuiteLockKey); err != nil {
		_ = conn.Close()
		return func() {}
	}
	return func() {
		_, _ = conn.ExecContext(ctx, "SELECT pg_advisory_unlock($1)", editSuiteLockKey)
		_ = conn.Close()
	}
}

func TestMain(m *testing.M) {
	dsn := os.Getenv("TEST_DATABASE_DSN")
	if dsn == "" {
		dsn = "host=localhost port=5432 user=postgres password=postgres dbname=kun_catalog_test sslmode=disable"
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "SKIP: cannot connect to test database: %v\n", err)
		os.Exit(0)
	}
	// The SKIP paths below os.Exit without releasing: process death closes the
	// session and Postgres drops its advisory locks, so that is safe.
	sqlDB, _ := db.DB()
	release := acquireEditSuiteLock(sqlDB)
	if err := editing.AutoMigrate(db); err != nil {
		fmt.Fprintf(os.Stderr, "SKIP: editing migration failed: %v\n", err)
		os.Exit(0)
	}
	if err := db.Exec(`
		CREATE TABLE IF NOT EXISTS edit_test_widget (
			id           bigint PRIMARY KEY,
			name         text NOT NULL DEFAULT '',
			olang        text NOT NULL DEFAULT 'ja',
			rating       smallint NOT NULL DEFAULT 0,
			open_note    text NOT NULL DEFAULT '',
			trusted_note text NOT NULL DEFAULT '',
			locked_code  text NOT NULL DEFAULT ''
		)`).Error; err != nil {
		fmt.Fprintf(os.Stderr, "SKIP: widget table creation failed: %v\n", err)
		os.Exit(0)
	}
	testDB = db
	code := m.Run()
	release()
	os.Exit(code)
}

func cleanTables(t *testing.T) {
	t.Helper()
	for _, table := range []string{
		"edit_proposal_amendment", "edit_proposal", "edit_revision", "edit_test_widget",
	} {
		if err := testDB.Exec("TRUNCATE " + table + " RESTART IDENTITY CASCADE").Error; err != nil {
			t.Fatalf("truncate %s: %v", table, err)
		}
	}
}

// --- fake family ------------------------------------------------------------

// Field keys of the fake widget family. Policies cover the E0 matrix:
//
//	name         propose perm:edit.test.widget   review perm  automerge never   (spec default)
//	olang        (default, enum-style validator)
//	rating       (default, integer validator)
//	open_note    propose open                    automerge always
//	trusted_note propose open                    automerge trusted
//	locked_code  locked
//	legacy       deprecated
//
// Site overlay "overlay-site": open_note → locked, name → open+always.
const (
	fName    = "test.widget.name"
	fOLang   = "test.widget.olang"
	fRating  = "test.widget.rating"
	fOpen    = "test.widget.open_note"
	fTrusted = "test.widget.trusted_note"
	fLocked  = "test.widget.locked_code"
	fLegacy  = "test.widget.legacy"

	permPropose = "edit.test.widget"
	permReview  = "edit.test.widget.review"

	overlaySite = "overlay-site"
	// ownerReviewSite overlays ONLY name with OwnerReview (E3b): the asserted
	// entity owner passes the review rule there — and nowhere else.
	ownerReviewSite = "owner-review-site"
)

type widgetRow struct {
	ID          int64  `gorm:"column:id"`
	Name        string `gorm:"column:name"`
	OLang       string `gorm:"column:olang"`
	Rating      int16  `gorm:"column:rating"`
	OpenNote    string `gorm:"column:open_note"`
	TrustedNote string `gorm:"column:trusted_note"`
	LockedCode  string `gorm:"column:locked_code"`
}

func (widgetRow) TableName() string { return "edit_test_widget" }

func stringValidator(v any) error {
	if _, ok := v.(string); !ok {
		return fmt.Errorf("must be a string")
	}
	return nil
}

func ratingValidator(v any) error {
	f, ok := v.(float64)
	if !ok || f != float64(int64(f)) || f < 0 || f > 10 {
		return fmt.Errorf("must be an integer 0..10")
	}
	return nil
}

func widgetApply(column string) editing.ApplyFunc {
	return func(ctx context.Context, tx *gorm.DB, id int64, value any) error {
		if f, ok := value.(float64); ok {
			value = int64(f)
		}
		res := tx.Exec("UPDATE edit_test_widget SET "+column+" = ? WHERE id = ?", value, id)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return editing.ErrEntityNotFound
		}
		return nil
	}
}

func widgetSpec(db *gorm.DB) editing.EntityTypeSpec {
	deny := func(any) error { return fmt.Errorf("legacy field") }
	denyApply := func(context.Context, *gorm.DB, int64, any) error { return fmt.Errorf("legacy field") }
	return editing.EntityTypeSpec{
		Family: "test",
		Type:   "test.widget",
		LoadSnapshot: func(ctx context.Context, id int64) (map[string]any, error) {
			var w widgetRow
			if err := db.WithContext(ctx).First(&w, id).Error; err != nil {
				if err == gorm.ErrRecordNotFound {
					return nil, editing.ErrEntityNotFound
				}
				return nil, err
			}
			return map[string]any{
				fName: w.Name, fOLang: w.OLang, fRating: int64(w.Rating),
				fOpen: w.OpenNote, fTrusted: w.TrustedNote, fLocked: w.LockedCode,
			}, nil
		},
		Txn: func(ctx context.Context, fn func(tx *gorm.DB) error) error {
			return db.WithContext(ctx).Transaction(fn)
		},
		DefaultPolicy: editing.Policy{
			Propose:   editing.ProposePerm(permPropose),
			Review:    editing.ReviewPerm(permReview),
			Automerge: editing.AutomergeNever,
		},
		Fields: []editing.FieldSpec{
			{Key: fName, Kind: editing.KindText, DiffHint: editing.DiffHintInline,
				Validate: stringValidator, Apply: widgetApply("name")},
			{Key: fOLang, Kind: editing.KindEnum, DiffHint: editing.DiffHintInline,
				Validate: stringValidator, Apply: widgetApply("olang")},
			{Key: fRating, Kind: editing.KindInt, DiffHint: editing.DiffHintInline,
				Validate: ratingValidator, Apply: widgetApply("rating")},
			{Key: fOpen, Kind: editing.KindText, DiffHint: editing.DiffHintLines,
				Policy:   &editing.Policy{Propose: editing.ProposeOpen, Review: editing.ReviewPerm(permReview), Automerge: editing.AutomergeAlways},
				Validate: stringValidator, Apply: widgetApply("open_note")},
			{Key: fTrusted, Kind: editing.KindText, DiffHint: editing.DiffHintLines,
				Policy:   &editing.Policy{Propose: editing.ProposeOpen, Review: editing.ReviewPerm(permReview), Automerge: editing.AutomergeTrusted},
				Validate: stringValidator, Apply: widgetApply("trusted_note")},
			{Key: fLocked, Kind: editing.KindText, DiffHint: editing.DiffHintInline,
				Policy:   &editing.Policy{Propose: editing.ProposeLocked, Review: editing.ReviewPerm(permReview), Automerge: editing.AutomergeNever},
				Validate: stringValidator, Apply: widgetApply("locked_code")},
			{Key: fLegacy, Kind: editing.KindText, DiffHint: editing.DiffHintInline,
				Deprecated: true, Validate: deny, Apply: denyApply},
		},
		SiteOverlays: map[string]map[string]editing.Policy{
			overlaySite: {
				fOpen: {Propose: editing.ProposeLocked, Review: editing.ReviewPerm(permReview), Automerge: editing.AutomergeNever},
				fName: {Propose: editing.ProposeOpen, Review: editing.ReviewPerm(permReview), Automerge: editing.AutomergeAlways},
			},
			ownerReviewSite: {
				fName: {Propose: editing.ProposeOpen, Review: editing.ReviewPerm(permReview), Automerge: editing.AutomergeNever, OwnerReview: true},
			},
		},
	}
}

func newEngine(t *testing.T) *editing.Engine {
	t.Helper()
	cleanTables(t)
	reg := editing.NewRegistry()
	if err := reg.Register(widgetSpec(testDB)); err != nil {
		t.Fatalf("register widget spec: %v", err)
	}
	return editing.NewEngine(testDB, reg)
}

func createWidget(t *testing.T, id int64) {
	t.Helper()
	if err := testDB.Exec("INSERT INTO edit_test_widget (id) VALUES (?)", id).Error; err != nil {
		t.Fatalf("create widget: %v", err)
	}
}

func loadWidget(t *testing.T, id int64) widgetRow {
	t.Helper()
	var w widgetRow
	if err := testDB.First(&w, id).Error; err != nil {
		t.Fatalf("load widget: %v", err)
	}
	return w
}

// --- actors -----------------------------------------------------------------

func actorWith(uid int64, tier int16, perms ...string) editing.PolicyContext {
	set := make(map[string]bool, len(perms))
	for _, p := range perms {
		set[p] = true
	}
	return editing.PolicyContext{
		UserID: uid, Site: "test-site", TrustTier: tier,
		HasPerm: func(key string) bool { return set[key] },
	}
}

func anonActor(uid int64) editing.PolicyContext    { return actorWith(uid, 0) }
func trustedActor(uid int64) editing.PolicyContext { return actorWith(uid, editing.TrustedTier) }
func editorActor(uid int64) editing.PolicyContext  { return actorWith(uid, 0, permPropose) }
func reviewerActor(uid int64) editing.PolicyContext {
	return actorWith(uid, 0, permPropose, permReview)
}
