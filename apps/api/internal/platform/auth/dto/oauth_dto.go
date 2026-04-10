package dto

// AuthorizeRequest represents an OAuth authorization request.
// Supports both query parameters (GET /authorize) and JSON body (POST /authorize/consent).
type AuthorizeRequest struct {
	ClientID            string `query:"client_id" json:"client_id" validate:"required"`
	RedirectURI         string `query:"redirect_uri" json:"redirect_uri" validate:"required,url"`
	ResponseType        string `query:"response_type" json:"response_type" validate:"required,oneof=code"`
	Scope               string `query:"scope" json:"scope"`
	State               string `query:"state" json:"state" validate:"required"`
	CodeChallenge       string `query:"code_challenge" json:"code_challenge"`               // PKCE
	CodeChallengeMethod string `query:"code_challenge_method" json:"code_challenge_method"` // PKCE: "S256" or "plain"
}

// TokenRequest represents an OAuth token request
type TokenRequest struct {
	GrantType    string `json:"grant_type" validate:"required,oneof=authorization_code refresh_token"`
	Code         string `json:"code"`
	RedirectURI  string `json:"redirect_uri"`
	ClientID     string `json:"client_id" validate:"required"`
	ClientSecret string `json:"client_secret"`
	RefreshToken string `json:"refresh_token"`
	CodeVerifier string `json:"code_verifier"` // PKCE
}

// TokenResponse represents an OAuth token response
type TokenResponse struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	RefreshToken string `json:"refresh_token,omitempty"`
	Scope        string `json:"scope,omitempty"`
}

// UserInfoResponse represents an OAuth userinfo response
type UserInfoResponse struct {
	Sub         string `json:"sub"`
	Name        string `json:"name"`
	Email       string `json:"email"`
	Picture     string `json:"picture,omitempty"`
	UpdatedAt   int64  `json:"updated_at,omitempty"`
}

// AuthorizationCode represents an authorization code
type AuthorizationCode struct {
	Code         string
	ClientID     string
	UserUUID     string
	RedirectURI  string
	Scope        string
	CodeVerifier string
	ExpiresAt    int64
}
