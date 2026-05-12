package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	stderrors "errors"
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

// CreateAuthorizationCode creates a new authorization code.
//
// Scope enforcement: the requested `scope` string is split on whitespace
// and every token must appear in the client's AllowedScopes (or be one
// of the OIDC core scopes when AllowedScopes is unset). Any disallowed
// token aborts the request with ErrOAuthInvalidScope — preventing a
// scope-escalation where any registered client could request e.g.
// `image:upload` without explicit grant.
func (s *OAuthService) CreateAuthorizationCode(
	ctx context.Context,
	userID uint,
	clientID string,
	redirectURI string,
	scope string,
	codeChallenge string,
	codeChallengeMethod string,
) (string, error) {
	client, err := s.clientRepo.FindByClientID(ctx, clientID)
	if err != nil {
		return "", errors.NewWithCode(errors.ErrOAuthInvalidClient)
	}
	if _, ok := client.CheckScope(scope); !ok {
		return "", errors.NewWithCode(errors.ErrOAuthInvalidScope)
	}

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

	// Fetch the full client record — we need IsPublic (for confidential
	// secret enforcement below), SiteID (to bind the JWT to a site so
	// image_service can do its cross-site quota check), and Grants (to
	// confirm this client is even allowed to use the authorization_code
	// grant).
	client, err := s.clientRepo.FindByClientID(ctx, req.ClientID)
	if err != nil {
		return nil, errors.NewWithCode(errors.ErrOAuthInvalidClient)
	}

	// Grant-type allow-list. A client created with Grants only
	// ["refresh_token"] (hypothetical) cannot mint tokens via code
	// exchange — and vice versa for the refresh path.
	if !client.IsGrantAllowed("authorization_code") {
		return nil, errors.NewWithCode(errors.ErrOAuthInvalidGrant)
	}

	// Validate redirect URI
	if authCode.RedirectURI != req.RedirectURI {
		return nil, errors.NewWithCode(errors.ErrOAuthInvalidRedirectURI)
	}

	// Authentication rules — strict, type-driven:
	//
	//   confidential client (IsPublic=false): MUST present client_secret
	//     (PKCE may be additionally used, but does NOT replace secret —
	//     otherwise an attacker who steals the auth code only needs the
	//     PKCE verifier from the same client to bypass secret entirely).
	//
	//   public client (IsPublic=true): MUST use PKCE (no secret to give).
	hasSecret := req.ClientSecret != ""
	hasPKCE := authCode.CodeChallenge != ""

	if client.IsPublic {
		if !hasPKCE {
			return nil, errors.NewWithCode(errors.ErrOAuthPKCERequired)
		}
	} else {
		if !hasSecret {
			return nil, errors.NewWithCode(errors.ErrOAuthInvalidClientSecret)
		}
		if err := s.verifyClientSecret(ctx, req.ClientID, req.ClientSecret); err != nil {
			return nil, err
		}
	}

	// Always verify PKCE when a challenge was provided (regardless of
	// client type) — never accept a verifier-less exchange for a code
	// that was issued with a challenge.
	if hasPKCE {
		if !s.verifyCodeVerifier(req.CodeVerifier, authCode.CodeChallenge, authCode.CodeChallengeMethod) {
			return nil, errors.NewWithCode(errors.ErrOAuthInvalidCodeVerifier)
		}
	}

	// Mark code as used — atomic claim. If a concurrent exchange (same
	// `code`, two requests) reached this point, only the winner gets
	// RowsAffected=1. The loser receives ErrCodeAlreadyUsed and must NOT
	// proceed to issue tokens.
	if err := s.authCodeRepo.MarkAsUsed(ctx, authCode.ID); err != nil {
		if stderrors.Is(err, authrepo.ErrCodeAlreadyUsed) {
			return nil, errors.NewWithCode(errors.ErrOAuthInvalidCode)
		}
		return nil, err
	}

	// Get user with roles
	user, err := s.userRepo.FindByIDWithRoles(ctx, authCode.UserID)
	if err != nil {
		return nil, errors.NewWithCode(errors.ErrAuthUserNotFound)
	}

	// SiteID binds this JWT to the client's site. image_service's
	// middleware reads claims.SiteID to enforce that a kungal user's
	// token cannot drive uploads against moyu's quota (or vice versa).
	// Without writing SiteID here the check in image/middleware/auth.go
	// silently degrades to "always allow".
	var siteID uint
	if client.SiteID != nil {
		siteID = *client.SiteID
	}

	// Generate tokens with scope + site_id embedded in access token
	accessToken, err := utils.GenerateAccessToken(
		s.cfg.JWT.Secret,
		utils.TokenClaims{
			UserUUID: user.UUID,
			UID:      user.ID,
			Email:    user.Email,
			Name:     user.Name,
			Roles:    user.RoleNames(),
			Scope:    authCode.Scope,
			SiteID:   siteID,
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

	// Create session. ClientID binds this refresh_token to the issuing
	// OAuth client — refresh requests from a different client_id will
	// be rejected even if they somehow obtained the refresh_token.
	// Scope is persisted so refresh can re-issue with the same scope
	// (otherwise the refreshed token would lose its scope claim and
	// /oauth/userinfo would treat it as "all fields").
	session := &model.Session{
		UserID:       user.ID,
		ClientID:     req.ClientID,
		Scope:        authCode.Scope,
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
//   - openid  → sub, id, roles (always included — core identity)
//   - profile → name, picture
//   - email   → email
//
// If scope is empty (e.g. internal /auth/me), all fields are returned.
//
// `id` and `roles` are returned regardless of scope: `id` is a second
// representation of the same identity as `sub`, and `roles` is already
// embedded in the JWT this caller is using to authenticate. Withholding
// them on a /userinfo call would be theatre — the caller already has them.
func (s *OAuthService) GetUserInfo(ctx context.Context, userUUID, scope string) (*dto.UserInfoResponse, error) {
	user, err := s.userRepo.FindByUUIDWithRoles(ctx, userUUID)
	if err != nil {
		return nil, errors.NewWithCode(errors.ErrAuthUserNotFound)
	}

	roles := make([]string, 0, len(user.Roles))
	for _, r := range user.Roles {
		roles = append(roles, r.Name)
	}

	info := &dto.UserInfoResponse{
		ID:    user.ID,
		Sub:   user.UUID,
		Roles: roles,
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

// RevokeToken revokes a session by either its refresh_token or its
// access_token. RFC 7009 §2.1 requires servers to accept both — clients
// that lost their refresh_token must still be able to revoke their
// active access_token.
//
// Lookup strategy:
//   - hint == "refresh_token": try refresh first, then access as fallback
//   - hint == "access_token":  try access first, then refresh as fallback
//   - hint == "" (or other):   try refresh, then access
//
// "Token not found" is silently treated as success — never leak token
// existence (the handler returns 200 regardless).
func (s *OAuthService) RevokeToken(ctx context.Context, token, hint string) error {
	lookupBySession := func() (uint, bool) {
		if hint == "access_token" {
			if sess, err := s.sessionRepo.FindBySessionToken(ctx, token); err == nil {
				return sess.ID, true
			}
			if sess, err := s.sessionRepo.FindByRefreshToken(ctx, token); err == nil {
				return sess.ID, true
			}
			return 0, false
		}
		// Default + explicit "refresh_token" hint: refresh first
		if sess, err := s.sessionRepo.FindByRefreshToken(ctx, token); err == nil {
			return sess.ID, true
		}
		if sess, err := s.sessionRepo.FindBySessionToken(ctx, token); err == nil {
			return sess.ID, true
		}
		return 0, false
	}

	sessionID, found := lookupBySession()
	if !found {
		return nil // not our token (or already revoked) — treat as success
	}
	return s.sessionRepo.Delete(ctx, sessionID)
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
//
// Authentication rules (RFC 6749 §6 + §2.1):
//   - Confidential clients (default): must present a valid client_secret.
//   - Public clients (oauth_clients.is_public = true, e.g. SPAs using
//     PKCE): the refresh_token itself is the proof of authorization; no
//     client_secret is needed. We still verify the client_id exists.
func (s *OAuthService) RefreshWithClient(ctx context.Context, refreshToken, clientID, clientSecret string) (*dto.TokenResponse, error) {
	client, err := s.clientRepo.FindByClientID(ctx, clientID)
	if err != nil {
		return nil, errors.NewWithCode(errors.ErrOAuthInvalidClient)
	}

	// Grant-type allow-list check. A client whose Grants column doesn't
	// include "refresh_token" cannot use this path — even if it somehow
	// holds a valid refresh_token.
	if !client.IsGrantAllowed("refresh_token") {
		return nil, errors.NewWithCode(errors.ErrOAuthInvalidGrant)
	}

	if client.IsPublic {
		// Public client — no secret required. Refresh-token possession
		// is the proof. (Server still validates the refresh_token below.)
	} else {
		if clientSecret == "" {
			return nil, errors.NewWithCode(errors.ErrOAuthInvalidClientSecret)
		}
		if err := s.verifyClientSecret(ctx, clientID, clientSecret); err != nil {
			return nil, err
		}
	}

	// Find session by refresh token
	session, err := s.sessionRepo.FindByRefreshToken(ctx, refreshToken)
	if err != nil {
		return nil, errors.NewWithCode(errors.ErrAuthInvalidToken)
	}

	// Bind check: a refresh_token can only be redeemed by the client it
	// was originally issued to. This prevents a leaked public-client
	// refresh_token from being used cross-client.
	//
	// Legacy sessions created before this column existed have
	// ClientID="" — they predate the OAuth flow and are only used by
	// /auth/refresh (which doesn't go through this path). So an empty
	// session.ClientID here implies an OAuth-issued session whose
	// client_id was never recorded, which we treat as a security failure.
	if session.ClientID != clientID {
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

	// Generate new tokens. Crucially the new access_token carries the
	// session's original scope AND the client's site_id — without these
	// a refreshed token would lose its scope claim (privacy regression
	// in /oauth/userinfo) and lose its site binding (image_service
	// site-mismatch check would silently allow).
	var siteID uint
	if client.SiteID != nil {
		siteID = *client.SiteID
	}
	accessToken, err := utils.GenerateAccessToken(
		s.cfg.JWT.Secret,
		utils.TokenClaims{
			UserUUID: user.UUID,
			UID:      user.ID,
			Email:    user.Email,
			Name:     user.Name,
			Roles:    user.RoleNames(),
			Scope:    session.Scope,
			SiteID:   siteID,
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

// verifyCodeVerifier verifies the PKCE code verifier.
//
// Only S256 is accepted. An empty `method` is treated as S256 (the OIDC
// default and what most well-behaved clients send). Any other value —
// `plain`, unknown algorithms, garbage — fails closed.
//
// The DTO validator on AuthorizeRequest already rejects non-S256 values
// at the entry; this is defense-in-depth for cases where an auth code
// was created via a different path (e.g. internal API).
func (s *OAuthService) verifyCodeVerifier(verifier, challenge, method string) bool {
	if method != "" && method != "S256" {
		return false
	}
	h := sha256.Sum256([]byte(verifier))
	computed := base64.RawURLEncoding.EncodeToString(h[:])
	return computed == challenge
}
