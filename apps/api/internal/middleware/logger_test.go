package middleware

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v3"
)

func logStatusOf(t *testing.T, register func(app *fiber.App), method, path string) (wire int, logged int) {
	t.Helper()

	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })

	app := fiber.New(fiber.Config{
		ErrorHandler: func(c fiber.Ctx, err error) error {
			code := fiber.StatusInternalServerError
			var fe *fiber.Error
			if errors.As(err, &fe) {
				code = fe.Code
			}
			return c.Status(code).JSON(fiber.Map{"code": code})
		},
	})
	app.Use(Logger())
	register(app)

	resp, err := app.Test(httptest.NewRequest(method, path, nil))
	if err != nil {
		t.Fatalf("test request: %v", err)
	}
	defer resp.Body.Close()

	dec := json.NewDecoder(bytes.NewReader(buf.Bytes()))
	for {
		var line struct {
			Msg    string `json:"msg"`
			Status int    `json:"status"`
		}
		if err := dec.Decode(&line); err != nil {
			break
		}
		if line.Msg == "request completed" {
			logged = line.Status
		}
	}
	return resp.StatusCode, logged
}

func TestLoggerRecordsUnmatchedRouteAs404(t *testing.T) {
	wire, logged := logStatusOf(t, func(app *fiber.App) {
		app.Get("/alive", func(c fiber.Ctx) error { return c.SendString("ok") })
	}, http.MethodGet, "/retired/face")

	if wire != http.StatusNotFound {
		t.Fatalf("client status = %d, want 404", wire)
	}
	if logged != http.StatusNotFound {
		t.Errorf("logged status = %d, want 404 — an unmatched route must not read as a success", logged)
	}
}

func TestLoggerRecordsHandlerErrorStatus(t *testing.T) {
	t.Run("fiber error keeps its code", func(t *testing.T) {
		wire, logged := logStatusOf(t, func(app *fiber.App) {
			app.Get("/gone", func(c fiber.Ctx) error {
				return fiber.NewError(fiber.StatusGone, "retired")
			})
		}, http.MethodGet, "/gone")

		if wire != http.StatusGone || logged != http.StatusGone {
			t.Errorf("wire = %d logged = %d, want 410/410", wire, logged)
		}
	})

	t.Run("plain error becomes 500", func(t *testing.T) {
		wire, logged := logStatusOf(t, func(app *fiber.App) {
			app.Get("/boom", func(c fiber.Ctx) error { return errors.New("boom") })
		}, http.MethodGet, "/boom")

		if wire != http.StatusInternalServerError || logged != http.StatusInternalServerError {
			t.Errorf("wire = %d logged = %d, want 500/500", wire, logged)
		}
	})
}

func TestLoggerKeepsHandlerWrittenStatus(t *testing.T) {
	wire, logged := logStatusOf(t, func(app *fiber.App) {
		app.Get("/missing", func(c fiber.Ctx) error {
			return c.Status(http.StatusNotFound).JSON(fiber.Map{"code": 404})
		})
	}, http.MethodGet, "/missing")

	if wire != http.StatusNotFound || logged != http.StatusNotFound {
		t.Errorf("wire = %d logged = %d, want 404/404", wire, logged)
	}
}
