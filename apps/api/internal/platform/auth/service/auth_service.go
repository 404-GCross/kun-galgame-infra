package service

import (
	"context"
	"time"

	"api/internal/platform/auth/dto"
	"api/internal/platform/auth/model"
	"api/internal/platform/auth/repository"
	"api/pkg/config"
	"api/pkg/errors"
	"api/pkg/utils"
)

// AuthService handles authentication logic
type AuthService struct {
	userRepo    *repository.UserRepository
	sessionRepo *repository.SessionRepository
	cfg         *config.Config
}

// NewAuthService creates a new AuthService
func NewAuthService(
	userRepo *repository.UserRepository,
	sessionRepo *repository.SessionRepository,
	jwtCfg config.JWTConfig,
) *AuthService {
	return &AuthService{
		userRepo:    userRepo,
		sessionRepo: sessionRepo,
		cfg:         &config.Config{JWT: jwtCfg},
	}
}

// NewAuthServiceWithConfig creates a new AuthService with full config
func NewAuthServiceWithConfig(
	userRepo *repository.UserRepository,
	sessionRepo *repository.SessionRepository,
	cfg *config.Config,
) *AuthService {
	return &AuthService{
		userRepo:    userRepo,
		sessionRepo: sessionRepo,
		cfg:         cfg,
	}
}

// Register registers a new user
func (s *AuthService) Register(ctx context.Context, req *dto.RegisterRequest) (*model.User, error) {
	// Check if email exists
	exists, err := s.userRepo.ExistsByEmail(ctx, req.Email)
	if err != nil {
		return nil, err
	}
	if exists {
		return nil, errors.NewWithCode(errors.ErrAuthEmailExists)
	}

	// Check if name exists
	exists, err = s.userRepo.ExistsByName(ctx, req.Name)
	if err != nil {
		return nil, err
	}
	if exists {
		return nil, errors.NewWithCode(errors.ErrAuthNameExists)
	}

	// Hash password
	hashedPassword, err := utils.HashPassword(req.Password)
	if err != nil {
		return nil, err
	}

	// Create user
	user := &model.User{
		Name:     req.Name,
		Email:    req.Email,
		Password: &hashedPassword,
	}

	if err := s.userRepo.Create(ctx, user); err != nil {
		return nil, err
	}

	return user, nil
}

// Login authenticates a user
func (s *AuthService) Login(ctx context.Context, req *dto.LoginRequest) (*dto.TokenPair, *model.User, error) {
	// Find user by email
	user, err := s.userRepo.FindByEmail(ctx, req.Email)
	if err != nil {
		return nil, nil, errors.NewWithCode(errors.ErrAuthUserNotFound)
	}

	// Check if user is banned
	if user.IsBanned() {
		return nil, nil, errors.NewWithCode(errors.ErrAuthUnauthorized)
	}

	// Check if password is set (migration users need to reset password)
	if !user.IsPasswordSet() {
		return nil, nil, errors.NewWithCode(errors.ErrAuthPasswordRequired)
	}

	// Verify password
	ok, err := utils.VerifyPassword(req.Password, *user.Password)
	if err != nil || !ok {
		return nil, nil, errors.NewWithCode(errors.ErrAuthInvalidPassword)
	}

	// Generate tokens
	tokens, err := s.generateTokens(user)
	if err != nil {
		return nil, nil, err
	}

	// Create session
	session := &model.Session{
		UserID:       user.ID,
		SessionToken: tokens.AccessToken,
		RefreshToken: tokens.RefreshToken,
		UserAgent:    req.UserAgent,
		IPAddress:    req.IPAddress,
		ExpiresAt:    time.Now().Add(7 * 24 * time.Hour), // 7 days
	}

	if err := s.sessionRepo.Create(ctx, session); err != nil {
		return nil, nil, err
	}

	return tokens, user, nil
}

// Logout logs out a user
func (s *AuthService) Logout(ctx context.Context, sessionToken string) error {
	session, err := s.sessionRepo.FindBySessionToken(ctx, sessionToken)
	if err != nil {
		return nil
	}
	return s.sessionRepo.Delete(ctx, session.ID)
}

// RefreshToken refreshes an access token
func (s *AuthService) RefreshToken(ctx context.Context, refreshToken string) (*dto.TokenPair, error) {
	// Find session by refresh token
	session, err := s.sessionRepo.FindByRefreshToken(ctx, refreshToken)
	if err != nil {
		return nil, errors.NewWithCode(errors.ErrAuthInvalidToken)
	}

	// Check if session is expired
	if session.IsExpired() {
		_ = s.sessionRepo.Delete(ctx, session.ID)
		return nil, errors.NewWithCode(errors.ErrAuthTokenExpired)
	}

	// Find user
	user, err := s.userRepo.FindByID(ctx, session.UserID)
	if err != nil {
		return nil, errors.NewWithCode(errors.ErrAuthUserNotFound)
	}

	// Generate new tokens
	tokens, err := s.generateTokens(user)
	if err != nil {
		return nil, err
	}

	// Update session with new tokens
	session.SessionToken = tokens.AccessToken
	session.RefreshToken = tokens.RefreshToken
	session.ExpiresAt = time.Now().Add(7 * 24 * time.Hour)

	if err := s.sessionRepo.Update(ctx, session); err != nil {
		return nil, err
	}

	return tokens, nil
}

// GetCurrentUser gets the current user from token
func (s *AuthService) GetCurrentUser(ctx context.Context, userUUID string) (*model.User, error) {
	return s.userRepo.FindByUUID(ctx, userUUID)
}

// ChangePassword changes a user's password
func (s *AuthService) ChangePassword(ctx context.Context, userUUID string, oldPassword, newPassword string) error {
	user, err := s.userRepo.FindByUUID(ctx, userUUID)
	if err != nil {
		return errors.NewWithCode(errors.ErrAuthUserNotFound)
	}

	// For migration users, old password is not required
	if user.IsPasswordSet() {
		ok, err := utils.VerifyPassword(oldPassword, *user.Password)
		if err != nil || !ok {
			return errors.NewWithCode(errors.ErrAuthInvalidPassword)
		}
	}

	// Hash new password
	hashedPassword, err := utils.HashPassword(newPassword)
	if err != nil {
		return err
	}

	return s.userRepo.UpdatePassword(ctx, userUUID, hashedPassword)
}

// ValidateAccessToken validates an access token and returns claims
func (s *AuthService) ValidateAccessToken(tokenString string) (*utils.TokenClaims, error) {
	claims, err := utils.ParseToken(tokenString, s.cfg.JWT.Secret)
	if err != nil {
		return nil, err
	}
	return claims, nil
}

// generateTokens generates access and refresh tokens
func (s *AuthService) generateTokens(user *model.User) (*dto.TokenPair, error) {
	// Access token (15 minutes)
	accessToken, err := utils.GenerateAccessToken(
		s.cfg.JWT.Secret,
		utils.TokenClaims{
			UserUUID: user.UUID,
			Email:    user.Email,
			Name:     user.Name,
		},
		15*time.Minute,
	)
	if err != nil {
		return nil, err
	}

	// Refresh token (7 days)
	refreshToken, err := utils.GenerateRefreshToken(
		s.cfg.JWT.Secret,
		user.UUID,
		7*24*time.Hour,
	)
	if err != nil {
		return nil, err
	}

	return &dto.TokenPair{
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
	}, nil
}
