package service

import (
	"context"
	"testing"
)

func TestListUsableReasons(t *testing.T) {
	if testDB == nil {
		t.Skip("TEST_DATABASE_DSN not set")
	}
	cleanTables(t)
	svc := NewRegistryService(testDB)
	ctx := context.Background()

	siteA, siteB := "site-a", "site-b"
	if _, err := svc.CreateReason(ctx, 1, "custom_a", "A 站自定义", &siteA, 1); err != nil {
		t.Fatalf("create site-a reason: %v", err)
	}
	if _, err := svc.CreateReason(ctx, 1, "custom_b", "B 站自定义", &siteB, 1); err != nil {
		t.Fatalf("create site-b reason: %v", err)
	}
	dep, err := svc.CreateReason(ctx, 1, "retired_a", "A 站已弃用", &siteA, 1)
	if err != nil {
		t.Fatalf("create deprecated reason: %v", err)
	}
	deprecated := true
	if _, err := svc.PatchReason(ctx, 1, dep.ID, ReasonPatch{IsDeprecated: &deprecated}); err != nil {
		t.Fatalf("deprecate reason: %v", err)
	}
	t.Cleanup(func() {
		testDB.Exec(`DELETE FROM trust_report_reason WHERE site IS NOT NULL`)
	})

	reasons, err := svc.ListUsableReasons(ctx, siteA)
	if err != nil {
		t.Fatalf("list usable: %v", err)
	}

	byKey := map[string]bool{}
	globals := 0
	for _, r := range reasons {
		byKey[r.Key] = true
		if r.Site == nil {
			globals++
		}
		if r.IsDeprecated {
			t.Fatalf("deprecated reason %q leaked into the usable feed", r.Key)
		}
	}
	if globals != 6 {
		t.Fatalf("global base = %d reasons, want the 6 seeds", globals)
	}
	if !byKey["custom_a"] {
		t.Fatal("site-a extension missing from its own feed")
	}
	if byKey["custom_b"] {
		t.Fatal("site-b extension leaked into site-a's feed")
	}
	if byKey["retired_a"] {
		t.Fatal("deprecated site-a extension leaked into the feed")
	}
}
