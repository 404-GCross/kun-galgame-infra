package dto

// UserListRequest represents a user list request.
//
// Page/Limit are `omitempty` so an absent value (binds to 0) passes
// validation and the service applies its defaults; a present value is still
// bounds-checked. SortBy is whitelisted both here (oneof) and at the repo
// layer (allowlist) — the latter is the load-bearing guard against ORDER BY
// injection since this DTO's tags only run via the handler's explicit
// utils.Validate, not an app-wide StructValidator.
type UserListRequest struct {
	Page     int    `query:"page" validate:"omitempty,min=1"`
	Limit    int    `query:"limit" validate:"omitempty,min=1,max=100"`
	Search   string `query:"search"`
	Status   *int   `query:"status"`
	SortBy   string `query:"sort_by" validate:"omitempty,oneof=created_at name email moemoepoint"`
	SortDesc bool   `query:"sort_desc"`
}

// UserListResponse represents a user list response
type UserListResponse struct {
	Users      []UserResponse `json:"users"`
	Total      int64          `json:"total"`
	Page       int            `json:"page"`
	Limit      int            `json:"limit"`
	TotalPages int            `json:"total_pages"`
}

// UpdateUserRequest represents a user update request
type UpdateUserRequest struct {
	// Admin user-edit must enforce the same name rule as self-edit
	// (PATCH /auth/me) — otherwise an admin could create an unreachable
	// account with invisible-codepoint names that no user could log
	// into. `kun_name` = utils.IsValidName.
	Name   *string `json:"name" validate:"omitempty,kun_name"`
	Email  *string `json:"email" validate:"omitempty,email"`
	Avatar *string `json:"avatar" validate:"omitempty,url"`
	Bio    *string `json:"bio" validate:"omitempty,max=107"`
	Status *int    `json:"status" validate:"omitempty,oneof=0 1"`
}

// BanUserRequest represents a ban user request
type BanUserRequest struct {
	Reason string `json:"reason" validate:"required,min=1,max=500"`
}

// AssignRoleRequest grants a role to a user. `ren` is intentionally absent from
// the oneof — it is provisioned via DB only and never grantable through the API
// (see the role-management ren-gate in admin_handler.callerCanManageRole).
type AssignRoleRequest struct {
	Role string `json:"role" validate:"required,oneof=user moderator admin"`
}

// UserDetailResponse represents detailed user info for admin
type UserDetailResponse struct {
	UserResponse
	IP            string `json:"ip"`
	SessionCount  int    `json:"session_count"`
	OAuthAccounts int    `json:"oauth_accounts"`
	SiteData      []UserSiteDataResponse `json:"site_data"`
}

// UserSiteDataResponse represents user site data
type UserSiteDataResponse struct {
	SiteID   uint   `json:"site_id"`
	SiteName string `json:"site_name"`
	Role     int    `json:"role"`
	Status   int    `json:"status"`
}
