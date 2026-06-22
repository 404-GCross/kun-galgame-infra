package handler

import (
	"encoding/json"
	"strings"
	"testing"

	"api/internal/platform/artifact/service"

	"github.com/gofiber/fiber/v3"
)

// TestOpenAPIGeneration proves Setup wires the Huma API and emits an OpenAPI 3.1
// spec covering all six artifact operations. The service is constructed with nil
// deps — Setup only registers operations (handlers are never invoked here), so no
// DB/S3/Redis is needed to generate the spec.
func TestOpenAPIGeneration(t *testing.T) {
	app := fiber.New()
	svc := service.New(nil, nil, nil, service.Options{})
	api := Setup(app, svc, true)

	spec, err := json.Marshal(api.OpenAPI())
	if err != nil {
		t.Fatalf("marshal openapi: %v", err)
	}
	s := string(spec)

	if !strings.Contains(s, `"openapi":"3.1`) {
		t.Errorf("spec is not OpenAPI 3.1; got prefix: %.40s", s)
	}

	for _, op := range []string{
		"listArtifacts", "getArtifact", "downloadArtifact",
		"deleteArtifact", "initUpload", "completeUpload",
	} {
		if !strings.Contains(s, op) {
			t.Errorf("operationId %q missing from spec", op)
		}
	}

	for _, path := range []string{
		"/api/v1/artifacts",
		"/api/v1/artifacts/{uuid}",
		"/api/v1/artifacts/{uuid}/download",
		"/api/v1/artifacts/{uuid}/complete",
	} {
		if !strings.Contains(s, path) {
			t.Errorf("path %q missing from spec", path)
		}
	}

	// The house envelope must appear in the success response schemas.
	if !strings.Contains(s, `"code"`) || !strings.Contains(s, `"message"`) {
		t.Errorf("spec does not reference the house envelope {code,message}")
	}
}
