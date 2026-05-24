package dto

// RegisterRequest represents a registration request.
// UserAgent / IPAddress are populated by the handler (json:"-") so the
// session row created at registration records the same context Login
// records — same pattern as LoginRequest.
//
// `Code` is the 6-digit verification token sent to `Email` via
// `POST /auth/register/send-code` (10-minute TTL, Redis-backed). It is
// REQUIRED — registration without prior email proof-of-ownership is no
// longer supported. See docs/integration/oauth/05-registration.md.
type RegisterRequest struct {
	Name      string `json:"name" validate:"required,min=2,max=17"`
	Email     string `json:"email" validate:"required,email"`
	Password  string `json:"password" validate:"required,min=6,max=100"`
	Code      string `json:"code" validate:"required,len=6"`
	UserAgent string `json:"-"`
	IPAddress string `json:"-"`
}

// SendRegisterCodeRequest is the body for `POST /auth/register/send-code`.
// Pre-checks name + email uniqueness so we fail fast (without sending an
// email) when the user has typo'd a name already in use or is trying to
// re-register an already-taken email.
type SendRegisterCodeRequest struct {
	Name  string `json:"name" validate:"required,min=2,max=17"`
	Email string `json:"email" validate:"required,email"`
}

// LoginRequest represents a login request
// Account accepts either an email or a username
type LoginRequest struct {
	Account   string `json:"account" validate:"required"`
	Password  string `json:"password" validate:"required"`
	UserAgent string `json:"-"`
	IPAddress string `json:"-"`
}

// RefreshRequest represents a token refresh request
type RefreshRequest struct {
	RefreshToken string `json:"refresh_token" validate:"required"`
}

// ChangePasswordRequest represents a password change request
type ChangePasswordRequest struct {
	OldPassword string `json:"old_password"`
	NewPassword string `json:"new_password" validate:"required,min=6,max=100"`
}

// ForgotPasswordRequest represents a forgot password request
type ForgotPasswordRequest struct {
	Email string `json:"email" validate:"required,email"`
}

// ResetPasswordRequest represents a password reset request
type ResetPasswordRequest struct {
	Token    string `json:"token" validate:"required"`
	Password string `json:"password" validate:"required,min=6,max=100"`
}

// ResetPasswordConfirmRequest represents a password reset confirmation
type ResetPasswordConfirmRequest struct {
	Token    string `json:"token" validate:"required"`
	Password string `json:"password" validate:"required,min=6,max=100"`
}

// SendEmailChangeCodeRequest represents a request to send email change verification code
type SendEmailChangeCodeRequest struct {
	NewEmail string `json:"new_email" validate:"required,email"`
}

// ChangeEmailRequest represents a request to change email with verification code
type ChangeEmailRequest struct {
	Code     string `json:"code" validate:"required,len=6"`
	NewEmail string `json:"new_email" validate:"required,email"`
}

// UpdateProfileRequest is the body for PATCH /auth/me — partial update of
// the authenticated user's display fields. All fields are optional; nil
// pointer means "leave alone", non-nil means "set to this value".
//
// `avatar` and `avatar_image_hash` are independent: avatar is the legacy
// URL fallback, avatar_image_hash points to an image_service-hosted image.
// The frontend resolveAvatarUrl prefers hash over URL when both are set.
// Setting one does not auto-clear the other — callers can set both
// (e.g. on image upload) or just hash (most common path going forward).
type UpdateProfileRequest struct {
	Name            *string `json:"name,omitempty"              validate:"omitempty,min=2,max=17"`
	Avatar          *string `json:"avatar,omitempty"            validate:"omitempty,max=255"`
	AvatarImageHash *string `json:"avatar_image_hash,omitempty" validate:"omitempty,max=64"`
	Bio             *string `json:"bio,omitempty"               validate:"omitempty,max=107"`
}

// TokenPair represents an access/refresh token pair
type TokenPair struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
}

// UserResponse represents a user in API responses
type UserResponse struct {
	UUID        string   `json:"uuid"`
	Name        string   `json:"name"`
	Email       string   `json:"email"`
	Avatar      string   `json:"avatar"`
	Bio         string   `json:"bio"`
	Moemoepoint int      `json:"moemoepoint"`
	Status      int      `json:"status"`
	Roles       []string `json:"roles"`
	CreatedAt   string   `json:"created_at"`
}

// LoginResponse represents a login response
// Note: refresh_token is set as httpOnly cookie, not in response body
type LoginResponse struct {
	User        UserResponse `json:"user"`
	AccessToken string       `json:"access_token"`
}

// RefreshResponse represents a token refresh response
// Note: refresh_token is set as httpOnly cookie, not in response body
type RefreshResponse struct {
	AccessToken string `json:"access_token"`
}
