package perm

import "api/internal/platform/authz"

const (
	PublishDirect   authz.Permission = "galgame.publish_direct"
	Review          authz.Permission = "galgame.review"
	EditAny         authz.Permission = "galgame.edit_any"
	Create          authz.Permission = "galgame.create"
	AdminAccess     authz.Permission = "galgame.admin_access"
	TaxonomyEditAny authz.Permission = "galgame.taxonomy.edit_any"
	TaxonomyReview  authz.Permission = "galgame.taxonomy.review"
	SearchAllStates authz.Permission = "galgame.search.all_states"
	OwnerOverride   authz.Permission = "galgame.owner_override"

	EditGameReview authz.Permission = "edit.galgame.game.review"
	EditGameStatus authz.Permission = "edit.galgame.game.status"
	EditGameVNDBID authz.Permission = "edit.galgame.game.vndb_id"
)

var moderatorPerms = []authz.Permission{
	PublishDirect,
	Review,
	EditAny,
	Create,
	AdminAccess,
	TaxonomyEditAny,
	TaxonomyReview,
	SearchAllStates,
	EditGameStatus,
	EditGameVNDBID,
}

var adminPerms = append(append([]authz.Permission{}, moderatorPerms...), OwnerOverride, EditGameReview)

var Bundles = authz.Bundles{
	"creator":   {PublishDirect},
	"moderator": moderatorPerms,
	"admin":     adminPerms,
	"ren":       adminPerms,
}

var Resolver = authz.NewResolver(Bundles)
