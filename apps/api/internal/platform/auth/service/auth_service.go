package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"api/internal/infrastructure/mail"
	"api/internal/platform/auth/dto"
	"api/internal/platform/auth/model"
	"api/internal/platform/auth/repository"
	"api/pkg/config"
	"api/pkg/errors"
	"api/pkg/utils"
)

// AuthService handles authentication logic
type AuthService struct {
	userRepo          *repository.UserRepository
	sessionRepo       *repository.SessionRepository
	passwordResetRepo *repository.PasswordResetRepository
	mailer            *mail.Mailer
	cfg               *config.Config
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

// NewAuthServiceFull creates a new AuthService with all dependencies
func NewAuthServiceFull(
	userRepo *repository.UserRepository,
	sessionRepo *repository.SessionRepository,
	passwordResetRepo *repository.PasswordResetRepository,
	mailer *mail.Mailer,
	cfg *config.Config,
) *AuthService {
	return &AuthService{
		userRepo:          userRepo,
		sessionRepo:       sessionRepo,
		passwordResetRepo: passwordResetRepo,
		mailer:            mailer,
		cfg:               cfg,
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
	// Find user by email or username
	var (
		user *model.User
		err  error
	)
	if strings.Contains(req.Account, "@") {
		user, err = s.userRepo.FindByEmail(ctx, req.Account)
	} else {
		user, err = s.userRepo.FindByName(ctx, req.Account)
	}
	if err != nil {
		return nil, nil, errors.NewWithCode(errors.ErrAuthUserNotFound)
	}

	// Check if user is banned
	if user.IsBanned() {
		return nil, nil, errors.NewWithCode(errors.ErrAuthUnauthorized)
	}

	// Verify password (new system or legacy migration)
	if user.IsPasswordSet() {
		// New system password exists — verify directly
		ok, err := utils.VerifyPassword(req.Password, *user.Password)
		if err != nil || !ok {
			return nil, nil, errors.NewWithCode(errors.ErrAuthInvalidPassword)
		}
	} else if user.HasLegacyPassword() {
		// Try legacy passwords and migrate on success
		migrated := false

		// Try kungal bcrypt
		if user.KungalPassword != nil && *user.KungalPassword != "" {
			if utils.VerifyBcryptPassword(req.Password, *user.KungalPassword) {
				migrated = true
			}
		}

		// Try moyu custom argon2id
		if !migrated && user.MoyuPassword != nil && *user.MoyuPassword != "" {
			if utils.VerifyMoyuPassword(req.Password, *user.MoyuPassword) {
				migrated = true
			}
		}

		if !migrated {
			return nil, nil, errors.NewWithCode(errors.ErrAuthInvalidPassword)
		}

		// Legacy password matched — hash with new system and save
		newHash, err := utils.HashPassword(req.Password)
		if err != nil {
			return nil, nil, err
		}
		if err := s.userRepo.MigrateLegacyPassword(ctx, user.ID, newHash); err != nil {
			return nil, nil, err
		}
	} else {
		// No password at all — need to reset
		return nil, nil, errors.NewWithCode(errors.ErrAuthPasswordRequired)
	}

	// Load roles for JWT claims and response
	userWithRoles, err := s.userRepo.FindByIDWithRoles(ctx, user.ID)
	if err == nil {
		user.Roles = userWithRoles.Roles
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

// GetCurrentUserWithRoles gets the current user with roles preloaded
func (s *AuthService) GetCurrentUserWithRoles(ctx context.Context, userUUID string) (*model.User, error) {
	return s.userRepo.FindByUUIDWithRoles(ctx, userUUID)
}

// ForgotPassword initiates password reset flow
func (s *AuthService) ForgotPassword(ctx context.Context, email string) error {
	// Find user by email
	user, err := s.userRepo.FindByEmail(ctx, email)
	if err != nil {
		// Don't reveal if email exists for security
		return nil
	}

	if s.passwordResetRepo == nil || s.mailer == nil {
		return fmt.Errorf("password reset not configured")
	}

	// Delete any existing reset tokens for this user
	_ = s.passwordResetRepo.DeleteByUserID(ctx, user.ID)

	// Generate reset token
	token, err := generateSecureToken(32)
	if err != nil {
		return err
	}

	// Create password reset record
	reset := &model.PasswordReset{
		UserID:    user.ID,
		Token:     token,
		ExpiresAt: time.Now().Add(1 * time.Hour), // 1 hour expiry
	}

	if err := s.passwordResetRepo.Create(ctx, reset); err != nil {
		return err
	}

	// Send reset email (link to frontend)
	resetLink := fmt.Sprintf("%s/auth/reset-password?token=%s", s.cfg.Server.FrontendURL, token)
	if err := s.mailer.SendPasswordResetEmail(user.Email, user.Name, resetLink); err != nil {
		return fmt.Errorf("failed to send email: %w", err)
	}

	return nil
}

// ResetPassword resets password using token
func (s *AuthService) ResetPassword(ctx context.Context, token, newPassword string) error {
	if s.passwordResetRepo == nil {
		return fmt.Errorf("password reset not configured")
	}

	// Find valid reset token
	reset, err := s.passwordResetRepo.FindValidByToken(ctx, token)
	if err != nil {
		return errors.NewWithCode(errors.ErrAuthInvalidToken)
	}

	// Hash new password
	hashedPassword, err := utils.HashPassword(newPassword)
	if err != nil {
		return err
	}

	// Update user password
	user, err := s.userRepo.FindByID(ctx, reset.UserID)
	if err != nil {
		return errors.NewWithCode(errors.ErrAuthUserNotFound)
	}

	if err := s.userRepo.UpdatePassword(ctx, user.UUID, hashedPassword); err != nil {
		return err
	}

	// Mark token as used
	if err := s.passwordResetRepo.MarkAsUsed(ctx, reset.ID); err != nil {
		return err
	}

	// Delete all sessions for this user (force re-login)
	_ = s.sessionRepo.DeleteByUserID(ctx, user.ID)

	return nil
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
	// Load roles if not already preloaded
	if user.Roles == nil {
		userWithRoles, err := s.userRepo.FindByIDWithRoles(context.Background(), user.ID)
		if err == nil {
			user.Roles = userWithRoles.Roles
		}
	}

	// Access token (15 minutes)
	accessToken, err := utils.GenerateAccessToken(
		s.cfg.JWT.Secret,
		utils.TokenClaims{
			UserUUID: user.UUID,
			Email:    user.Email,
			Name:     user.Name,
			Roles:    user.RoleNames(),
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

// generateSecureToken generates a cryptographically secure random token
func generateSecureToken(length int) (string, error) {
	bytes := make([]byte, length)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes), nil
}
