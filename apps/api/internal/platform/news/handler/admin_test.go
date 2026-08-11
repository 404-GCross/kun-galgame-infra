package handler

import (
	"context"
	"net/http"
	"testing"

	"api/internal/platform/news/model"
	siteModel "api/internal/platform/site/model"

	"github.com/gofiber/fiber/v3"
)

type stubClients struct {
	client *siteModel.OAuthClient
	err    error
}

func (s stubClients) FindByClientID(context.Context, string) (*siteModel.OAuthClient, error) {
	return s.client, s.err
}

func mountAdmin(clients OAuthClientLookup, clientID string, roles []string) *fiber.App {
	app := fiber.New()
	app.Use(AdminPrefix, func(c fiber.Ctx) error {
		c.Locals("token_client_id", clientID)
		c.Locals("user_roles", roles)
		c.Locals("user_id", uint(1))
		return c.Next()
	}, AdminGate(clients))
	h := NewAdminHandler(nil)
	g := app.Group(AdminPrefix)
	g.Get("/items", h.Queue)
	return app
}

// TestThirdPartyClientIsNotAModerationSurface: an access token minted for a
// developer's app must not reach the queue even if its holder is a moderator.
// The moderation console is ours, not something a third-party app speaks for.
func TestThirdPartyClientIsNotAModerationSurface(t *testing.T) {
	owner := uint(4242)
	third := stubClients{client: &siteModel.OAuthClient{OwnerUserID: &owner}}
	app := mountAdmin(third, "some-third-party", []string{"moderator"})
	status, body := do(t, app, AdminPrefix+"/items")
	if status != http.StatusForbidden {
		t.Errorf("a third-party client reached the queue: %d %s", status, body)
	}

	first := stubClients{client: &siteModel.OAuthClient{}}
	app = mountAdmin(first, "first-party", []string{"moderator"})
	if status, body := do(t, app, AdminPrefix+"/items"); status == http.StatusForbidden {
		t.Errorf("a first-party moderator was refused: %d %s", status, body)
	}
}

func TestUnregisteredClientIsRefused(t *testing.T) {
	app := mountAdmin(stubClients{}, "ghost", []string{"moderator"})
	if status, _ := do(t, app, AdminPrefix+"/items"); status != http.StatusForbidden {
		t.Errorf("an unregistered client got %d, want 403", status)
	}
}

func TestQueueNeedsTheReviewPermission(t *testing.T) {
	first := stubClients{client: &siteModel.OAuthClient{}}
	for _, role := range []string{"user", "creator", ""} {
		app := mountAdmin(first, "first-party", []string{role})
		if status, _ := do(t, app, AdminPrefix+"/items"); status == http.StatusOK {
			t.Errorf("role %q reached the moderation queue", role)
		}
	}
}

// TestAdminFaceIsNotDegradedToAnEmptyQueue: the public face answers 503 when
// kun_news is unreachable so a consumer cannot mistake an outage for "no news".
// The same reasoning is sharper here — an empty moderation queue is the one
// answer nobody double-checks.
func TestAdminFaceIsNotDegradedToAnEmptyQueue(t *testing.T) {
	first := stubClients{client: &siteModel.OAuthClient{}}
	app := mountAdmin(first, "first-party", []string{"moderator"})
	status, body := do(t, app, AdminPrefix+"/items")
	if status != http.StatusServiceUnavailable {
		t.Errorf("GET items with no service = %d, want 503 (body %s)", status, body)
	}
}

func TestParseStatusesRejectsUnknown(t *testing.T) {
	if _, ok := parseStatuses("9"); ok {
		t.Error("status 9 must be rejected, not silently filtered on")
	}
	if _, ok := parseStatuses("0,abc"); ok {
		t.Error("a non-numeric status must be rejected")
	}
	got, ok := parseStatuses("0,3")
	if !ok || len(got) != 2 || got[0] != model.StatusPending || got[1] != model.StatusWithdrawn {
		t.Errorf("parseStatuses(0,3) = %v,%v", got, ok)
	}
	if got, ok := parseStatuses(""); !ok || got != nil {
		t.Errorf("no status filter must mean every status, got %v", got)
	}
}

func TestAdminPagingParams(t *testing.T) {
	if _, ok := parseAdminLimit("201"); ok {
		t.Error("limit 201 must be rejected")
	}
	if n, ok := parseAdminLimit(""); !ok || n != 50 {
		t.Errorf("default limit = %d,%v", n, ok)
	}
	if _, ok := parseOffset("-1"); ok {
		t.Error("a negative offset must be rejected")
	}
	if n, ok := parseOffset(""); !ok || n != 0 {
		t.Errorf("default offset = %d,%v", n, ok)
	}
	if _, ok := parseID("0"); ok {
		t.Error("id 0 must be rejected")
	}
}
