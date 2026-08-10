package handler

import (
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"
)

func TestSpecExport(t *testing.T) {
	api := Setup(fiber.New(), nil, nil, nil, nil, nil)
	b, err := api.OpenAPI().YAML()
	if err != nil {
		t.Fatalf("marshal spec: %v", err)
	}
	spec := string(b)
	for _, want := range []string{
		"/api/v1/trust/reports",
		"/api/v1/trust/subject-kinds",
		"/api/v1/trust/subject-kinds/ensure",
		"/api/v1/trust/forward",
		"/api/v1/trust/forward/resolve",
		"/api/v1/trust/scan",
		"/api/v1/trust/check",
		"operationId: submitReport",
		"operationId: forwardReviewItem",
		"operationId: submitScan",
		"operationId: checkText",
		"operationId: ensureSubjectKinds",
		"subject_kind",
		"reason_key",
	} {
		if !strings.Contains(spec, want) {
			t.Errorf("S2S spec missing %q", want)
		}
	}
}

func TestAdminSpecExport(t *testing.T) {
	api := SetupAdmin(fiber.New(), nil, nil, nil, nil, nil, nil)
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
		"/api/v1/admin/trust/subject-kinds/batch",
		"/api/v1/admin/trust/report-reasons",
		"/api/v1/admin/trust/dispositions",
		"/api/v1/admin/trust/terms",
		"/api/v1/admin/trust/terms/{id}/deprecate",
		"operationId: decideTrustReviewItem",
		"operationId: batchTrustSubjectKinds",
		"operationId: createTrustTerm",
		"operationId: deprecateTrustTerm",
	} {
		if !strings.Contains(spec, want) {
			t.Errorf("admin spec missing %q", want)
		}
	}
}
