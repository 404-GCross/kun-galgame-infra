package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"strings"
	"time"

	"api/internal/platform/auth/dto"
	"api/internal/platform/auth/model"
	authrepo "api/internal/platform/auth/repository"
	sitemodel "api/internal/platform/site/model"
	siterepo "api/internal/platform/site/repository"
	"api/pkg/config"
	"api/pkg/errors"
	"api/pkg/utils"
)

// OAuthService handles OAuth 2.0 logic
type OAuthService struct {
	userRepo     *authrepo.UserRepository
	authCodeRepo *authrepo.AuthorizationCodeRepository
	sessionRepo  *authrepo.SessionRepository
	clientRepo   *siterepo.OAuthClientRepository
	cfg          *config.Config
}

// NewOAuthService creates a new OAuthService
func NewOAuthService(
	userRepo *authrepo.UserRepository,
	authCodeRepo *authrepo.AuthorizationCodeRepository,
	sessionRepo *authrepo.SessionRepository,
	clientRepo *siterepo.OAuthClientRepository,
	cfg *config.Config,
) *OAuthService {
	return &OAuthService{
		userRepo:     userRepo,
		authCodeRepo: authCodeRepo,
		sessionRepo:  sessionRepo,
		clientRepo:   clientRepo,
		cfg:          cfg,
	}
}

// ValidateClient validates an OAuth client
func (s *OAuthService) ValidateClient(ctx context.Context, clientID, redirectURI string) (*sitemodel.OAuthClient, error) {
	client, err := s.clientRepo.FindByClientID(ctx, clientID)
	if err != nil {
		return nil, errors.NewWithCode(errors.ErrOAuthInvalidClient)
	}

	// Validate redirect URI
	if !client.HasRedirectURI(redirectURI) {
		return nil, errors.NewWithCode(errors.ErrOAuthInvalidRedirectURI)
	}

	if !client.IsActive() {
		return nil, errors.NewWithCode(errors.ErrOAuthInvalidClient)
	}

	return client, nil
}

// CreateAuthorizationCode creates a new authorization code
func (s *OAuthService) CreateAuthorizationCode(
	ctx context.Context,
	userID uint,
	clientID string,
	redirectURI string,
	scope string,
	codeChallenge string,
	codeChallengeMethod string,
) (string, error) {
	// Generate secure random code
	codeBytes := make([]byte, 32)
	if _, err := rand.Read(codeBytes); err != nil {
		return "", err
	}
	code := hex.EncodeToString(codeBytes)

	authCode := &model.AuthorizationCode{
		Code:                code,
		ClientID:            clientID,
		UserID:              userID,
		RedirectURI:         redirectURI,
		Scope:               scope,
		CodeChallenge:       codeChallenge,
		CodeChallengeMethod: codeChallengeMethod,
		ExpiresAt:           time.Now().Add(10 * time.Minute), // 10 minutes
	}

	if err := s.authCodeRepo.Create(ctx, authCode); err != nil {
		return "", err
	}

	return code, nil
}

// verifyClientSecret validates the client secret using constant-time comparison
func (s *OAuthService) verifyClientSecret(ctx context.Context, clientID, clientSecret string) error {
	client, err := s.clientRepo.FindByClientID(ctx, clientID)
	if err != nil {
		return errors.NewWithCode(errors.ErrOAuthInvalidClient)
	}
	if subtle.ConstantTimeCompare([]byte(client.Secret), []byte(clientSecret)) != 1 {
		return errors.NewWithCode(errors.ErrOAuthInvalidClientSecret)
	}
	return nil
}

// ExchangeCode exchanges an authorization code for tokens
func (s *OAuthService) ExchangeCode(ctx context.Context, req *dto.TokenRequest) (*dto.TokenResponse, error) {
	// Find authorization code
	authCode, err := s.authCodeRepo.FindValidByCode(ctx, req.Code)
	if err != nil {
		return nil, errors.NewWithCode(errors.ErrOAuthInvalidCode)
	}

	// Validate client
	if authCode.ClientID != req.ClientID {
		return nil, errors.NewWithCode(errors.ErrOAuthInvalidClient)
	}

	// Validate redirect URI
	if authCode.RedirectURI != req.RedirectURI {
		return nil, errors.NewWithCode(errors.ErrOAuthInvalidRedirectURI)
	}

	// Validate client_secret if provided
	hasSecret := req.ClientSecret != ""
	if hasSecret {
		if err := s.verifyClientSecret(ctx, req.ClientID, req.ClientSecret); err != nil {
			return nil, err
		}
	}

	// Validate PKCE if code challenge was provided during authorization
	hasPKCE := authCode.CodeChallenge != ""
	if hasPKCE {
		if !s.verifyCodeVerifier(req.CodeVerifier, authCode.CodeChallenge, authCode.CodeChallengeMethod) {
			return nil, errors.NewWithCode(errors.ErrOAuthInvalidCodeVerifier)
		}
	}

	// At least one client authentication method is required:
	// - Confidential clients must provide client_secret
	// - Public clients must use PKCE
	if !hasSecret && !hasPKCE {
		return nil, errors.NewWithCode(errors.ErrOAuthPKCERequired)
	}

	// Mark code as used
	if err := s.authCodeRepo.MarkAsUsed(ctx, authCode.ID); err != nil {
		return nil, err
	}

	// Get user with roles
	user, err := s.userRepo.FindByIDWithRoles(ctx, authCode.UserID)
	if err != nil {
		return nil, errors.NewWithCode(errors.ErrAuthUserNotFound)
	}

	// Generate tokens with scope embedded in access token
	accessToken, err := utils.GenerateAccessToken(
		s.cfg.JWT.Secret,
		utils.TokenClaims{
			UserUUID: user.UUID,
			Email:    user.Email,
			Name:     user.Name,
			Roles:    user.RoleNames(),
			Scope:    authCode.Scope,
		},
		15*time.Minute,
	)
	if err != nil {
		return nil, err
	}

	refreshToken, err := utils.GenerateRefreshToken(
		s.cfg.JWT.Secret,
		user.UUID,
		7*24*time.Hour,
	)
	if err != nil {
		return nil, err
	}

	// Create session
	session := &model.Session{
		UserID:       user.ID,
		SessionToken: accessToken,
		RefreshToken: refreshToken,
		ExpiresAt:    time.Now().Add(7 * 24 * time.Hour),
	}

	if err := s.sessionRepo.Create(ctx, session); err != nil {
		return nil, err
	}

	return &dto.TokenResponse{
		AccessToken:  accessToken,
		TokenType:    "Bearer",
		ExpiresIn:    900, // 15 minutes in seconds
		RefreshToken: refreshToken,
		Scope:        authCode.Scope,
	}, nil
}

