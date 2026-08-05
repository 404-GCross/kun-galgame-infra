package permissions

import "time"

// RolePermissionOverride is one ALLOW-ONLY overlay grant: "this role also holds
// this permission". There is deliberately no deny row and no polarity column —
// the code bundles are the FLOOR, and the overlay may only widen them. That
// asymmetry is what keeps an operator from locking themselves (or everyone) out
// of the console with one click, and it makes "what does the overlay do?" a
// question with only additive answers.
//
// Revoking a grant means DELETING its row, which returns the role to the code
// floor. The deletion is not lost — permission_audit_logs keeps it.
type RolePermissionOverride struct {
	ID uint `gorm:"primaryKey" json:"id"`
	// Role is the exact JWT `roles` claim string. Never "user": the implicit
	// user base never appears in the claim, so a row for it could not fire.
	Role string `gorm:"size:32;not null;uniqueIndex:uq_role_permission_override,priority:1" json:"role"`
	// Permission is a live key string, validated against the registry on write.
	Permission string `gorm:"size:128;not null;uniqueIndex:uq_role_permission_override,priority:2" json:"permission"`
	// GrantedByUserID is who granted it — the current holder, as opposed to the
	// full history in permission_audit_logs.
	GrantedByUserID uint      `gorm:"not null" json:"granted_by_user_id"`
	CreatedAt       time.Time `json:"created_at"`
}

// TableName returns the table name for RolePermissionOverride.
func (RolePermissionOverride) TableName() string { return "role_permission_overrides" }

// Audit actions.
const (
	ActionGrant  = "grant"
	ActionRevoke = "revoke"
)

// PermissionAuditLog is the append-only history of overlay writes. It outlives
// the override rows: a grant that was later revoked leaves two rows here and
// none in role_permission_overrides, which is the only way to answer "who gave
// moderator that key last Tuesday?" after it was taken back.
type PermissionAuditLog struct {
	ID          uint      `gorm:"primaryKey" json:"id"`
	ActorUserID uint      `gorm:"not null;index" json:"actor_user_id"`
	Action      string    `gorm:"size:16;not null" json:"action"`
	Role        string    `gorm:"size:32;not null" json:"role"`
	Permission  string    `gorm:"size:128;not null" json:"permission"`
	CreatedAt   time.Time `gorm:"index" json:"created_at"`
}

// TableName returns the table name for PermissionAuditLog.
func (PermissionAuditLog) TableName() string { return "permission_audit_logs" }
