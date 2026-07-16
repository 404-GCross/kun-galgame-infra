package devapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
)

// newSelfService builds a self-service service sharing one repo + AdminService
// (the composition the wiring uses).
func newSelfService(t *testing.T) (*SelfServiceService, *AdminService, *Repository) {
	t.Helper()
	repo := NewRepository(testDB)
	admin := NewAdminService(repo, newMemStore())
	return NewSelfServiceService(repo, admin), admin, repo
}

// cleanupSelf resets the tables between self-service tests. Unlike cleanup (used
// by the admin tests, whose apps carry the 'devapitest_' id prefix), the
// self-service CreateApp mints a random-hex id, so we clear owner-scoped rows by
// owner_user_id instead.
func cleanupSelf(t *testing.T) {
	t.Helper()
	if err := testDB.Exec(`TRUNCATE developer_api_keys, developer_api_usage RESTART IDENTITY`).Error; err != nil {
		t.Fatalf("truncate: %v", err)
	}
	if err := testDB.Exec(`DELETE FROM oauth_clients WHERE owner_user_id IS NOT NULL OR id LIKE 'devapitest_%'`).Error; err != nil {
		t.Fatalf("clean clients: %v", err)
	}
}

// --- service-level owner guard + caps + cascade ---

// TestSelfServiceCreateAndOwnerScope: a created app is owned by its creator,
// listed only for that owner, and invisible to another user (404 signal).
func TestSelfServiceCreateAndOwnerScope(t *testing.T) {
	cleanupSelf(t)
	svc, _, _ := newSelfService(t)
	ctx := context.Background()
	const userA, userB = uint(1), uint(2)

	app, err := svc.CreateApp(ctx, userA, "my app", "a short description")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if app.OwnerUserID == nil || *app.OwnerUserID != userA {
		t.Fatalf("owner = %v, want %d", app.OwnerUserID, userA)
	}
	if !app.DevEnabled || app.DevTier != TierFree {
		t.Errorf("new app should be dev_enabled free, got enabled=%v tier=%q", app.DevEnabled, app.DevTier)
	}
	if app.Tagline != "a short description" {
		t.Errorf("description not stored in tagline, got %q", app.Tagline)
	}

	// A sees it; B does not.
	aApps, _ := svc.ListApps(ctx, userA)
	if len(aApps) != 1 {
		t.Fatalf("A app count = %d, want 1", len(aApps))
	}
	bApps, _ := svc.ListApps(ctx, userB)
	if len(bApps) != 0 {
		t.Fatalf("B app count = %d, want 0", len(bApps))
	}
	// B cannot GET A's app (owner guard → record-not-found).
	if _, err := svc.GetApp(ctx, userB, app.ID); err == nil {
		t.Errorf("B GET of A's app must be record-not-found, got nil")
	}
}

// TestSelfServiceAppCap: the 6th app for one owner is rejected.
func TestSelfServiceAppCap(t *testing.T) {
	cleanupSelf(t)
	svc, _, _ := newSelfService(t)
	ctx := context.Background()
	const owner = uint(1)

	for i := 0; i < MaxAppsPerOwner; i++ {
		if _, err := svc.CreateApp(ctx, owner, fmt.Sprintf("app %d", i), ""); err != nil {
			t.Fatalf("create %d: %v", i, err)
		}
	}
	if _, err := svc.CreateApp(ctx, owner, "over the cap", ""); err != ErrAppLimitReached {
		t.Fatalf("6th app err = %v, want ErrAppLimitReached", err)
	}
}

// TestSelfServiceKeyCapAndScope: the active-key cap and the scope allow-list.
func TestSelfServiceKeyCapAndScope(t *testing.T) {
	cleanupSelf(t)
	svc, _, _ := newSelfService(t)
	ctx := context.Background()
	const owner = uint(1)

	app, err := svc.CreateApp(ctx, owner, "keyed", "")
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// A disallowed scope is rejected.
	if _, _, err := svc.MintKey(ctx, owner, app.ID, MintKeyInput{Name: "bad", Scopes: []string{ScopeGalgameNSFW}}); err != ErrScopeNotAllowed {
		t.Fatalf("nsfw scope err = %v, want ErrScopeNotAllowed", err)
	}

	// Mint up to the cap.
	for i := 0; i < MaxActiveKeysPerApp; i++ {
		if _, _, err := svc.MintKey(ctx, owner, app.ID, MintKeyInput{Name: fmt.Sprintf("k%d", i)}); err != nil {
			t.Fatalf("mint %d: %v", i, err)
		}
	}
	if _, _, err := svc.MintKey(ctx, owner, app.ID, MintKeyInput{Name: "over"}); err != ErrKeyLimitReached {
		t.Fatalf("6th key err = %v, want ErrKeyLimitReached", err)
	}

	// Revoking one frees a slot.
	keys, _ := svc.ListKeys(ctx, owner, app.ID)
	if _, err := svc.RevokeKey(ctx, owner, app.ID, keys[0].ID); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if _, _, err := svc.MintKey(ctx, owner, app.ID, MintKeyInput{Name: "back under"}); err != nil {
		t.Fatalf("mint after revoke: %v", err)
	}
}

