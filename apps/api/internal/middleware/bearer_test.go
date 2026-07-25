package middleware

import (
	"encoding/json"
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"
)

// TestSplitBearerSchemeIsCaseInsensitive pins RFC 7235 §2.1: the auth-scheme is
// case-insensitive. This is not pedantry — a standard OIDC client sending
// `bearer <token>` used to be rejected here as though its TOKEN were bad, which
// is close to undiagnosable from the far side of an integration.
func TestSplitBearerSchemeIsCaseInsensitive(t *testing.T) {
	cases := []struct {
		name      string
		header    string
		wantToken string
		wantOK    bool
	}{
		{"canonical", "Bearer abc", "abc", true},
		{"lowercase", "bearer abc", "abc", true},
		{"uppercase", "BEARER abc", "abc", true},
		{"mixed case", "BeArEr abc", "abc", true},
		{"wrong scheme", "Basic abc", "", false},
		{"scheme only, no space", "Bearer", "", false},
		{"scheme with empty credentials", "Bearer ", "", true}, // ok=true, token="" — callers reject
		{"empty header", "", "", false},
		{"token containing spaces is kept whole", "Bearer a b", "a b", true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			token, ok := splitBearer(tc.header)
			if ok != tc.wantOK || token != tc.wantToken {
				t.Fatalf("splitBearer(%q) = (%q, %v), want (%q, %v)",
					tc.header, token, ok, tc.wantToken, tc.wantOK)
			}
		})
	}
}

// TestBearerErrorShape pins the RFC 6750 §3 error format: every rejection
// carries a WWW-Authenticate challenge, and a request that presented no
// credentials at all gets a challenge with NO error code and no body (§3, "the
// resource server SHOULD NOT include an error code").
func TestBearerErrorShape(t *testing.T) {
	t.Run("no credentials: bare challenge, no error code, no body", func(t *testing.T) {
		app := fiber.New()
		app.Get("/x", func(c fiber.Ctx) error {
			return BearerError(c, fiber.StatusUnauthorized, "", "")
		})

		resp, err := app.Test(httptest.NewRequest("GET", "/x", nil))
		if err != nil {
			t.Fatalf("request: %v", err)
		}
		if resp.StatusCode != fiber.StatusUnauthorized {
			t.Fatalf("status = %d, want 401", resp.StatusCode)
		}
		challenge := resp.Header.Get("WWW-Authenticate")
		if challenge != `Bearer realm="kungal"` {
			t.Fatalf("challenge = %q", challenge)
		}
		if strings.Contains(challenge, "error=") {
			t.Fatal("a credential-less request must not be given an error code")
		}
		body, _ := io.ReadAll(resp.Body)
		if len(body) != 0 {
			t.Fatalf("expected an empty body, got %q", body)
		}
	})

	t.Run("invalid_token: challenge + matching JSON body", func(t *testing.T) {
		app := fiber.New()
		app.Get("/x", func(c fiber.Ctx) error {
			return BearerError(c, fiber.StatusUnauthorized, "invalid_token", "token expired")
		})

		resp, err := app.Test(httptest.NewRequest("GET", "/x", nil))
		if err != nil {
			t.Fatalf("request: %v", err)
		}
		if resp.StatusCode != fiber.StatusUnauthorized {
			t.Fatalf("status = %d, want 401", resp.StatusCode)
		}

		challenge := resp.Header.Get("WWW-Authenticate")
		for _, want := range []string{`realm="kungal"`, `error="invalid_token"`, `error_description="token expired"`} {
			if !strings.Contains(challenge, want) {
				t.Fatalf("challenge %q is missing %s", challenge, want)
			}
		}

		var got map[string]string
		body, _ := io.ReadAll(resp.Body)
		if err := json.Unmarshal(body, &got); err != nil {
			t.Fatalf("body %q is not JSON: %v", body, err)
		}
		if got["error"] != "invalid_token" || got["error_description"] != "token expired" {
			t.Fatalf("body = %v", got)
		}
		// The house envelope must never leak onto a protocol endpoint.
		if _, ok := got["code"]; ok {
			t.Fatal(`RFC 6750 body must not carry the house "code" key`)
		}
	})

	// A description containing a quote would otherwise break the header grammar
	// and could smuggle extra auth-params into the challenge.
	t.Run("quotes in the description are escaped", func(t *testing.T) {
		app := fiber.New()
		app.Get("/x", func(c fiber.Ctx) error {
			return BearerError(c, fiber.StatusUnauthorized, "invalid_token", `he said "hi"`)
		})

		resp, err := app.Test(httptest.NewRequest("GET", "/x", nil))
		if err != nil {
			t.Fatalf("request: %v", err)
		}
		challenge := resp.Header.Get("WWW-Authenticate")
		if !strings.Contains(challenge, `error_description="he said \"hi\""`) {
			t.Fatalf("quotes were not escaped: %q", challenge)
		}
	})
}