// GetUserInfo returns user info for the authenticated user, filtered by scope.
// OIDC standard scopes:
//   - openid  → sub (always included)
//   - profile → name, picture
//   - email   → email
//
// If scope is empty (e.g. internal /auth/me), all fields are returned.
func (s *OAuthService) GetUserInfo(ctx context.Context, userUUID, scope string) (*dto.UserInfoResponse, error) {
	user, err := s.userRepo.FindByUUID(ctx, userUUID)
	if err != nil {
		return nil, errors.NewWithCode(errors.ErrAuthUserNotFound)
	}

	info := &dto.UserInfoResponse{
		Sub: user.UUID,
	}

	// If no scope specified, return all fields
	if scope == "" {
		info.Name = user.Name
		info.Email = user.Email
		info.Picture = user.Avatar
		return info, nil
	}

	scopes := parseScopes(scope)

	if scopes["profile"] {
		info.Name = user.Name
		info.Picture = user.Avatar
	}
	if scopes["email"] {
		info.Email = user.Email
	}

	return info, nil
}

// parseScopes splits a space-separated scope string into a set
func parseScopes(scope string) map[string]bool {
	result := make(map[string]bool)
	for _, s := range strings.Fields(scope) {
		result[s] = true
	}
	return result
}

// RevokeToken revokes a refresh token
func (s *OAuthService) RevokeToken(ctx context.Context, refreshToken string) error {
	session, err := s.sessionRepo.FindByRefreshToken(ctx, refreshToken)
	if err != nil {
		return nil // Token not found, consider it revoked
	}
	return s.sessionRepo.Delete(ctx, session.ID)
}

// GetUserIDByUUID resolves a user UUID to a user ID
func (s *OAuthService) GetUserIDByUUID(ctx context.Context, uuid string) (uint, error) {
	user, err := s.userRepo.FindByUUID(ctx, uuid)
	if err != nil {
		return 0, err
	}
	return user.ID, nil
}

// RefreshWithClient refreshes tokens using a refresh token within the OAuth flow.
// Requires client_secret for authentication.
func (s *OAuthService) RefreshWithClient(ctx context.Context, refreshToken, clientID, clientSecret string) (*dto.TokenResponse, error) {
	// Validate client_secret (required for refresh_token grant)
	if clientSecret == "" {
		return nil, errors.NewWithCode(errors.ErrOAuthInvalidClientSecret)
	}
	if err := s.verifyClientSecret(ctx, clientID, clientSecret); err != nil {
		return nil, err
	}

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

	// Get user with roles
	user, err := s.userRepo.FindByIDWithRoles(ctx, session.UserID)
	if err != nil {
		return nil, errors.NewWithCode(errors.ErrAuthUserNotFound)
	}

	// Generate new tokens
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

	newRefreshToken, err := utils.GenerateRefreshToken(
		s.cfg.JWT.Secret,
		user.UUID,
		7*24*time.Hour,
	)
	if err != nil {
		return nil, err
	}

	// Update session with new tokens (rotation)
	session.SessionToken = accessToken
	session.RefreshToken = newRefreshToken
	session.ExpiresAt = time.Now().Add(7 * 24 * time.Hour)

	if err := s.sessionRepo.Update(ctx, session); err != nil {
		return nil, err
	}

	return &dto.TokenResponse{
		AccessToken:  accessToken,
		TokenType:    "Bearer",
		ExpiresIn:    900,
		RefreshToken: newRefreshToken,
	}, nil
}

// verifyCodeVerifier verifies the PKCE code verifier
func (s *OAuthService) verifyCodeVerifier(verifier, challenge, method string) bool {
	if method == "plain" {
		return verifier == challenge
	}

	// S256
	h := sha256.Sum256([]byte(verifier))
	computed := base64.RawURLEncoding.EncodeToString(h[:])
	return computed == challenge
}
