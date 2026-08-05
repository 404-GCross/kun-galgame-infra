package permissions

import (
	"time"

	"gorm.io/gorm"
)

// Override effects. A row is one of exactly these two; there is no third state,
// and "no row" means "whatever the code bundle says".
const (
	// EffectGrant: the role additionally holds this permission.
	EffectGrant = "grant"
	// EffectDeny: the role does NOT hold this permission even though its code
	// bundle grants it. Inert on the `ren` row by construction (see Merge).
	EffectDeny = "deny"
)

// RolePermissionOverride is one overlay row: "this role additionally holds this
// permission" (EffectGrant) or "this role does not hold it despite the code
// bundle" (EffectDeny).
//
// The deny half was ruled in on 2026-08-04. Until then the overlay was
// allow-only, so that no click could ever narrow anyone; the replacement rule is
// narrower but keeps the same property where it actually matters: `ren` is the
// lockout-recovery fuse and NOTHING in this table can reduce it. Every other
// editable role (creator / moderator / admin) may be reduced from the console,
// which is what makes "take that key off moderator until Monday" a supported
// operation instead of a deploy.
//
// One row per (role, permission) — grant and deny for the same pair would be a
// contradiction, so the unique index enforces that only one of them exists.
// Removing either kind of row means DELETING it, which returns the role to the
// code floor. The deletion is not lost — permission_audit_logs keeps it.
type RolePermissionOverride struct {
	ID uint `gorm:"primaryKey" json:"id"`
	// Role is the exact JWT `roles` claim string. Never "user": the implicit
	// user base never appears in the claim, so a row for it could not fire.
	Role string `gorm:"size:32;not null;uniqueIndex:uq_role_permission_override,priority:1" json:"role"`
	// Permission is a live key string, validated against the registry on write.
	Permission string `gorm:"size:128;not null;uniqueIndex:uq_role_permission_override,priority:2" json:"permission"`
	// Effect is EffectGrant or EffectDeny.
	//
	// Deliberately NO GORM `default:` tag. That tag makes GORM omit the field
	// from an INSERT whenever it holds the zero value, so a row written without
	// an effect would silently become whatever the database's default says —
	// i.e. a permission change that reads as a grant because nobody set it.
	// Without the tag an empty Effect is a NOT NULL violation: loud, at the
	// insert, instead of quiet and wrong forever. Store.Add refuses one earlier
	// still, and TestStoreNeverWritesAnEmptyEffect pins it.
	//
	// Adding a NOT NULL column to a populated table is therefore not something
	// AutoMigrate can do on its own — AddOverrideEffectColumn does it in raw SQL
	// first (add with a DEFAULT so existing rows backfill, then DROP DEFAULT).
	Effect string `gorm:"size:8;not null" json:"effect"`
	// GrantedByUserID is who wrote the row — the current holder, as opposed to
	// the full history in permission_audit_logs.
	GrantedByUserID uint      `gorm:"not null" json:"granted_by_user_id"`
	CreatedAt       time.Time `json:"created_at"`
}

// TableName returns the table name for RolePermissionOverride.
func (RolePermissionOverride) TableName() string { return "role_permission_overrides" }

// AddOverrideEffectColumn adds role_permission_overrides.effect to a table that
// may already have rows, and must run BEFORE AutoMigrate — same shape as
// devapi.AddOAuthClientDevColumns, and for the same reason: AutoMigrate cannot
// add a NOT NULL column to a populated table, and the alternative (leaving a
// DEFAULT on the model) is the tag trap the field comment describes.
//
// Every pre-existing row is a grant by construction: until this column existed
// the overlay had no other kind of row. So the DEFAULT backfills them correctly
// and is then dropped, leaving a column that no INSERT may omit.
//
// Idempotent — re-running migrate is a no-op.
func AddOverrideEffectColumn(db *gorm.DB) error {
	if !db.Migrator().HasTable(&RolePermissionOverride{}) {
		// Fresh database: AutoMigrate creates the column with the rest.
		return nil
	}
	return db.Exec(`
		ALTER TABLE role_permission_overrides
			ADD COLUMN IF NOT EXISTS effect varchar(8) NOT NULL DEFAULT 'grant';
		ALTER TABLE role_permission_overrides
			ALTER COLUMN effect DROP DEFAULT;
	`).Error
}

// Audit actions. The first two predate the deny half and keep their exact
// spelling — renaming them would rewrite the meaning of every historical row.
const (
	// ActionGrant: an EffectGrant row was written.
	ActionGrant = "grant"
	// ActionRevoke: an EffectGrant row was deleted (the "revoke-grant" of the
	// four-operation vocabulary).
	ActionRevoke = "revoke"
	// ActionDeny: an EffectDeny row was written.
	ActionDeny = "deny"
	// ActionRevokeDeny: an EffectDeny row was deleted — the role returns to its
	// code floor.
	ActionRevokeDeny = "revoke_deny"
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
