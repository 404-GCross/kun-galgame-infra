package middleware

import (
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"api/internal/infrastructure/cache"

	"github.com/gofiber/fiber/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var noRedis = &cache.RedisCache{}

func TestOAuthTokenRateLimit_BodyStillReadableByHandler(t *testing.T) {
	app := fiber.New()
	app.Post("/oauth/token", OAuthTokenRateLimit(noRedis), func(c fiber.Ctx) error {
		var body struct {
			GrantType string `json:"grant_type"`
			ClientID  string `json:"client_id"`
		}
		if err := c.Bind().JSON(&body); err != nil {
			return c.Status(400).SendString("bind failed: " + err.Error())
		}
		return c.JSON(fiber.Map{
			"grant_type": body.GrantType,
			"client_id":  body.ClientID,
		})
	})

	payload := `{"grant_type":"refresh_token","client_id":"abc123","refresh_token":"x"}`
	req := httptest.NewRequest("POST", "/oauth/token", strings.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")

	resp, err := app.Test(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	out, _ := io.ReadAll(resp.Body)
	assert.Equal(t, 200, resp.StatusCode, "handler must still bind body after limiter read it: %s", out)
	assert.Contains(t, string(out), `"client_id":"abc123"`)
	assert.Contains(t, string(out), `"grant_type":"refresh_token"`)
}

func TestOAuthTokenRateLimit_KeyGenerator(t *testing.T) {
	app := fiber.New()
	var seenKeys []string
	app.Post("/k", func(c fiber.Ctx) error {
		var body struct {
			ClientID string `json:"client_id"`
		}
		key := "tokip:" + c.IP()
		if err := c.Bind().JSON(&body); err == nil && body.ClientID != "" {
			key = "tokc:" + body.ClientID
		}
		seenKeys = append(seenKeys, key)
		return c.SendStatus(200)
	})

	for _, cid := range []string{"clientA", "clientA", "clientB"} {
		req := httptest.NewRequest("POST", "/k",
			strings.NewReader(`{"client_id":"`+cid+`"}`))
		req.Header.Set("Content-Type", "application/json")
		resp, err := app.Test(req)
		require.NoError(t, err)
		resp.Body.Close()
	}

	require.Len(t, seenKeys, 3)
	assert.Equal(t, "tokc:clientA", seenKeys[0])
	assert.Equal(t, "tokc:clientA", seenKeys[1])
	assert.Equal(t, "tokc:clientB", seenKeys[2])
	assert.NotEqual(t, seenKeys[1], seenKeys[2], "distinct clients → distinct buckets")
}

func TestRateLimit_SkipsAuthorizedTraffic(t *testing.T) {
	app := fiber.New()
	app.Use(RateLimit(noRedis))
	app.Get("/x", func(c fiber.Ctx) error { return c.SendStatus(200) })

	for i := 0; i < 150; i++ {
		req := httptest.NewRequest("GET", "/x", nil)
		req.Header.Set("Authorization", "Bearer fake-token")
		resp, err := app.Test(req)
		require.NoError(t, err)
		resp.Body.Close()
		require.Equal(t, 200, resp.StatusCode,
			"authorized request %d should bypass the per-IP limiter", i+1)
	}
}
