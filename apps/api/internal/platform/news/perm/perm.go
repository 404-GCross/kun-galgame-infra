package perm

import "api/internal/platform/authz"

// Review is the news moderation queue: reading it and deciding an item's fate.
// It sits in the moderator bundle alongside catalog.claim.review because
// clearing the ad spam 月幕 warned about is routine moderation work, not an
// administrative act.
const Review authz.Permission = "news.review"

var moderatorPerms = []authz.Permission{Review}

var adminPerms = append([]authz.Permission{}, moderatorPerms...)

var renPerms = append([]authz.Permission{}, adminPerms...)

var Bundles = authz.Bundles{
	"moderator": moderatorPerms,
	"admin":     adminPerms,
	"ren":       renPerms,
}

var Resolver = authz.NewHolder(Bundles)
