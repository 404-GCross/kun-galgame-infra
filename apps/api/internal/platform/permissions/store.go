package permissions

import (
	"context"
	"errors"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type Store struct {
	db *gorm.DB
}

func NewStore(db *gorm.DB) *Store { return &Store{db: db} }

func (s *Store) Overrides(ctx context.Context) ([]RolePermissionOverride, error) {
	var rows []RolePermissionOverride
	err := s.db.WithContext(ctx).Order("role, permission").Find(&rows).Error
	return rows, err
}

func (s *Store) OverrideEffect(ctx context.Context, role, permission string) (string, error) {
	var rows []RolePermissionOverride
	err := s.db.WithContext(ctx).
		Where("role = ? AND permission = ?", role, permission).Limit(1).Find(&rows).Error
	if err != nil || len(rows) == 0 {
		return "", err
	}
	return rows[0].Effect, nil
}

var ErrAlreadyGranted = errors.New("permissions: overlay row already exists")

var ErrNotGranted = errors.New("permissions: no overlay row to remove")

// ErrBadEffect is a programming error, not an operator one: something asked for
// a row that is neither a grant nor a deny. It is checked here, at the only
// place rows are written, because an empty Effect would be a row the engine
// silently ignores — a permission change that appears to have been made.
var ErrBadEffect = errors.New("permissions: override effect must be grant or deny")

func (s *Store) Add(ctx context.Context, role, permission, effect string, actorID uint) error {
	action, ok := addAction(effect)
	if !ok {
		return ErrBadEffect
	}
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		row := RolePermissionOverride{
			Role: role, Permission: permission, Effect: effect, GrantedByUserID: actorID,
		}
		res := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&row)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return ErrAlreadyGranted
		}
		return tx.Create(&PermissionAuditLog{
			ActorUserID: actorID, Action: action, Role: role, Permission: permission,
		}).Error
	})
}

func addAction(effect string) (string, bool) {
	switch effect {
	case EffectGrant:
		return ActionGrant, true
	case EffectDeny:
		return ActionDeny, true
	default:
		return "", false
	}
}

func (s *Store) Remove(ctx context.Context, role, permission string, actorID uint) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var row RolePermissionOverride
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("role = ? AND permission = ?", role, permission).Take(&row).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrNotGranted
		}
		if err != nil {
			return err
		}

		if err := tx.Delete(&RolePermissionOverride{}, row.ID).Error; err != nil {
			return err
		}

		action := ActionRevoke
		if row.Effect == EffectDeny {
			action = ActionRevokeDeny
		}
		return tx.Create(&PermissionAuditLog{
			ActorUserID: actorID, Action: action, Role: role, Permission: permission,
		}).Error
	})
}

type AuditEntry struct {
	ID          uint   `json:"id"`
	ActorUserID uint   `json:"actor_user_id"`
	ActorName   string `json:"actor_name"`
	Action      string `json:"action"`
	Role        string `json:"role"`
	Permission  string `json:"permission"`
	CreatedAt   string `json:"created_at"`
}

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
