package handler

import (
	"api/internal/platform/catalog/dto"
	"api/pkg/errors"
	"api/pkg/response"

	"github.com/gofiber/fiber/v3"
)

const cacheStats = "public, max-age=0, s-maxage=3600, stale-while-revalidate=600"

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
