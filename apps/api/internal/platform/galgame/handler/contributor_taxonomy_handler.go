// contributor_taxonomy_handler.go — the taxonomy picker on the surviving
// `/internal` platform-workflow face (A2-1g).
//
// Why a second door onto the same query. The galgame SUBMISSION form needs to
// resolve tags / officials / engines / series to wiki ids — the same ids its
// write payload carries. A2-1e area B gave that lookup a home, but on the
// staff face behind galgame.taxonomy.edit_any, so an ordinary contributor
// filling in the submission form gets a 403 from the only lane that can answer
// the question. The forum currently fails loudly against the staff face rather
// than degrade, which is the right call and also an unusable form.
//
// So: same query, contributor gate. The trust level is the one the /internal
// write and propose faces already set — a signed-in user, dual-credentialed
// (client key + Bearer JWT). Nothing here is more sensitive than what the
// submission form is about to let the same user WRITE: these are the names of
// public taxonomy rows, and the deprecated public face served them anonymously
// until W5 retired it.
//
// The gate difference is the ONLY difference. The families, the term splitting,
// the {id, name} row shape and the cap all come from service.TaxonomyPicker, so
// a picker component pointed at either door behaves identically.
package handler

import (
	"api/internal/platform/galgame/service"
	"api/pkg/errors"
	"api/pkg/response"

	"github.com/gofiber/fiber/v3"
)

// ContributorTaxonomyHandler serves the submission form's taxonomy picker.
type ContributorTaxonomyHandler struct {
	picker *service.TaxonomyPicker
}

// NewContributorTaxonomyHandler wires the contributor picker over the shared
// query.
func NewContributorTaxonomyHandler(picker *service.TaxonomyPicker) *ContributorTaxonomyHandler {
	return &ContributorTaxonomyHandler{picker: picker}
}

// Search serves GET /internal/galgame/taxonomy/{family}/search?q= — picker rows
// for a signed-in contributor. family is the closed set tag|official|engine|
// series; anything else is a 400 (our own vocabulary, so a typo is a caller
// mistake worth failing loudly, not an empty list that looks like "no matches").
func (h *ContributorTaxonomyHandler) Search(c fiber.Ctx) error {
	if userID, _ := c.Locals("user_id").(uint); userID == 0 {
		return response.Unauthorized(c, errors.ErrAuthUnauthorized)
	}
	family := c.Params("family")
	items, ok, err := h.picker.Search(c.Context(), family,
		service.TaxonomySearchTerms(c.Query("q")), service.TaxonomyPickerLimit)
	if !ok {
		return response.BadRequestMsg(c, errors.ErrInvalidParam,
			"family must be one of tag, official, engine, series")
	}
	if err != nil {
		return response.InternalError(c, errors.ErrOperationFailed)
	}
	// Same envelope as the staff door's rows, verbatim — a consumer switching
	// doors changes the URL and nothing else.
	return response.Success(c, staffList(items))
}
