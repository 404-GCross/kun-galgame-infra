package perm

import "api/internal/platform/authz"

const Review authz.Permission = "catalog.review"

const ClaimReview authz.Permission = "catalog.claim.review"

const (
	EditWork       authz.Permission = "edit.catalog.work"
	EditWorkReview authz.Permission = "edit.catalog.work.review"
)

const (
	EditTaxonomy       authz.Permission = "edit.catalog.taxonomy"
	EditTaxonomyReview authz.Permission = "edit.catalog.taxonomy.review"
)

const EditTrusted authz.Permission = "catalog.edit.trusted"

var moderatorPerms = []authz.Permission{ClaimReview}

var adminPerms = append(append([]authz.Permission{}, moderatorPerms...),
	EditWork, EditWorkReview, EditTaxonomy, EditTaxonomyReview, EditTrusted)

var renPerms = append(append([]authz.Permission{}, adminPerms...), Review)

var Bundles = authz.Bundles{
	"moderator": moderatorPerms,
	"admin":     adminPerms,
	"ren":       renPerms,
}

var Resolver = authz.NewHolder(Bundles)
