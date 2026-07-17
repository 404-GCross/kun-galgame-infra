package editspec_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"testing"

	"api/internal/platform/authz"
	"api/internal/platform/catalog/editspec"
	"api/internal/platform/catalog/migrate"
	"api/internal/platform/catalog/model"
	"api/internal/platform/catalog/perm"
	"api/internal/platform/catalog/seed"
	"api/internal/platform/editing"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// End-to-end pilot test (acceptance line: catalog.work three fields —
// propose → amend → merge → revision → diff → revert) against the exact
// production schema (migrate.Run includes editing.AutoMigrate) and the real
// catalog.work spec with the real perm keys.

var (
	testDB  *gorm.DB
	testCtx = context.Background()
)

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

func newEngine(t *testing.T) *editing.Engine {
	t.Helper()
	for _, table := range []string{
		"edit_proposal_amendment", "edit_proposal", "edit_revision", "catalog_work",
	} {
		if err := testDB.Exec("TRUNCATE " + table + " RESTART IDENTITY CASCADE").Error; err != nil {
			t.Fatalf("truncate %s: %v", table, err)
		}
	}
	reg := editing.NewRegistry()
	if err := editspec.RegisterWork(reg, testDB); err != nil {
		t.Fatalf("register catalog.work: %v", err)
	}
	return editing.NewEngine(testDB, reg)
}

func createWork(t *testing.T, displayName string) *model.CatalogWork {
	t.Helper()
	w := &model.CatalogWork{
		MediumID: 1, OLang: "ja", DisplayName: displayName,
		ContentRating: model.ContentRatingAllAges, Status: model.WorkStatusLive,
	}
	if err := testDB.Create(w).Error; err != nil {
		t.Fatalf("create work: %v", err)
	}
	return w
}

// realActor mirrors the S2S handler's policyCtx: roles resolve through the
// REAL catalog perm bundles (no fake HasPerm here — this is the wiring test).
func realActor(uid int64, roles ...string) editing.PolicyContext {
	return editing.PolicyContext{
		UserID: uid, Site: "nextmoe", TrustTier: 0,
		HasPerm: func(key string) bool {
			return perm.Resolver.Can(roles, authz.Permission(key))
		},
	}
}

