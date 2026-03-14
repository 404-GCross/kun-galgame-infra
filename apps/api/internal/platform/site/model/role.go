package model

// Role represents a user role
type Role struct {
	ID          uint   `gorm:"primaryKey" json:"id"`
	Name        string `gorm:"size:50;uniqueIndex;not null" json:"name"`
	Description string `gorm:"type:text;default:''" json:"description"`

	// Relations
	Permissions []Permission `gorm:"many2many:role_permissions;" json:"permissions,omitempty"`
}

// TableName returns the table name for Role
func (Role) TableName() string {
	return "roles"
}

// Permission represents a permission
type Permission struct {
	ID          uint   `gorm:"primaryKey" json:"id"`
	Name        string `gorm:"size:100;uniqueIndex;not null" json:"name"`
	Description string `gorm:"type:text;default:''" json:"description"`

	// Relations
	Roles []Role `gorm:"many2many:role_permissions;" json:"-"`
}

// TableName returns the table name for Permission
func (Permission) TableName() string {
	return "permissions"
}
