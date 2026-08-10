package service

import (
	"context"
	"regexp"
	"time"

	"api/internal/platform/auth/dto"
	"api/internal/platform/auth/model"
	"api/pkg/errors"
)

var siteRoleNamePattern = regexp.MustCompile(`^[a-z][a-z0-9_]{1,49}$`)

var globalOnlyRoleNames = map[string]struct{}{
	"user":  {},
	"admin": {},
	"ren":   {},
}

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

func (s *AdminService) RevokeSiteRole(ctx context.Context, targetUUID string, siteID uint, roleName string) error {
	user, err := s.userRepo.FindByUUID(ctx, targetUUID)
	if err != nil {
		return errors.NewWithCode(errors.ErrAuthUserNotFound)
	}
	return s.siteRoleRepo.Revoke(ctx, user.ID, siteID, roleName)
}

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
