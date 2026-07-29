// taxonomy_picker.go — the ONE taxonomy picker query, shared by both doors
// that serve it (A2-1g).
//
// Two faces need "find me the taxonomy rows matching this text, as {id, name}
// in the wiki id space":
//
//	GET /api/<family>/search            staff edit forms (A2-1e area B) —
//	                                    gated on galgame.taxonomy.edit_any
//	GET /internal/galgame/taxonomy/
//	    {family}/search                 the CONTRIBUTOR submission form —
//	                                    any signed-in user
//
// They differ in exactly one thing: who may call them. Everything else — the
// families, the term splitting, the row shape, the cap — must stay identical,
// because the same frontend picker component renders both and a drift between
// them shows up as "the selector works for moderators and misbehaves for
// everyone else". So the query lives here, once, and the two handlers are
// nothing but a gate plus a mount point.
package service

import (
	"context"
	"strings"

	"api/internal/platform/galgame/dto"
	"api/internal/platform/galgame/repository"
)

// Taxonomy picker families — the closed vocabulary both faces accept.
const (
	TaxonomyFamilyTag      = "tag"
	TaxonomyFamilyOfficial = "official"
	TaxonomyFamilyEngine   = "engine"
	TaxonomyFamilySeries   = "series"
)

// TaxonomyPickerLimit caps a picker response. The four families run 146-3,037
// rows, so this is a response-size guard rather than a paging scheme: the
// caller narrows with `q`, it never walks pages here.
const TaxonomyPickerLimit = 50

// TaxonomyPicker resolves picker rows over the four taxonomy repositories.
// Pure reads: no revision, no search hook, no write path to coordinate with.
type TaxonomyPicker struct {
	tagRepo      *repository.TagRepository
	officialRepo *repository.OfficialRepository
	engineRepo   *repository.EngineRepository
	seriesRepo   *repository.SeriesRepository
}

// NewTaxonomyPicker wires the shared picker.
func NewTaxonomyPicker(
	tagRepo *repository.TagRepository,
	officialRepo *repository.OfficialRepository,
	engineRepo *repository.EngineRepository,
	seriesRepo *repository.SeriesRepository,
) *TaxonomyPicker {
	return &TaxonomyPicker{
		tagRepo: tagRepo, officialRepo: officialRepo,
		engineRepo: engineRepo, seriesRepo: seriesRepo,
	}
}

// IsTaxonomyFamily reports whether a token names one of the four families.
func IsTaxonomyFamily(family string) bool {
	switch family {
	case TaxonomyFamilyTag, TaxonomyFamilyOfficial, TaxonomyFamilyEngine, TaxonomyFamilySeries:
		return true
	default:
		return false
	}
}

// TaxonomySearchTerms splits a raw `q` into the whitespace-separated terms the
// repositories AND together. Exported so both handlers split identically.
func TaxonomySearchTerms(raw string) []string {
	return strings.Fields(strings.TrimSpace(raw))
}

// Search resolves one family's picker rows. Returns ok=false for a family
// outside the vocabulary (the handler turns that into a 400).
//
// Blank-query behaviour differs by family, and deliberately so — it is a
// property of the FACET, not of the caller:
//
//   - tag / official are open-ended vocabularies (3,037 / 24,334 rows). An
//     unfiltered dump is meaningless to a picker, so a blank query is an empty
//     list: type something.
//   - engine / series are small curated sets (189 / 146) that every console
//     hydrates as a flat list. Refusing to serve them unfiltered would just
//     push the caller back onto the deprecated public lane.
func (p *TaxonomyPicker) Search(ctx context.Context, family string, terms []string, limit int) ([]dto.StaffTaxonomyListItem, bool, error) {
	if limit <= 0 {
		limit = TaxonomyPickerLimit
	}
	switch family {
	case TaxonomyFamilyTag:
		if len(terms) == 0 {
			return []dto.StaffTaxonomyListItem{}, true, nil
		}
		rows, err := p.tagRepo.Search(ctx, terms)
		if err != nil {
			return nil, true, err
		}
		out := make([]dto.StaffTaxonomyListItem, 0, len(rows))
		for _, r := range rows {
			out = append(out, dto.StaffTaxonomyListItem{ID: r.ID, Name: r.Name})
		}
		return capPickerRows(out, limit), true, nil

	case TaxonomyFamilyOfficial:
		if len(terms) == 0 {
			return []dto.StaffTaxonomyListItem{}, true, nil
		}
		rows, err := p.officialRepo.Search(ctx, terms)
		if err != nil {
			return nil, true, err
		}
		out := make([]dto.StaffTaxonomyListItem, 0, len(rows))
		for _, r := range rows {
			out = append(out, dto.StaffTaxonomyListItem{ID: r.ID, Name: r.Name})
		}
		return capPickerRows(out, limit), true, nil

	case TaxonomyFamilyEngine:
		rows, err := p.engineRepo.SearchByName(ctx, terms, limit)
		if err != nil {
			return nil, true, err
		}
		out := make([]dto.StaffTaxonomyListItem, 0, len(rows))
		for _, r := range rows {
			out = append(out, dto.StaffTaxonomyListItem{ID: r.ID, Name: r.Name})
		}
		return out, true, nil

	case TaxonomyFamilySeries:
		rows, err := p.seriesRepo.SearchByName(ctx, terms, limit)
		if err != nil {
			return nil, true, err
		}
		out := make([]dto.StaffTaxonomyListItem, 0, len(rows))
		for _, r := range rows {
			out = append(out, dto.StaffTaxonomyListItem{ID: r.ID, Name: r.Name})
		}
		return out, true, nil

	default:
		return nil, false, nil
	}
}

// capPickerRows trims to the response cap. The tag / official repositories
// carry their own internal LIMIT, so this only ever binds if that one is
// raised — it exists so the cap is stated in ONE place for all four families.
func capPickerRows(rows []dto.StaffTaxonomyListItem, limit int) []dto.StaffTaxonomyListItem {
	if len(rows) > limit {
		return rows[:limit]
	}
	return rows
}
