// staff_taxonomy_handler.go — the taxonomy READ-BACK ops on the surviving
// `/api` staff face (A2-1e area B; doc 106 R5 + R11).
//
// Why this exists. The `/api` face has carried taxonomy CREATE / UPDATE /
// DELETE / REVERT since the W3 surgery, but not a single GET: the reads moved
// to `/internal` in wave 05 and then retired to `/v1` in W5. The two admin
// consoles were therefore left prefilling their edit forms from whatever a
// LIST row happens to carry — and because the update payload is a WHOLESALE
// replacement (a present field replaces, an absent one keeps), every field the
// console cannot read back is a field it silently ERASES on save. Both
// consoles wipe `alias` on every taxonomy edit today for exactly this reason.
//
// So each family gets a pair:
//
//	GET /api/<family>/search?q=…   identity rows, for the picker
//	GET /api/<family>/{id}         the full editable record, for the form
//
// and the record's field set is precisely the matching Update*Request's
// editable set (see dto/staff_taxonomy_dto.go). Read what you may write, write
// what you read.
//
// Face conventions, all inherited rather than invented:
//   - ids are WIKI ids end to end (R11) — the same key space the write ops and
//     the revision history use. The public browse lane's migration to catalog
//     ids (P2/R1) deliberately does not reach into this lane;
//   - jwtAuth + galgame.taxonomy.edit_any, the SAME gate the update ops
//     enforce: this is edit-form supply, so anyone who may read it is by
//     definition someone who may write it (`POST /api/tag` is the one looser
//     route, and it is a create, not a read-back);
//   - `response.Success` envelope, `{items,total}` for lists — the `/api` and
//     `/internal` convention;
//   - no Huma registration: `/api` is staff-only and carries no published spec.
package handler

import (
	"encoding/json"
	"strconv"

	"api/internal/platform/galgame/dto"
	"api/internal/platform/galgame/perm"
	"api/internal/platform/galgame/repository"
	"api/internal/platform/galgame/service"
	"api/pkg/errors"
	"api/pkg/response"

	"github.com/gofiber/fiber/v3"
	"gorm.io/gorm"
)

// StaffTaxonomyHandler serves the read-back pairs over the four taxonomy
// repositories. The DETAIL ops read their repository directly (each record has
// its own field set); the SEARCH ops delegate to service.TaxonomyPicker, which
// the contributor-facing /internal door shares — one query, two gates, so the
// two pickers cannot drift (A2-1g).
type StaffTaxonomyHandler struct {
	tagRepo      *repository.TagRepository
	officialRepo *repository.OfficialRepository
	engineRepo   *repository.EngineRepository
	seriesRepo   *repository.SeriesRepository
	picker       *service.TaxonomyPicker
}

// NewStaffTaxonomyHandler wires the staff taxonomy read-back handler.
func NewStaffTaxonomyHandler(
	tagRepo *repository.TagRepository,
	officialRepo *repository.OfficialRepository,
	engineRepo *repository.EngineRepository,
	seriesRepo *repository.SeriesRepository,
	picker *service.TaxonomyPicker,
) *StaffTaxonomyHandler {
	return &StaffTaxonomyHandler{
		tagRepo: tagRepo, officialRepo: officialRepo,
		engineRepo: engineRepo, seriesRepo: seriesRepo, picker: picker,
	}
}

// staffFamilySearch is the shared body of the four staff search ops: enforce
// the editor gate, then run the SAME picker query the contributor door runs.
func (h *StaffTaxonomyHandler) staffFamilySearch(c fiber.Ctx, family string) error {
	if err := requireTaxonomyEditor(c); err != nil {
		return err
	}
	items, _, err := h.picker.Search(c.Context(), family,
		service.TaxonomySearchTerms(c.Query("q")), service.TaxonomyPickerLimit)
	if err != nil {
		return response.InternalError(c, errors.ErrOperationFailed)
	}
	return response.Success(c, staffList(items))
}

// ── tag ─────────────────────────────────────────────────────────────────────

// TagSearch serves GET /api/tag/search?q= — identity rows for the picker.
func (h *StaffTaxonomyHandler) TagSearch(c fiber.Ctx) error {
	return h.staffFamilySearch(c, service.TaxonomyFamilyTag)
}

// TagDetail serves GET /api/tag/{id} — the tag edit form's full read-back.
func (h *StaffTaxonomyHandler) TagDetail(c fiber.Ctx) error {
	if err := requireTaxonomyEditor(c); err != nil {
		return err
	}
	id, ok := staffID(c)
	if !ok {
		return response.BadRequest(c, errors.ErrInvalidID)
	}
	row, err := h.tagRepo.FindByID(c.Context(), id)
	if err != nil {
		return staffReadError(c, err)
	}
	aliases := make([]string, 0, len(row.Alias))
	for _, a := range row.Alias {
		aliases = append(aliases, a.Name)
	}
	return response.Success(c, dto.StaffTagRecord{
		ID: row.ID, Name: row.Name, Category: row.Category,
		Description: row.Description, Alias: aliases,
	})
}

// ── official ────────────────────────────────────────────────────────────────

// OfficialSearch serves GET /api/official/search?q=.
func (h *StaffTaxonomyHandler) OfficialSearch(c fiber.Ctx) error {
	return h.staffFamilySearch(c, service.TaxonomyFamilyOfficial)
}

