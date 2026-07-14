package service

import (
	"context"

	"api/internal/platform/community/model"
)

// The id-addressed S2S tenant guard (ruling 4). Every read/write that names a
// thread/post/review-item by its globally-unique id must confirm the target
// belongs to the CALLING tenant — otherwise a client of site A could reach site
// B's content purely because ids are global. The guard lives in the authority
// layer (this service), never in the BFF.
//
// The caller's tenant travels through the request context (WithCallerSite),
// stamped once by the handler from the client's site binding, so each flow can
// enforce the guard on the load it ALREADY performs (no extra query). An unset
// caller site (the empty string) is a TRUSTED INTERNAL caller — the platform
// trust-callback path and the service unit tests call in without an S2S binding
// — and is never guarded.
//
// A guard failure is reported as the SAME "not found" a truly-absent id yields
// (ErrThreadNotFound / ErrPostNotFound / ErrReviewNotFound → 404), so a
// cross-tenant probe cannot distinguish "belongs to another site" from "does not
// exist" (no existence leak). Catalog-anchored threads (work/person) are shared
// cross-site by design (invariant 1) and are exempt from the guard.

type callerSiteKey struct{}

// WithCallerSite stamps the authenticated caller's tenant onto ctx. The handler
// calls it once per guarded S2S request, right after resolving the site binding.
func WithCallerSite(ctx context.Context, site string) context.Context {
	return context.WithValue(ctx, callerSiteKey{}, site)
}

// callerSite reads the tenant stamped by WithCallerSite ("" = a trusted internal
// caller, which the guard skips).
func callerSite(ctx context.Context) string {
	s, _ := ctx.Value(callerSiteKey{}).(string)
	return s
}

// CrossTenant reports whether a SITE-LOCAL thread belongs to a different tenant
// than callerSite. Pure predicate, shared by the handler read paths (which hold
// the site binding directly) and the ctx-based write guard below. An empty
// callerSite (internal caller) never crosses; catalog anchors never cross.
func CrossTenant(callerSite, threadSite string, anchorKind int16) bool {
	return callerSite != "" && model.AnchorIsSiteLocal(anchorKind) && threadSite != callerSite
}

// crossTenantCtx applies CrossTenant against the caller stamped on ctx — the
// form the write flows use on their existing thread/post-context load.
func crossTenantCtx(ctx context.Context, threadSite string, anchorKind int16) bool {
	return CrossTenant(callerSite(ctx), threadSite, anchorKind)
}
