package repository

import (
	"context"
	"time"

	"api/internal/platform/auth/model"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// UserSiteRoleRepository is the storage for site-scoped role grants.
type UserSiteRoleRepository struct {
	db *gorm.DB
}

// NewUserSiteRoleRepository creates a new UserSiteRoleRepository.
func NewUserSiteRoleRepository(db *gorm.DB) *UserSiteRoleRepository {
	return &UserSiteRoleRepository{db: db}
}

// ActiveRoleNames returns the UNEXPIRED site-role names a user holds on one
// site — the input to the site_roles claim. A NULL expires_at is a permanent
// grant; a past expires_at is filtered out. Sorted for stable claim output.
func (r *UserSiteRoleRepository) ActiveRoleNames(ctx context.Context, userID, siteID uint) ([]string, error) {
	var names []string
	err := r.db.WithContext(ctx).
		Model(&model.UserSiteRole{}).
		Where("user_id = ? AND site_id = ?", userID, siteID).
		Where("expires_at IS NULL OR expires_at > ?", time.Now()).
		Order("role_name ASC").
		Pluck("role_name", &names).Error
	return names, err
}

// ActiveRoleNamesForUsers is the batch form of ActiveRoleNames: for a set of
// users on ONE site, it returns each user's unexpired role names. Users with no
// grant are simply absent from the map. One query, for the S2S batch endpoint.
func (r *UserSiteRoleRepository) ActiveRoleNamesForUsers(ctx context.Context, userIDs []uint, siteID uint) (map[uint][]string, error) {
	out := make(map[uint][]string)
	if len(userIDs) == 0 {
		return out, nil
	}
	var rows []struct {
		UserID   uint
		RoleName string
	}
	err := r.db.WithContext(ctx).
		Model(&model.UserSiteRole{}).
		Select("user_id, role_name").
		Where("user_id IN ? AND site_id = ?", userIDs, siteID).
		Where("expires_at IS NULL OR expires_at > ?", time.Now()).
		Order("user_id ASC, role_name ASC").
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		out[row.UserID] = append(out[row.UserID], row.RoleName)
	}
	return out, nil
}

// ListByUser returns all of a user's grants (every site, including expired) for
// the admin detail view.
func (r *UserSiteRoleRepository) ListByUser(ctx context.Context, userID uint) ([]model.UserSiteRole, error) {
	var rows []model.UserSiteRole
	err := r.db.WithContext(ctx).
		Where("user_id = ?", userID).
		Order("site_id ASC, role_name ASC").
		Find(&rows).Error
	return rows, err
}

// Grant upserts a grant. Idempotent on the unique (user_id, site_id, role_name)
// triple — a re-grant refreshes granted_by / granted_at / expires_at / note.
func (r *UserSiteRoleRepository) Grant(ctx context.Context, g *model.UserSiteRole) error {
	return r.db.WithContext(ctx).
		Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "user_id"}, {Name: "site_id"}, {Name: "role_name"}},
			DoUpdates: clause.AssignmentColumns([]string{"granted_by", "granted_at", "expires_at", "note"}),
		}).
		Create(g).Error
}

// Revoke removes a specific grant. Idempotent — revoking a grant that doesn't
// exist deletes 0 rows and is not an error.
func (r *UserSiteRoleRepository) Revoke(ctx context.Context, userID, siteID uint, roleName string) error {
	return r.db.WithContext(ctx).
		Where("user_id = ? AND site_id = ? AND role_name = ?", userID, siteID, roleName).
		Delete(&model.UserSiteRole{}).Error
}