// OfficialDetail serves GET /api/official/{id} — the 会社 edit form's read-back.
// `original` and `description` are the two fields no list row carries, and the
// reason the forum console already pays for a detail round-trip here.
func (h *StaffTaxonomyHandler) OfficialDetail(c fiber.Ctx) error {
	if err := requireTaxonomyEditor(c); err != nil {
		return err
	}
	id, ok := staffID(c)
	if !ok {
		return response.BadRequest(c, errors.ErrInvalidID)
	}
	row, err := h.officialRepo.FindByID(c.Context(), id)
	if err != nil {
		return staffReadError(c, err)
	}
	aliases := make([]string, 0, len(row.Alias))
	for _, a := range row.Alias {
		aliases = append(aliases, a.Name)
	}
	return response.Success(c, dto.StaffOfficialRecord{
		ID: row.ID, Name: row.Name, Original: row.Original, Link: row.Link,
		Lang: row.Lang, Category: row.Category, Description: row.Description,
		Alias: aliases,
	})
}

// ── engine ──────────────────────────────────────────────────────────────────

// EngineSearch serves GET /api/engine/search?q=. An empty q returns the WHOLE
// facet (a few hundred rows, capped) rather than nothing: both consoles
// hydrate their engine picker from the flat list, and refusing to serve it
// would just push them back to the deprecated public lane.
func (h *StaffTaxonomyHandler) EngineSearch(c fiber.Ctx) error {
	return h.staffFamilySearch(c, service.TaxonomyFamilyEngine)
}

// EngineDetail serves GET /api/engine/{id}.
func (h *StaffTaxonomyHandler) EngineDetail(c fiber.Ctx) error {
	if err := requireTaxonomyEditor(c); err != nil {
		return err
	}
	id, ok := staffID(c)
	if !ok {
		return response.BadRequest(c, errors.ErrInvalidID)
	}
	row, err := h.engineRepo.FindByID(c.Context(), id)
	if err != nil {
		return staffReadError(c, err)
	}
	return response.Success(c, dto.StaffEngineRecord{
		ID: row.ID, Name: row.Name, Description: row.Description,
		Alias: staffEngineAliases(row.Alias),
	})
}

// ── series ──────────────────────────────────────────────────────────────────

// SeriesSearch serves GET /api/series/search?q=. Empty q = the whole facet,
// same rationale as EngineSearch.
func (h *StaffTaxonomyHandler) SeriesSearch(c fiber.Ctx) error {
	return h.staffFamilySearch(c, service.TaxonomyFamilySeries)
}

// SeriesDetail serves GET /api/series/{id}. galgame_ids is the membership the
// update op replaces wholesale, so a form that cannot read it back empties the
// series on save.
//
// The membership is read with NO content_limit filter on purpose: this is an
// editorial view of what the series contains, not a browsing view, and hiding
// half the members from the editor would make every save drop them.
func (h *StaffTaxonomyHandler) SeriesDetail(c fiber.Ctx) error {
	if err := requireTaxonomyEditor(c); err != nil {
		return err
	}
	id, ok := staffID(c)
	if !ok {
		return response.BadRequest(c, errors.ErrInvalidID)
	}
	row, err := h.seriesRepo.FindByID(c.Context(), id, "")
	if err != nil {
		return staffReadError(c, err)
	}
	ids := make([]int, 0, len(row.Galgame))
	for _, g := range row.Galgame {
		ids = append(ids, g.ID)
	}
	return response.Success(c, dto.StaffSeriesRecord{
		ID: row.ID, Name: row.Name, Description: row.Description, GalgameIDs: ids,
	})
}

// ── shared helpers ──────────────────────────────────────────────────────────

// requireTaxonomyEditor enforces the same gate the taxonomy WRITE ops enforce
// in-handler: a valid JWT plus galgame.taxonomy.edit_any. Returning the error
// response directly keeps every op's preamble one line.
func requireTaxonomyEditor(c fiber.Ctx) error {
	userID, _ := c.Locals("user_id").(uint)
	if userID == 0 {
		return response.Unauthorized(c, errors.ErrAuthUnauthorized)
	}
	roles, _ := c.Locals("user_roles").([]string)
	if !perm.Resolver.Can(roles, perm.TaxonomyEditAny) {
		return response.Forbidden(c, errors.ErrForbidden)
	}
	return nil
}

// staffID parses the {id} path param; ok=false for anything that is not a
// positive integer (a 400, never a degraded lookup of id 0).
func staffID(c fiber.Ctx) (int, bool) {
	id, err := strconv.Atoi(c.Params("id"))
	if err != nil || id <= 0 {
		return 0, false
	}
	return id, true
}

// staffList wraps rows in the face's list envelope. total is the returned row
// count: these lanes are capped pickers, not paged lanes, so promising a
// whole-set total would be a number no parameter can page through.
func staffList(items []dto.StaffTaxonomyListItem) fiber.Map {
	if items == nil {
		items = []dto.StaffTaxonomyListItem{}
	}
	return fiber.Map{"items": items, "total": len(items)}
}

// staffEngineAliases parses the engine's inline jsonb alias array to a
// non-nil []string (a malformed / absent column yields []) — the same
// projection the public engine face applies, kept local so this staff-only
// handler adds no dependency on the public taxonomy service.
func staffEngineAliases(raw []byte) []string {
	out := []string{}
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &out)
	}
	if out == nil {
		out = []string{}
	}
	return out
}

// staffReadError maps a repository read failure: a missing row is a 404, and
// anything else is a 500.
func staffReadError(c fiber.Ctx, err error) error {
	if err == gorm.ErrRecordNotFound {
		return response.NotFound(c, errors.ErrNotFound)
	}
	return response.InternalError(c, errors.ErrOperationFailed)
}
