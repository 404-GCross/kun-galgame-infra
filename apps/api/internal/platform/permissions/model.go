package permissions

import (
	"time"

	"gorm.io/gorm"
)

const (
	EffectGrant = "grant"
	EffectDeny  = "deny"
)

type RolePermissionOverride struct {
	ID              uint      `gorm:"primaryKey" json:"id"`
	Role            string    `gorm:"size:32;not null;uniqueIndex:uq_role_permission_override,priority:1" json:"role"`
	Permission      string    `gorm:"size:128;not null;uniqueIndex:uq_role_permission_override,priority:2" json:"permission"`
	Effect          string    `gorm:"size:8;not null" json:"effect"`
	GrantedByUserID uint      `gorm:"not null" json:"granted_by_user_id"`
	CreatedAt       time.Time `json:"created_at"`
}

func (RolePermissionOverride) TableName() string { return "role_permission_overrides" }

func AddOverrideEffectColumn(db *gorm.DB) error {
	if !db.Migrator().HasTable(&RolePermissionOverride{}) {
		return nil
	}
	return db.Exec(`
		ALTER TABLE role_permission_overrides
			ADD COLUMN IF NOT EXISTS effect varchar(8) NOT NULL DEFAULT 'grant';
		ALTER TABLE role_permission_overrides
			ALTER COLUMN effect DROP DEFAULT;
	`).Error
}

const (
	ActionGrant      = "grant"
	ActionRevoke     = "revoke"
	ActionDeny       = "deny"
	ActionRevokeDeny = "revoke_deny"
)

type PermissionAuditLog struct {
	ID          uint      `gorm:"primaryKey" json:"id"`
	ActorUserID uint      `gorm:"not null;index" json:"actor_user_id"`
	Action      string    `gorm:"size:16;not null" json:"action"`
	Role        string    `gorm:"size:32;not null" json:"role"`
	Permission  string    `gorm:"size:128;not null" json:"permission"`
	CreatedAt   time.Time `gorm:"index" json:"created_at"`
}

func (PermissionAuditLog) TableName() string { return "permission_audit_logs" }
