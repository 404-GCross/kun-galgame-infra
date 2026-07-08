package model

import "time"

// UserSiteRole is a site-scoped role grant: the user holds RoleName ONLY on the
// site identified by SiteID, gaining no cross-site power. It is the storage
// behind the `site_roles` claim (docs/integration/oauth/12-site-roles.md).
//
// RoleName is validated at the grant API — never `user`/`admin`/`ren` (the
// global-only names), always the site-name policy pattern. The IdP only records
// the grant; what a name grants is decided by the consuming site's own perm
// bundle (the platform never learns a product's vocabulary).
type UserSiteRole struct {
	ID uint `gorm:"primaryKey" json:"id"`
	// (user_id, site_id, role_name) is unique — one row per grant. All three
	// carry an explicit priority so the composite index column order is pinned
	// (GORM defaults unmarked shared-index fields to priority 10, which would
	// reorder the columns silently). The prefix (user_id, site_id) also backs
	// the per-site lookup that assembles the claim.
	UserID    uint       `gorm:"not null;uniqueIndex:idx_user_site_role,priority:1" json:"user_id"`
	SiteID    uint       `gorm:"not null;uniqueIndex:idx_user_site_role,priority:2" json:"site_id"`
	RoleName  string     `gorm:"size:50;not null;uniqueIndex:idx_user_site_role,priority:3" json:"role_name"`
	GrantedBy uint       `gorm:"not null" json:"granted_by"`
	GrantedAt time.Time  `json:"granted_at"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
	Note      string     `gorm:"type:text;not null;default:''" json:"note,omitempty"`
}

// TableName returns the table name for UserSiteRole
func (UserSiteRole) TableName() string {
	return "user_site_roles"
}
