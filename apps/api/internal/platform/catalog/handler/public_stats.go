// public_stats.go — the slim public counts endpoint (GET /v1/catalog/stats,
// wave 149b): how big is this catalogue, and nothing else.
//
// The internal dashboard (GET /api/v1/catalog/stats, S2S) is untouched and
// stays internal: review queue levels, LLM verdicts, the anchor source × tier
// matrix, source freshness, orphan counts and the claim-state matrix are
// OPERATIONAL telemetry — they describe how the registry is curated, which is
// nobody's business on a frozen product contract.
package handler

import (
	"api/internal/platform/catalog/dto"
	"api/pkg/errors"
	"api/pkg/response"

	"github.com/gofiber/fiber/v3"
)

// cacheStats is the counts endpoint's window: the numbers move by a handful of
// rows a day, every caller gets the identical payload (no parameters at all),
// and it is the archetypal front-page number — so it caches an order of
// magnitude longer than the detail records.
const cacheStats = "public, max-age=0, s-maxage=3600, stale-while-revalidate=600"

// Stats serves GET /v1/catalog/stats — LIVE work counts per medium plus the
// identity-family totals. No parameters: the payload is one global aggregate,
// identical for every caller (r18 rows are counted, see PublicSummary).
func (h *PublicHandler) Stats(c fiber.Ctx) error {
	sum, err := h.stats.PublicSummary(c.Context())
	if err != nil {
		return response.InternalError(c, errors.ErrInternalServer)
	}
	out := dto.PublicCatalogStats{
		Works: dto.PublicStatsWorks{
			Total:    sum.WorksTotal,
			ByMedium: make([]dto.PublicStatsMediumCount, 0, len(sum.WorksByMedium)),
		},
		Entities: dto.PublicStatsEntities{
			Labels: sum.Entities.Labels, Characters: sum.Entities.Characters,
			CreditNames: sum.Entities.CreditNames, Persons: sum.Entities.Persons,
		},
	}
	for _, r := range sum.WorksByMedium {
		out.Works.ByMedium = append(out.Works.ByMedium, dto.PublicStatsMediumCount{
			MediumID: r.MediumID, Medium: r.Medium, Count: r.Count,
		})
	}
	c.Set("Cache-Control", cacheStats)
	return response.Success(c, out)
}
