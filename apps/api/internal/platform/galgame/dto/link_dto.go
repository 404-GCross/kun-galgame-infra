package dto

// CreateLinkRequest represents a link creation request
type CreateLinkRequest struct {
	Name string `json:"name" validate:"required,max=107"`
	Link string `json:"link" validate:"required,max=500,url"`
}

// CreateAliasRequest represents an alias creation request
type CreateAliasRequest struct {
	Name string `json:"name" validate:"required,max=500"`
}

// DeleteByIDRequest represents a delete request with an ID in the body
type DeleteByIDRequest struct {
	ID int `json:"id" validate:"required"`
}
