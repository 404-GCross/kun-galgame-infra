package handler

import (
	"strings"

	"api/internal/middleware"
	catperm "api/internal/platform/catalog/perm"

	"github.com/gofiber/fiber/v3"
)

// AdminClaimsPrefix is the staff claim-review subtree of the admin face.
const AdminClaimsPrefix = "/api/v1/admin/catalog/claims"

// AdminGate is the permission gate for the whole /api/v1/admin/catalog prefix.
//
// TWO authorities live under that one prefix and a single RequirePermission
// cannot express both: the claim review queue is content moderation
// (catalog.claim.review, moderator and up), everything else — candidates,
// merge proposals, probable refs — is curation of the identity registry itself
// (catalog.review, ren only). Fiber runs EVERY prefix middleware that matches a
// nested path, so the narrower branch cannot simply be layered on top of the
// broader one: registering a second Use on .../claims would leave the ren gate
// in the chain and keep moderators out. The choice therefore has to be made
// inside one handler, which is this.
//
// It lives beside the routes rather than in cmd/catalog so the split is pinned
// by the handler package's own tests.
func AdminGate() fiber.Handler {
	curation := middleware.RequirePermission(catperm.Resolver, catperm.Review)
	claims := middleware.RequirePermission(catperm.Resolver, catperm.ClaimReview)
	return func(c fiber.Ctx) error {
		if strings.HasPrefix(c.Path(), AdminClaimsPrefix) {
			return claims(c)
		}
		return curation(c)
	}
}
