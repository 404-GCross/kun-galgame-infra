package dto

// AuthorizeRequest represents an OAuth authorization request.
// Supports both query parameters (GET /authorize) and JSON body (POST /authorize/consent).
type AuthorizeRequest struct {
	ClientID      string `query:"client_id" json:"client_id" validate:"required"`
	RedirectURI   string `query:"redirect_uri" json:"redirect_uri" validate:"required,url"`
	ResponseType  string `query:"response_type" json:"response_type" validate:"required,oneof=code"`
	Scope         string `query:"scope" json:"scope"`
	State         string `query:"state" json:"state" validate:"required"`
	CodeChallenge string `query:"code_challenge" json:"code_challenge"` // PKCE
	// PKCE method. We deliberately accept only S256 (RFC 7636 §4.2 says
	// `plain` is permitted but discouraged; OAuth 2.1 drafts remove it
	// entirely). Empty string is also accepted at this validator level
	// because callers may have omitted the challenge altogether — only
	// when CodeChallenge is non-empty does the server treat "" as S256
	// (the OIDC default).
	CodeChallengeMethod string `query:"code_challenge_method" json:"code_challenge_method" validate:"omitempty,oneof=S256"`
	// Prompt is the OIDC `prompt` param. Honored: `login` (force re-auth —
	// RP-initiated re-prompt, see 07-logout.md), `select_account` (show the
	// account chooser), `none` (silent — frontend errors if interaction is
	// needed). The backend just passes it through; the OP frontend acts on it.
	Prompt string `query:"prompt" json:"prompt" validate:"omitempty,oneof=login select_account none"`
	// LoginHint (OIDC) names the account to switch to in a multi-account switch;
	// the frontend chooser uses it to pre-select / silently switch.
	// See docs/integration/oauth/09-account-switching.md.
	LoginHint string `query:"login_hint" json:"login_hint"`
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

// UserInfoResponse represents an OAuth userinfo response.
//
// `id` is the integer primary key in OAuth's `users` table — the same
// value downstream services (kungal/moyu/galgame_wiki) use as a foreign
// key after `migrate-users` aligns IDs across all three DBs. Returned
// alongside `sub` (the UUID) so consumers can pick whichever they
// prefer; both identify the same user.
//
// `roles` mirrors the JWT `roles` claim — exposed here as a convenience
// so callers don't have to parse the access token to learn the user's
// role set. Not gated by scope: it's the same data already in the JWT
// that authenticated this request.
type UserInfoResponse struct {
	ID        uint     `json:"id"`
	Sub       string   `json:"sub"`
	Name      string   `json:"name,omitempty"`
	Email     string   `json:"email,omitempty"`
	Picture   string   `json:"picture,omitempty"`
	Roles     []string `json:"roles"`
	UpdatedAt int64    `json:"updated_at,omitempty"`
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
