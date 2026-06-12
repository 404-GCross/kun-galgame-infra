package dto

// UserBrief is the compact user representation returned by
// `GET /api/v1/users/batch`. It's intended for cross-service consumers
// (kungal / moyu) that need to render comments, profile pills, etc.
//
// Excluded vs UserResponse: email (PII; consumers shouldn't depend on it),
// moemoepoint (not display-relevant cross-service).
//
// Included: id (the integer key consumers store as foreign key),
// avatar_image_hash (so consumers can resolve to the right CDN URL),
// status (so consumers can hide banned users), created_at (the user's real
// OAuth registration time — consumers MUST source the "join date" from here,
// NOT from a local first-seen/lazily-created row, which is blank for users
// who registered but never visited that site and wrong for those whose first
// visit lagged their registration).
type UserBrief struct {
	ID              uint     `json:"id"`
	UUID            string   `json:"uuid"`
	Name            string   `json:"name"`
	Avatar          string   `json:"avatar"`
	AvatarImageHash *string  `json:"avatar_image_hash,omitempty"`
	Bio             string   `json:"bio"`
	Status          int      `json:"status"`
	Roles           []string `json:"roles"`
	// CreatedAt is the user's OAuth registration time as UTC RFC3339
	// ("2006-01-02T15:04:05Z"), matching UserResponse.CreatedAt's format.
	CreatedAt string `json:"created_at"`
}

// BatchGetUsersRequest is the parsed query for GET /users/batch.
type BatchGetUsersRequest struct {
	IDs []uint `query:"ids" validate:"required,min=1,max=100"`
}

// BatchGetUsersResponse is the response shape.
//
//	{
//	  "users":     [{...}, ...],   // present users, arbitrary order
//	  "not_found": [99, 101]        // requested IDs that don't exist
//	}
type BatchGetUsersResponse struct {
	Users    []UserBrief `json:"users"`
	NotFound []uint      `json:"not_found"`
}

// SearchUsersResponse is the response shape for GET /users/search.
//
// No `not_found` field — search returns matches; missing-name isn't
// meaningful since the input is a free-text query, not specific IDs.
type SearchUsersResponse struct {
	Users []UserBrief `json:"users"`
}
