package permissions_test

import (
	"context"
	"fmt"
	"os"
	"testing"

	"api/internal/platform/authz"
	"api/internal/platform/permissions"
	trustPerm "api/internal/platform/trust/perm"
	"api/internal/testsupport/dbtest"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

var testDB *gorm.DB

func TestMain(m *testing.M) {
	dsn, ok := dbtest.DSN()
	if !ok {
		dbtest.SkipMain("permissions overlay suite")
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{
		Logger: gormlogger.Default.LogMode(gormlogger.Silent),
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "FAIL: cannot open the assigned test database: %v\n", err)
		os.Exit(1)
	}
	if err := db.AutoMigrate(
		&permissions.RolePermissionOverride{}, &permissions.PermissionAuditLog{},
	); err != nil {
		fmt.Fprintf(os.Stderr, "FAIL: migrate: %v\n", err)
		os.Exit(1)
	}
	// The audit read LEFT JOINs users for the actor's display name. The full
	// auth model is not this suite's subject, so stand up only the two columns
	// the join touches.
	if err := db.Exec(`CREATE TABLE IF NOT EXISTS users (
		id BIGSERIAL PRIMARY KEY, name TEXT NOT NULL DEFAULT '')`).Error; err != nil {
		fmt.Fprintf(os.Stderr, "FAIL: create users stub: %v\n", err)
		os.Exit(1)
	}
	testDB = db
	os.Exit(m.Run())
}

func reset(t *testing.T) {
	t.Helper()
	for _, table := range []string{"role_permission_overrides", "permission_audit_logs", "users"} {
		if err := testDB.Exec("TRUNCATE " + table + " RESTART IDENTITY CASCADE").Error; err != nil {
			t.Fatalf("truncate %s: %v", table, err)
		}
	}
}

// TestStoreGrantRevokeAndAudit pins that an overlay change and its audit row
// are inseparable, and that a revoke leaves the history behind.
func TestStoreGrantRevokeAndAudit(t *testing.T) {
	reset(t)
	ctx := context.Background()
	store := permissions.NewStore(testDB)

	if err := testDB.Exec(`INSERT INTO users (id, name) VALUES (7, 'kun')`).Error; err != nil {
		t.Fatalf("seed actor: %v", err)
	}

	key := string(trustPerm.TermManage)
	if err := store.Grant(ctx, permissions.RoleModerator, key, 7); err != nil {
		t.Fatalf("grant: %v", err)
	}

	has, err := store.HasOverride(ctx, permissions.RoleModerator, key)
	if err != nil || !has {
		t.Fatalf("HasOverride after grant = %v, %v; want true, nil", has, err)
	}

	// A second grant is a lost race, not a silent duplicate.
	if err := store.Grant(ctx, permissions.RoleModerator, key, 7); err != permissions.ErrAlreadyGranted {
		t.Errorf("duplicate grant = %v, want ErrAlreadyGranted", err)
	}

	if err := store.Revoke(ctx, permissions.RoleModerator, key, 7); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if has, _ := store.HasOverride(ctx, permissions.RoleModerator, key); has {
		t.Error("override row survived the revoke")
	}
	if err := store.Revoke(ctx, permissions.RoleModerator, key, 7); err != permissions.ErrNotGranted {
		t.Errorf("revoke with nothing to revoke = %v, want ErrNotGranted", err)
	}

	entries, err := store.RecentAudit(ctx, 50)
	if err != nil {
		t.Fatalf("audit: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("audit has %d rows, want 2 (grant + revoke): %+v", len(entries), entries)
	}
	// Newest first.
	if entries[0].Action != permissions.ActionRevoke || entries[1].Action != permissions.ActionGrant {
		t.Errorf("audit order = %q, %q; want revoke, grant", entries[0].Action, entries[1].Action)
	}
	if entries[0].ActorName != "kun" {
		t.Errorf("actor name = %q, want kun", entries[0].ActorName)
	}
	if entries[0].Permission != key || entries[0].Role != permissions.RoleModerator {
		t.Errorf("audit row lost its subject: %+v", entries[0])
	}
}

// TestDistributorRefreshSwapsLiveResolvers is the end-to-end proof that a row
// in the table changes what a running service enforces: it grants a REAL key to
// moderator, refreshes, and asks the trust domain's package-level Holder — the
// same object cmd/trust's route gate captured at startup.
func TestDistributorRefreshSwapsLiveResolvers(t *testing.T) {
	reset(t)
	ctx := context.Background()
	reg := permissions.Live()
	dist := permissions.NewDistributor(testDB, reg, nil)
	// Leave the process on the code floor whatever this test does.
	t.Cleanup(func() {
		reset(t)
		if err := dist.Refresh(ctx); err != nil {
			t.Errorf("cleanup refresh: %v", err)
		}
	})

	if trustPerm.Resolver.Can([]string{"moderator"}, trustPerm.TermManage) {
		t.Fatal("moderator must start without trust.term_manage")
	}

	store := permissions.NewStore(testDB)
	if err := store.Grant(ctx, permissions.RoleModerator, string(trustPerm.TermManage), 1); err != nil {
		t.Fatalf("grant: %v", err)
	}
	if err := dist.Refresh(ctx); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if !trustPerm.Resolver.Can([]string{"moderator"}, trustPerm.TermManage) {
		t.Error("the overlay grant did not reach the live trust resolver")
	}
	// The code floor is untouched for everyone else.
	if !trustPerm.Resolver.Can([]string{"admin"}, trustPerm.TermManage) {
		t.Error("the refresh dropped admin's code-floor grant")
	}

	if err := store.Revoke(ctx, permissions.RoleModerator, string(trustPerm.TermManage), 1); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if err := dist.Refresh(ctx); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if trustPerm.Resolver.Can([]string{"moderator"}, trustPerm.TermManage) {
		t.Error("the revoke did not return moderator to the code floor")
	}
}

// TestLoadSnapshotDropsRetiredKeys pins that a row naming a key no longer in the
// code cannot put that key back into a resolver.
func TestLoadSnapshotDropsRetiredKeys(t *testing.T) {
	reset(t)
	ctx := context.Background()

	rows := []permissions.RolePermissionOverride{
		{Role: permissions.RoleModerator, Permission: string(trustPerm.TermManage), GrantedByUserID: 1},
		{Role: permissions.RoleModerator, Permission: "galgame.publish_direct", GrantedByUserID: 1},
	}
	if err := testDB.Create(&rows).Error; err != nil {
		t.Fatalf("seed: %v", err)
	}

	snap, err := permissions.LoadSnapshot(ctx, testDB, permissions.Live())
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	got := snap[permissions.RoleModerator]
	if len(got) != 1 || got[0] != trustPerm.TermManage {
		t.Errorf("snapshot = %v, want only %q", got, trustPerm.TermManage)
	}
}

// TestServiceApplyEnforcesValidationBeforeWriting pins that a rejected write
// leaves no trace — no override row and, crucially, no audit row claiming a
// change that never happened.
func TestServiceApplyEnforcesValidationBeforeWriting(t *testing.T) {
	reset(t)
	ctx := context.Background()
	reg := permissions.Live()
	dist := permissions.NewDistributor(testDB, reg, nil)
	t.Cleanup(func() {
		reset(t)
		if err := dist.Refresh(ctx); err != nil {
			t.Errorf("cleanup refresh: %v", err)
		}
	})
	svc := permissions.NewService(reg, permissions.NewStore(testDB), dist)

	ren := permissions.Caller{UserID: 1, Roles: []string{"admin", "ren"}}

	// Non-delegable: refused even for ren.
	err := svc.Apply(ctx, ren, permissions.Action{
		Grant: true, Role: permissions.RoleAdmin, Permission: authz.Permission("oauth.sites.manage_all"),
	})
	assertRejected(t, err, "不可委派")

	// Containment: moderator cannot get a key admin lacks.
	err = svc.Apply(ctx, ren, permissions.Action{
		Grant: true, Role: permissions.RoleModerator, Permission: authz.Permission("oauth.users.pii_view"),
	})
	assertRejected(t, err, "授予 admin")

	var overrides, audits int64
	testDB.Model(&permissions.RolePermissionOverride{}).Count(&overrides)
	testDB.Model(&permissions.PermissionAuditLog{}).Count(&audits)
	if overrides != 0 || audits != 0 {
		t.Errorf("a rejected write left %d override(s) and %d audit row(s)", overrides, audits)
	}

	// The ordered sequence succeeds and IS audited.
	if err := svc.Apply(ctx, ren, permissions.Action{
		Grant: true, Role: permissions.RoleAdmin, Permission: authz.Permission("oauth.users.pii_view"),
	}); err != nil {
		t.Fatalf("granting admin first must pass: %v", err)
	}
	if err := svc.Apply(ctx, ren, permissions.Action{
		Grant: true, Role: permissions.RoleModerator, Permission: authz.Permission("oauth.users.pii_view"),
	}); err != nil {
		t.Fatalf("granting moderator after admin must pass: %v", err)
	}
	testDB.Model(&permissions.PermissionAuditLog{}).Count(&audits)
	if audits != 2 {
		t.Errorf("audit rows = %d, want 2", audits)
	}
}

// TestMatrixReportsSourcesAndEditability pins the console export: which cells
// are code-floor, which are overlay, and which THIS caller may toggle.
func TestMatrixReportsSourcesAndEditability(t *testing.T) {
	reset(t)
	ctx := context.Background()
	reg := permissions.Live()
	dist := permissions.NewDistributor(testDB, reg, nil)
	t.Cleanup(func() {
		reset(t)
		if err := dist.Refresh(ctx); err != nil {
			t.Errorf("cleanup refresh: %v", err)
		}
	})
	svc := permissions.NewService(reg, permissions.NewStore(testDB), dist)
	ren := permissions.Caller{UserID: 1, Roles: []string{"admin", "ren"}}

	if err := svc.Apply(ctx, ren, permissions.Action{
		Grant: true, Role: permissions.RoleAdmin, Permission: authz.Permission("oauth.users.pii_view"),
	}); err != nil {
		t.Fatalf("seed grant: %v", err)
	}

	m, err := svc.Matrix(ctx, ren)
	if err != nil {
		t.Fatalf("matrix: %v", err)
	}
	if !m.ManagesPermissions {
		t.Error("ren must be reported as holding oauth.permissions.manage")
	}

	row := findKey(t, m, "oauth.users.pii_view")
	if c := row.Grants[permissions.RoleAdmin]; !c.Granted || c.Source != permissions.SourceOverlay || !c.Editable {
		t.Errorf("admin cell = %+v; want granted from the overlay and editable", c)
	}
	if c := row.Grants[permissions.RoleRen]; !c.Granted || c.Source != permissions.SourceCode || c.Editable {
		t.Errorf("ren cell = %+v; want a granted, non-editable code cell", c)
	}
	if c := row.Grants[permissions.RoleUser]; c.Granted || c.Editable {
		t.Errorf("user cell = %+v; want empty and non-editable", c)
	}

	locked := findKey(t, m, "oauth.sites.manage_all")
	if !locked.NonDelegable {
		t.Error("oauth.sites.manage_all must be flagged non-delegable")
	}
	for _, role := range permissions.EditableRoles {
		if locked.Grants[role].Editable {
			t.Errorf("a non-delegable key must have no editable cell (%s)", role)
		}
	}

	// An ordinary admin sees a much narrower editable set — and never the
	// creator column.
	admin := permissions.Caller{UserID: 2, Roles: []string{"admin"}}
	am, err := svc.Matrix(ctx, admin)
	if err != nil {
		t.Fatalf("matrix (admin): %v", err)
	}
	if am.ManagesPermissions {
		t.Error("a plain admin must not report permissions.manage")
	}
	arow := findKey(t, am, "oauth.users.pii_view")
	if arow.Grants[permissions.RoleCreator].Editable {
		t.Error("the creator column must be ren-only")
	}
	if arow.Grants[permissions.RoleAdmin].Editable {
		t.Error("an admin must not edit its own tier")
	}
}

func findKey(t *testing.T, m *permissions.Matrix, key string) permissions.KeyRow {
	t.Helper()
	for _, d := range m.Domains {
		for _, k := range d.Keys {
			if k.Key == key {
				return k
			}
		}
	}
	t.Fatalf("key %q not in the matrix", key)
	return permissions.KeyRow{}
}