// TestSelfServiceKeyOwnerGuard: user B cannot touch A's key even if B guesses
// A's client_id + key id.
func TestSelfServiceKeyOwnerGuard(t *testing.T) {
	cleanupSelf(t)
	svc, _, repo := newSelfService(t)
	ctx := context.Background()
	const userA, userB = uint(1), uint(2)

	app, _ := svc.CreateApp(ctx, userA, "A app", "")
	key, plaintext, err := svc.MintKey(ctx, userA, app.ID, MintKeyInput{Name: "k"})
	if err != nil {
		t.Fatalf("mint: %v", err)
	}

	// B rotates/revokes A's key by full coordinates → record-not-found (app not owned).
	if _, _, err := svc.RotateKey(ctx, userB, app.ID, key.ID); err == nil {
		t.Errorf("B rotate of A's key must be record-not-found")
	}
	if _, err := svc.RevokeKey(ctx, userB, app.ID, key.ID); err == nil {
		t.Errorf("B revoke of A's key must be record-not-found")
	}
	// A's key is untouched — still resolves.
	if c, _ := repo.ResolveByHash(ctx, HashKey(plaintext), time.Now()); c == nil {
		t.Errorf("A's key must still resolve after B's failed attempts")
	}
}

// TestSelfServiceDeactivateCascade: deactivating an app revokes every live key
// (and they stop resolving) and flips dev_enabled off.
func TestSelfServiceDeactivateCascade(t *testing.T) {
	cleanupSelf(t)
	svc, _, repo := newSelfService(t)
	ctx := context.Background()
	const owner = uint(1)

	app, _ := svc.CreateApp(ctx, owner, "doomed", "")
	_, p1, _ := svc.MintKey(ctx, owner, app.ID, MintKeyInput{Name: "k1"})
	_, p2, _ := svc.MintKey(ctx, owner, app.ID, MintKeyInput{Name: "k2"})

	if err := svc.DeactivateApp(ctx, owner, app.ID); err != nil {
		t.Fatalf("deactivate: %v", err)
	}
	// Both keys revoked → neither resolves (revoked_at set AND dev_enabled off).
	if c, _ := repo.ResolveByHash(ctx, HashKey(p1), time.Now()); c != nil {
		t.Errorf("k1 must not resolve after deactivate")
	}
	if c, _ := repo.ResolveByHash(ctx, HashKey(p2), time.Now()); c != nil {
		t.Errorf("k2 must not resolve after deactivate")
	}
	reloaded, err := repo.GetApp(ctx, app.ID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if reloaded.DevEnabled {
		t.Errorf("app must be dev_enabled=false after deactivate")
	}
}

// TestSelfServiceUsageShape: usage is aggregated by (day, face) across keys.
func TestSelfServiceUsageShape(t *testing.T) {
	cleanupSelf(t)
	svc, _, repo := newSelfService(t)
	ctx := context.Background()
	const owner = uint(1)

	app, _ := svc.CreateApp(ctx, owner, "used", "")
	// Two keys, two faces, same day — must roll up to two (day, face) rows.
	rec := NewUsageRecorder(repo, newMemStore())
	rec.Record(&Credential{KeyID: 11, ClientID: app.ID}, "catalog", 200)
	rec.Record(&Credential{KeyID: 11, ClientID: app.ID}, "catalog", 404)
	rec.Record(&Credential{KeyID: 22, ClientID: app.ID}, "catalog", 200) // same face, other key
	rec.Record(&Credential{KeyID: 11, ClientID: app.ID}, "galgame", 500)
	if err := rec.Flush(ctx); err != nil {
		t.Fatalf("flush: %v", err)
	}

	rows, err := svc.Usage(ctx, owner, app.ID, 7)
	if err != nil {
		t.Fatalf("usage: %v", err)
	}
	byFace := map[string]UsageDayFace{}
	for _, r := range rows {
		byFace[r.Face] = r
	}
	if got := byFace["catalog"]; got.Count != 3 || got.Status4xx != 1 || got.Status5xx != 0 {
		t.Errorf("catalog agg = %+v, want count 3 / 4xx 1 / 5xx 0", got)
	}
	if got := byFace["galgame"]; got.Count != 1 || got.Status5xx != 1 {
		t.Errorf("galgame agg = %+v, want count 1 / 5xx 1", got)
	}
	// Non-owner sees a 404 signal, not the data.
	if _, err := svc.Usage(ctx, uint(2), app.ID, 7); err == nil {
		t.Errorf("non-owner usage must be record-not-found")
	}
}

// --- handler-level owner guard: every endpoint 404s for a non-owner (no leak) ---

// fakeAuth injects a user_id local, standing in for middleware.Auth so the
// handler owner guard can be exercised without a real JWT.
func fakeAuth(userID uint) fiber.Handler {
	return func(c fiber.Ctx) error {
		c.Locals("user_id", userID)
		return c.Next()
	}
}

// TestSelfServiceHandlerOwnerGuard404 mounts the real routes and confirms that
// user B gets 404 on every endpoint that targets user A's app — the app is
// never distinguishable from a nonexistent one (no existence leak).
func TestSelfServiceHandlerOwnerGuard404(t *testing.T) {
	cleanupSelf(t)
	svc, _, _ := newSelfService(t)
	ctx := context.Background()
	const userA, userB = uint(1), uint(2)

	app, _ := svc.CreateApp(ctx, userA, "A app", "")
	key, _, err := svc.MintKey(ctx, userA, app.ID, MintKeyInput{Name: "k"})
	if err != nil {
		t.Fatalf("mint: %v", err)
	}

	// A Fiber app mounting the self-service routes as user B.
	fiberApp := fiber.New()
	h := NewSelfServiceHandler(svc)
	g := fiberApp.Group("/dev", fakeAuth(userB))
	h.Register(g)

	base := "/dev/apps/" + app.ID
	kbase := base + "/keys/" + fmt.Sprint(key.ID)
	cases := []struct {
		method, path string
		body         string
	}{
		{"GET", base, ""},
		{"PATCH", base, `{"name":"hacked"}`},
		{"DELETE", base, ""},
		{"POST", base + "/keys", `{"name":"x"}`},
		{"GET", base + "/keys", ""},
		{"POST", kbase + "/rotate", ""},
		{"DELETE", kbase, ""},
		{"GET", base + "/usage", ""},
	}
	for _, tc := range cases {
		var body io.Reader
		if tc.body != "" {
			body = bytes.NewBufferString(tc.body)
		}
		req := httptest.NewRequest(tc.method, tc.path, body)
		if tc.body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		resp, err := fiberApp.Test(req)
		if err != nil {
			t.Fatalf("%s %s: %v", tc.method, tc.path, err)
		}
		if resp.StatusCode != fiber.StatusNotFound {
			t.Errorf("%s %s as non-owner = %d, want 404", tc.method, tc.path, resp.StatusCode)
		}
		_ = resp.Body.Close()
	}

	// Sanity: the SAME requests as the owner (A) do NOT 404.
	ownerApp := fiber.New()
	og := ownerApp.Group("/dev", fakeAuth(userA))
	NewSelfServiceHandler(svc).Register(og)
	resp, _ := ownerApp.Test(httptest.NewRequest("GET", base, nil))
	if resp.StatusCode != fiber.StatusOK {
		t.Errorf("owner GET = %d, want 200", resp.StatusCode)
	}
	_ = resp.Body.Close()
}

// TestSelfServiceMintShowOnce: the mint response carries the plaintext once, and
// it never appears in the subsequent list (show-once + no-leak discipline).
func TestSelfServiceMintShowOnce(t *testing.T) {
	cleanupSelf(t)
	svc, _, _ := newSelfService(t)
	ctx := context.Background()
	const owner = uint(1)
	app, _ := svc.CreateApp(ctx, owner, "showonce", "")

	fiberApp := fiber.New()
	NewSelfServiceHandler(svc).Register(fiberApp.Group("/dev", fakeAuth(owner)))

	// Mint via HTTP → plaintext present in the response.
	mintReq := httptest.NewRequest("POST", "/dev/apps/"+app.ID+"/keys", bytes.NewBufferString(`{"name":"k"}`))
	mintReq.Header.Set("Content-Type", "application/json")
	resp, err := fiberApp.Test(mintReq)
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	var minted struct {
		Data struct {
			Key       string `json:"key"`
			KeyPrefix string `json:"key_prefix"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &minted); err != nil {
		t.Fatalf("decode mint: %v", err)
	}
	if !HasKeyPrefix(minted.Data.Key) {
		t.Fatalf("mint response missing plaintext key, got %q", minted.Data.Key)
	}
	plaintext := minted.Data.Key

	// List via HTTP → no plaintext, only prefix/last4.
	lresp, err := fiberApp.Test(httptest.NewRequest("GET", "/dev/apps/"+app.ID+"/keys", nil))
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	raw, _ := io.ReadAll(lresp.Body)
	_ = lresp.Body.Close()
	if bytes.Contains(raw, []byte(plaintext)) {
		t.Errorf("list response leaks the plaintext key")
	}
	if !bytes.Contains(raw, []byte(minted.Data.KeyPrefix)) {
		t.Errorf("list response should carry the key prefix %q", minted.Data.KeyPrefix)
	}
}
