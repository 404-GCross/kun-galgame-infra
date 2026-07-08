package model

// Role represents a named user role. Roles are the wire contract's five fixed
// names (docs/integration/oauth/11-roles.md); a user's roles live in the
// user_roles join table. Authorization itself is permission-first — roles map
// to permission bundles in the per-domain perm packages (internal/platform/
// */perm), not to rows in a permissions table. See docs/auth/04.
type Role struct {
	ID          uint   `gorm:"primaryKey" json:"id"`
	Name        string `gorm:"size:50;uniqueIndex;not null" json:"name"`
	Description string `gorm:"type:text;default:''" json:"description"`
}

// TableName returns the table name for Role
func (Role) TableName() string {
	return "roles"
}
