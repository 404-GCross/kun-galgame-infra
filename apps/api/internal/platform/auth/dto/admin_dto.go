package dto

// UserListRequest represents a user list request
type UserListRequest struct {
	Page     int    `query:"page" validate:"min=1"`
	Limit    int    `query:"limit" validate:"min=1,max=100"`
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
	Name   *string `json:"name" validate:"omitempty,min=2,max=17"`
	Email  *string `json:"email" validate:"omitempty,email"`
	Avatar *string `json:"avatar" validate:"omitempty,url"`
	Bio    *string `json:"bio" validate:"omitempty,max=107"`
	Status *int    `json:"status" validate:"omitempty,oneof=0 1"`
}

// BanUserRequest represents a ban user request
type BanUserRequest struct {
	Reason string `json:"reason" validate:"required,min=1,max=500"`
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
