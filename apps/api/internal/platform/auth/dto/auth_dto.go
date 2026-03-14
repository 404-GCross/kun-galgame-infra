package dto

// RegisterRequest represents a registration request
type RegisterRequest struct {
	Name     string `json:"name" validate:"required,min=2,max=17"`
	Email    string `json:"email" validate:"required,email"`
	Password string `json:"password" validate:"required,min=6,max=100"`
}

// LoginRequest represents a login request
type LoginRequest struct {
	Email     string `json:"email" validate:"required,email"`
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

// TokenPair represents an access/refresh token pair
type TokenPair struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
}

// UserResponse represents a user in API responses
type UserResponse struct {
	UUID        string `json:"uuid"`
	Name        string `json:"name"`
	Email       string `json:"email"`
	Avatar      string `json:"avatar"`
	Bio         string `json:"bio"`
	Moemoepoint int    `json:"moemoepoint"`
	Status      int    `json:"status"`
	CreatedAt   string `json:"created_at"`
}

// LoginResponse represents a login response
type LoginResponse struct {
	User   UserResponse `json:"user"`
	Tokens TokenPair    `json:"tokens"`
}
