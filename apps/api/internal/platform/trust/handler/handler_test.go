package handler

import (
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"
)

// TestSpecExport is the S2S spec smoke: Setup with nil deps (the gen-openapi
// path) must produce a valid OpenAPI document with the intake operations.
func TestSpecExport(t *testing.T) {
	api := Setup(fiber.New(), nil, nil)
	b, err := api.OpenAPI().YAML()
	if err != nil {
		t.Fatalf("marshal spec: %v", err)
	}
	spec := string(b)
	for _, want := range []string{
		"/api/v1/trust/reports",
		"/api/v1/trust/subject-kinds",
		"operationId: submitReport",
		"subject_kind",
		"reason_key",
	} {
		if !strings.Contains(spec, want) {
			t.Errorf("S2S spec missing %q", want)
		}
	}
}

// TestAdminSpecExport is the admin spec smoke.
func TestAdminSpecExport(t *testing.T) {
	api := SetupAdmin(fiber.New(), nil, nil, nil)
	b, err := api.OpenAPI().YAML()
	if err != nil {
		t.Fatalf("marshal admin spec: %v", err)
	}
	spec := string(b)
	for _, want := range []string{
		"/api/v1/admin/trust/review-items",
		"/api/v1/admin/trust/review-items/{id}/claim",
		"/api/v1/admin/trust/review-items/{id}/decide",
		"/api/v1/admin/trust/subject-kinds",
		"/api/v1/admin/trust/report-reasons",
		"/api/v1/admin/trust/dispositions",
		"operationId: decideTrustReviewItem",
	} {
		if !strings.Contains(spec, want) {
			t.Errorf("admin spec missing %q", want)
		}
	}
}
