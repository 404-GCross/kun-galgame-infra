package permissions

import (
	"context"
	"errors"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Store is the overlay's data access. Every write pairs the override change
// with its audit row in ONE transaction: an unaudited permission change is the
// failure this whole feature exists to prevent, so the two must not be able to
// come apart.
type Store struct {
	db *gorm.DB
}

// NewStore wires a Store on the main database (kun_galgame_infra).
func NewStore(db *gorm.DB) *Store { return &Store{db: db} }

// Overrides returns every overlay row (console listing + validation lookups).
func (s *Store) Overrides(ctx context.Context) ([]RolePermissionOverride, error) {
	var rows []RolePermissionOverride
	err := s.db.WithContext(ctx).Order("role, permission").Find(&rows).Error
	return rows, err
}

// HasOverride reports whether the overlay currently grants (role, permission).
// It is the difference between "revoking a grant" and "trying to cut below the
// code floor", which the validator must be able to tell apart.
func (s *Store) HasOverride(ctx context.Context, role, permission string) (bool, error) {
	var count int64
	err := s.db.WithContext(ctx).Model(&RolePermissionOverride{}).
		Where("role = ? AND permission = ?", role, permission).Count(&count).Error
	return count > 0, err
}

// ErrAlreadyGranted is returned when the overlay row already exists — a lost
// race with a concurrent grant, not a validation failure.
var ErrAlreadyGranted = errors.New("permissions: overlay grant already exists")

// ErrNotGranted is returned when a revoke finds no overlay row to delete.
var ErrNotGranted = errors.New("permissions: no overlay grant to revoke")

// Grant inserts the overlay row and its audit row.
func (s *Store) Grant(ctx context.Context, role, permission string, actorID uint) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		row := RolePermissionOverride{Role: role, Permission: permission, GrantedByUserID: actorID}
		res := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&row)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return ErrAlreadyGranted
		}
		return tx.Create(&PermissionAuditLog{
			ActorUserID: actorID, Action: ActionGrant, Role: role, Permission: permission,
		}).Error
	})
}

// Revoke deletes the overlay row and its audit row. The role returns to the
// code floor; it never drops below it, because there is no deny row to write.
func (s *Store) Revoke(ctx context.Context, role, permission string, actorID uint) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		res := tx.Where("role = ? AND permission = ?", role, permission).
			Delete(&RolePermissionOverride{})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return ErrNotGranted
		}
		return tx.Create(&PermissionAuditLog{
			ActorUserID: actorID, Action: ActionRevoke, Role: role, Permission: permission,
		}).Error
	})
}

// AuditEntry is one audit row joined to the actor's display name.
type AuditEntry struct {
	ID          uint   `json:"id"`
	ActorUserID uint   `json:"actor_user_id"`
	ActorName   string `json:"actor_name"`
	Action      string `json:"action"`
	Role        string `json:"role"`
	Permission  string `json:"permission"`
	CreatedAt   string `json:"created_at"`
}

// RecentAudit returns the newest audit rows, actor name resolved. A LEFT JOIN
// so an anonymized or deleted actor still shows their change (with an empty
// name) rather than dropping the row out of the history.
func (s *Store) RecentAudit(ctx context.Context, limit int) ([]AuditEntry, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	var rows []struct {
		PermissionAuditLog
		ActorName string
	}
	err := s.db.WithContext(ctx).
		Model(&PermissionAuditLog{}).
		Select("permission_audit_logs.*, users.name AS actor_name").
		Joins("LEFT JOIN users ON users.id = permission_audit_logs.actor_user_id").
		Order("permission_audit_logs.id DESC").
		Limit(limit).
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	out := make([]AuditEntry, len(rows))
	for i, r := range rows {
		out[i] = AuditEntry{
			ID:          r.ID,
			ActorUserID: r.ActorUserID,
			ActorName:   r.ActorName,
			Action:      r.Action,
			Role:        r.Role,
			Permission:  r.Permission,
			CreatedAt:   r.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"),
		}
	}
	return out, nil
}
