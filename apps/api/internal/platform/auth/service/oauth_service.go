package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
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

	// Validate PKCE if code challenge was provided
	if authCode.CodeChallenge != "" {
		if !s.verifyCodeVerifier(req.CodeVerifier, authCode.CodeChallenge, authCode.CodeChallengeMethod) {
			return nil, errors.NewWithCode(errors.ErrOAuthInvalidCodeVerifier)
		}
	}

	// Mark code as used
	if err := s.authCodeRepo.MarkAsUsed(ctx, authCode.ID); err != nil {
		return nil, err
	}

	// Get user
	user, err := s.userRepo.FindByID(ctx, authCode.UserID)
	if err != nil {
		return nil, errors.NewWithCode(errors.ErrAuthUserNotFound)
	}

	// Generate tokens
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

// GetUserInfo returns user info for the authenticated user
func (s *OAuthService) GetUserInfo(ctx context.Context, userUUID string) (*dto.UserInfoResponse, error) {
	user, err := s.userRepo.FindByUUID(ctx, userUUID)
	if err != nil {
		return nil, errors.NewWithCode(errors.ErrAuthUserNotFound)
	}

	return &dto.UserInfoResponse{
		Sub:     user.UUID,
		Name:    user.Name,
		Email:   user.Email,
		Picture: user.Avatar,
	}, nil
}

// RevokeToken revokes a refresh token
func (s *OAuthService) RevokeToken(ctx context.Context, refreshToken string) error {
	session, err := s.sessionRepo.FindByRefreshToken(ctx, refreshToken)
	if err != nil {
		return nil // Token not found, consider it revoked
	}
	return s.sessionRepo.Delete(ctx, session.ID)
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
