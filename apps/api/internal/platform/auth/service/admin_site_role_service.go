package service

import (
	"context"
	"regexp"
	"time"

	"api/internal/platform/auth/dto"
	"api/internal/platform/auth/model"
	"api/pkg/errors"
)

// siteRoleNamePattern is the grantable site-role name policy: a lowercase
// identifier, 2–50 chars, starting with a letter. Allows `moderator`/`creator`
// (this site's slice of the same-named global role) and custom bundle names
// like `event_organizer`.
var siteRoleNamePattern = regexp.MustCompile(`^[a-z][a-z0-9_]{1,49}$`)

// globalOnlyRoleNames are never grantable as a SITE role: `user` is the implicit
// base, `admin`/`ren` are the global-only management tier. Rejecting them is
// what keeps a site grant from ever reaching an admin/ren-gated permission (the
// union-resolution safety argument, docs/integration/oauth/12-site-roles.md).
var globalOnlyRoleNames = map[string]struct{}{
	"user":  {},
	"admin": {},
	"ren":   {},
}

// validateSiteRoleName enforces the grantable-name policy (§A-4).
func validateSiteRoleName(name string) error {
	if _, banned := globalOnlyRoleNames[name]; banned {
		return errors.New(errors.ErrValidationFailed,
			"该角色名是全局专属，不能作为站点角色授予")
	}
	if !siteRoleNamePattern.MatchString(name) {
		return errors.New(errors.ErrValidationFailed,
			"角色名需为小写字母开头的 2-50 位标识符（a-z0-9_）")
	}
	return nil
}

// AssignSiteRole grants a site-scoped role to a user. Validates the name policy
// and that the site exists; records grantedBy. Idempotent (re-grant refreshes
// the metadata). Self-grant is refused by the handler, mirroring the global
// role matrix.
func (s *AdminService) AssignSiteRole(ctx context.Context, targetUUID string, grantedBy uint, req *dto.AssignSiteRoleRequest) error {
	if err := validateSiteRoleName(req.RoleName); err != nil {
		return err
	}
	if _, err := s.siteRepo.FindByID(ctx, req.SiteID); err != nil {
		return errors.NewWithCode(errors.ErrSiteNotFound)
	}
	user, err := s.userRepo.FindByUUID(ctx, targetUUID)
	if err != nil {
		return errors.NewWithCode(errors.ErrAuthUserNotFound)
	}
	return s.siteRoleRepo.Grant(ctx, &model.UserSiteRole{
		UserID:    user.ID,
		SiteID:    req.SiteID,
		RoleName:  req.RoleName,
		GrantedBy: grantedBy,
		GrantedAt: time.Now(),
		ExpiresAt: req.ExpiresAt,
		Note:      req.Note,
	})
}

// RevokeSiteRole removes a site-scoped grant. Idempotent — revoking a grant that
// isn't there succeeds (0 rows).
func (s *AdminService) RevokeSiteRole(ctx context.Context, targetUUID string, siteID uint, roleName string) error {
	user, err := s.userRepo.FindByUUID(ctx, targetUUID)
	if err != nil {
		return errors.NewWithCode(errors.ErrAuthUserNotFound)
	}
	return s.siteRoleRepo.Revoke(ctx, user.ID, siteID, roleName)
}

// listSiteRoles returns a user's grants projected for the admin detail view,
// resolving each site_id to its display name. Best-effort on the name lookup
// (an unknown site_id still shows the row, name blank).
func (s *AdminService) listSiteRoles(ctx context.Context, userID uint) ([]dto.SiteRoleResponse, error) {
	rows, err := s.siteRoleRepo.ListByUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	out := make([]dto.SiteRoleResponse, 0, len(rows))
	siteNames := make(map[uint]string)
	for i := range rows {
		r := &rows[i]
		name, ok := siteNames[r.SiteID]
		if !ok {
			if site, err := s.siteRepo.FindByID(ctx, r.SiteID); err == nil {
				name = site.Name
			}
			siteNames[r.SiteID] = name
		}
		out = append(out, dto.SiteRoleResponse{
			SiteID:    r.SiteID,
			SiteName:  name,
			RoleName:  r.RoleName,
			GrantedBy: r.GrantedBy,
			GrantedAt: r.GrantedAt,
			ExpiresAt: r.ExpiresAt,
			Note:      r.Note,
		})
	}
	return out, nil
}