func TestWorkPilotEndToEnd(t *testing.T) {
	e := newEngine(t)
	work := createWork(t, "初期名")

	editor := realActor(100, "admin")
	reviewer := realActor(200, "ren")
	nobody := realActor(300, "user")

	// A plain user cannot propose (default policy: perm-gated).
	var permErr *editing.PermissionError
	if _, _, err := e.CreateProposal(testCtx, editing.CreateProposalInput{
		EntityType: editspec.TypeWork, EntityID: work.ID,
		Patch: map[string]any{editspec.FieldWorkDisplayName: "x"}, Actor: nobody,
	}); !errors.As(err, &permErr) {
		t.Fatalf("user propose: %v", err)
	}

	// Propose all three pilot fields (admin).
	prop, rev, err := e.CreateProposal(testCtx, editing.CreateProposalInput{
		EntityType: editspec.TypeWork, EntityID: work.ID,
		Patch: map[string]any{
			editspec.FieldWorkDisplayName:   "正式名",
			editspec.FieldWorkOLang:         "en",
			editspec.FieldWorkContentRating: float64(model.ContentRatingR18),
		},
		Note: "fix metadata", Actor: editor,
	})
	if err != nil {
		t.Fatalf("propose: %v", err)
	}
	if rev != nil {
		t.Fatal("automerge=never must keep the proposal open")
	}

	// Amend (reviewer): correct the name, reject the olang change.
	if _, err := e.AmendProposal(testCtx, prop.ID, editing.AmendInput{
		Set:   map[string]any{editspec.FieldWorkDisplayName: "正式名・改"},
		Unset: []string{editspec.FieldWorkOLang},
		Note:  "olang stays ja", Actor: reviewer,
	}); err != nil {
		t.Fatalf("amend: %v", err)
	}

	// Merge → revision with double signature and precise changed_fields.
	merged, err := e.MergeProposal(testCtx, prop.ID, reviewer, "ok")
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	var changed []string
	if err := json.Unmarshal(merged.ChangedFields, &changed); err != nil {
		t.Fatal(err)
	}
	if len(changed) != 2 {
		t.Fatalf("changed_fields: %v", changed)
	}
	if merged.ActorUID != 100 || merged.AmenderUID == nil || *merged.AmenderUID != 200 {
		t.Fatalf("double signature: %+v", merged)
	}

	// The materialized row is the projection of the merge.
	var w model.CatalogWork
	if err := testDB.First(&w, work.ID).Error; err != nil {
		t.Fatal(err)
	}
	if w.DisplayName != "正式名・改" || w.OLang != "ja" || w.ContentRating != model.ContentRatingR18 {
		t.Fatalf("work after merge: %+v", w)
	}

	// Second edit for diff coverage (direct: none automerge, so via merge).
	prop2, _, err := e.CreateProposal(testCtx, editing.CreateProposalInput{
		EntityType: editspec.TypeWork, EntityID: work.ID,
		Patch: map[string]any{editspec.FieldWorkDisplayName: "第二版"}, Actor: editor,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.MergeProposal(testCtx, prop2.ID, reviewer, ""); err != nil {
		t.Fatalf("merge 2: %v", err)
	}

	// Diff 1 → 2: exactly the display_name changed.
	diffs, err := e.Diff(testCtx, editspec.TypeWork, work.ID, 1, 2)
	if err != nil {
		t.Fatalf("diff: %v", err)
	}
	if len(diffs) != 1 || diffs[0].Key != editspec.FieldWorkDisplayName ||
		diffs[0].From != "正式名・改" || diffs[0].To != "第二版" {
		t.Fatalf("diff: %+v", diffs)
	}

	// Revert to revision 1 — a NEW revision, action=reverted, history kept.
	_, rev3, err := e.Revert(testCtx, editing.RevertInput{
		EntityType: editspec.TypeWork, EntityID: work.ID, ToSeq: 1,
		Note: "undo", Actor: reviewer,
	})
	if err != nil {
		t.Fatalf("revert: %v", err)
	}
	if rev3.Seq != 3 || rev3.Action != editing.ActionReverted {
		t.Fatalf("revert revision: %+v", rev3)
	}
	if err := testDB.First(&w, work.ID).Error; err != nil {
		t.Fatal(err)
	}
	if w.DisplayName != "正式名・改" {
		t.Fatalf("reverted name: %q", w.DisplayName)
	}
	revs, err := e.ListRevisions(testCtx, editspec.TypeWork, work.ID, 0)
	if err != nil || len(revs) != 3 {
		t.Fatalf("history: %d err=%v", len(revs), err)
	}
}

func TestWorkValidators(t *testing.T) {
	e := newEngine(t)
	work := createWork(t, "validator target")
	editor := realActor(100, "admin")

	var valErr *editing.ValidationError
	cases := []map[string]any{
		{editspec.FieldWorkDisplayName: ""},                    // empty
		{editspec.FieldWorkDisplayName: "   "},                 // whitespace
		{editspec.FieldWorkDisplayName: 42.0},                  // not a string
		{editspec.FieldWorkOLang: "xx"},                        // not whitelisted
		{editspec.FieldWorkOLang: "JA"},                        // case-sensitive
		{editspec.FieldWorkContentRating: float64(3)},          // out of range
		{editspec.FieldWorkContentRating: 1.5},                 // not an integer
		{editspec.FieldWorkContentRating: "r18"},               // not a number
	}
	for i, patch := range cases {
		if _, _, err := e.CreateProposal(testCtx, editing.CreateProposalInput{
			EntityType: editspec.TypeWork, EntityID: work.ID, Patch: patch, Actor: editor,
		}); !errors.As(err, &valErr) {
			t.Errorf("case %d (%v): want ValidationError, got %v", i, patch, err)
		}
	}

	// Soft-deleted works are unreachable through the edit path.
	if err := testDB.Delete(&model.CatalogWork{}, work.ID).Error; err != nil {
		t.Fatal(err)
	}
	if _, _, err := e.CreateProposal(testCtx, editing.CreateProposalInput{
		EntityType: editspec.TypeWork, EntityID: work.ID,
		Patch: map[string]any{editspec.FieldWorkDisplayName: "ghost"}, Actor: editor,
	}); !errors.Is(err, editing.ErrEntityNotFound) {
		t.Fatalf("soft-deleted work: %v", err)
	}
}
