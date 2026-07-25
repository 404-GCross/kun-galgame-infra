package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	stderrors "errors"
	"fmt"
	"log/slog"
	"net/url"
	"strconv"
	"strings"
	"time"

	"api/internal/platform/auth/dto"
	"api/internal/platform/auth/model"
	authrepo "api/internal/platform/auth/repository"
	sitemodel "api/internal/platform/site/model"
	siterepo "api/internal/platform/site/repository"
	"api/pkg/config"
	"api/pkg/errors"
	"api/pkg/oidctoken"
	"api/pkg/utils"

	"github.com/golang-jwt/jwt/v5"
)

// OAuthService handles OAuth 2.0 logic
type OAuthService struct {
	userRepo     *authrepo.UserRepository
	authCodeRepo *authrepo.AuthorizationCodeRepository
	sessionRepo  *authrepo.SessionRepository
	clientRepo   *siterepo.OAuthClientRepository
	siteRoleRepo *authrepo.UserSiteRoleRepository
	cfg          *config.Config
	// signer mints access tokens. When nil, falls back to the legacy HS256
	// path (cfg.JWT.Secret). Injected via WithTokenSigner (Phase 1).
	signer oidctoken.Signer
	// idSigner mints id_tokens (ES256, always asymmetric). Nil => no id_token
	// issued (e.g. no active key). Injected via WithIDSigner (Phase 2).
	idSigner *oidctoken.IDSigner
}

// WithTokenSigner injects the access-token signer (ES256 when asymmetric signing
// is enabled, else HS256). Returns the service for chaining.
func (s *OAuthService) WithTokenSigner(signer oidctoken.Signer) *OAuthService {
	s.signer = signer
	return s
}

// WithIDSigner injects the id_token signer (ES256). Returns the service for
// chaining; nil disables id_token issuance.
func (s *OAuthService) WithIDSigner(idSigner *oidctoken.IDSigner) *OAuthService {
	s.idSigner = idSigner
	return s
}

// signAccessToken mints an access token via the injected signer, falling back
// to legacy HS256 when no signer is set.
func (s *OAuthService) signAccessToken(claims utils.TokenClaims, ttl time.Duration) (string, error) {
	if s.signer != nil {
		return s.signer.SignAccess(claims, ttl)
	}
	return utils.GenerateAccessToken(s.cfg.JWT.Secret, claims, ttl)
}

// siteRoles returns the user's UNEXPIRED site-role names for siteID — the
// site_roles claim, scoped to the issuing client's site. Returns nil when the
// client isn't site-bound (siteID==0). Best-effort: a lookup error is logged
// and treated as "no site roles" so a transient DB blip degrades the token to
// the user's global roles rather than failing the whole mint.
func (s *OAuthService) siteRoles(ctx context.Context, userID, siteID uint) []string {
	if siteID == 0 || s.siteRoleRepo == nil {
		return nil
	}
	names, err := s.siteRoleRepo.ActiveRoleNames(ctx, userID, siteID)
	if err != nil {
		slog.Warn("site_roles lookup failed; issuing token without them",
			"user_id", userID, "site_id", siteID, "err", err)
		return nil
	}
	return names
}

// NewOAuthService creates a new OAuthService
func NewOAuthService(
	userRepo *authrepo.UserRepository,
	authCodeRepo *authrepo.AuthorizationCodeRepository,
	sessionRepo *authrepo.SessionRepository,
	clientRepo *siterepo.OAuthClientRepository,
	siteRoleRepo *authrepo.UserSiteRoleRepository,
	cfg *config.Config,
) *OAuthService {
	return &OAuthService{
		userRepo:     userRepo,
		authCodeRepo: authCodeRepo,
		sessionRepo:  sessionRepo,
		clientRepo:   clientRepo,
		siteRoleRepo: siteRoleRepo,
		cfg:          cfg,
	}
}

// ClientPublicInfo is the safe-to-expose subset of an OAuth client used
// by the public GET /oauth/client-info endpoint. Deliberately omits
// secret / redirect_uris / scopes / image quotas — anything those could
// reveal infrastructure layout or allow client-spoofing attempts.
//
// Consumed by the OAuth web /oauth/authorize page to decide whether to
// render the consent UI (AutoConsent=true → silent grant) and what name
// to show alongside the "你将授权访问 X" copy.
type ClientPublicInfo struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	AutoConsent bool   `json:"auto_consent"`
	SiteDomain  string `json:"site_domain"`
}

// GetClientPublicInfo fetches the public-safe metadata for a client.
// Returns ErrOAuthInvalidClient when the id doesn't exist; no other
// error path leaks information about valid-vs-invalid ids.
func (s *OAuthService) GetClientPublicInfo(ctx context.Context, clientID string) (*ClientPublicInfo, error) {
	client, err := s.clientRepo.FindByClientIDWithSite(ctx, clientID)
	if err != nil {
		return nil, errors.NewWithCode(errors.ErrOAuthInvalidClient)
	}
	info := &ClientPublicInfo{
		ID:          client.ID,
		Name:        client.Name,
		AutoConsent: client.AutoConsent,
	}
	if client.Site != nil {
		info.SiteDomain = client.Site.Domain
	}
	return info, nil
}

// EcosystemApp is one entry in the public app directory (GET /oauth/ecosystem):
// the "one KUN Galgame account → these sites" strip on register/login. Only
// public display fields — no secret/redirect/scope. See
// docs/integration/oauth/10-app-directory.md.
type EcosystemApp struct {
	Name       string `json:"name"`
	SiteDomain string `json:"site_domain"`
	LogoURL    string `json:"logo_url,omitempty"`
	Tagline    string `json:"tagline,omitempty"`
	// AutoConsent marks a first-party ("官方") site — the downstream strip shows
	// an "官方" chip and these sort ahead of third-party sites (see FindListed).
	AutoConsent bool `json:"auto_consent"`
}

// ListEcosystem returns the opt-in (Listed) clients for the public app
// directory, official (auto_consent) first, then DisplayOrder then Name.
// Public marketing metadata.
func (s *OAuthService) ListEcosystem(ctx context.Context) ([]EcosystemApp, error) {
	clients, err := s.clientRepo.FindListed(ctx)
	if err != nil {
		return nil, err
	}
	apps := make([]EcosystemApp, 0, len(clients))
	for i := range clients {
		c := &clients[i]
		app := EcosystemApp{Name: c.Name, LogoURL: c.LogoURL, Tagline: c.Tagline, AutoConsent: c.AutoConsent}
		if c.Site != nil {
			app.SiteDomain = c.Site.Domain
		}
		apps = append(apps, app)
	}
	return apps, nil
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

// ValidatePostLogoutRedirect checks that a post-logout redirect URL is safe to
// send the browser to: its origin (scheme+host) must match one of the client's
// registered redirect_uris. Returns ("", false) for an unknown client, an
// unparseable URL, or an origin not in the allow-list — the caller then falls
// back to a safe default. This is the open-redirect guard for the logout
// entrypoint. See docs/integration/oauth/07-logout.md.
func (s *OAuthService) ValidatePostLogoutRedirect(ctx context.Context, clientID, redirect string) (string, bool) {
	if redirect == "" || clientID == "" {
		return "", false
	}
	target, err := url.Parse(redirect)
	if err != nil || target.Scheme == "" || target.Host == "" {
		return "", false
	}
	client, err := s.clientRepo.FindByClientID(ctx, clientID)
	if err != nil {
		return "", false
	}
	var uris []string
	if err := json.Unmarshal(client.RedirectURIs, &uris); err != nil {
		return "", false
	}
	for _, u := range uris {
		if ru, err := url.Parse(u); err == nil && ru.Scheme == target.Scheme && ru.Host == target.Host {
			return redirect, true
		}
	}
	return "", false
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
	nonce string,
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
		Nonce:               nonce,
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
	if !client.VerifySecret(clientSecret) {
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

	// Fetch the full client record (with its Site — the site domain becomes
	// the access token's `aud`). We need IsPublic (for confidential secret
	// enforcement below), SiteID (to bind the JWT to a site so image_service
	// can do its cross-site quota check), and Grants (to confirm this client
	// is even allowed to use the authorization_code grant).
	client, err := s.clientRepo.FindByClientIDWithSite(ctx, req.ClientID)
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
	// Block banned/anonymized users from redeeming a pre-existing auth code.
	// The code is independent of sessions, so deleting sessions at ban time
	// doesn't cover this path — without this check a code issued just before
	// the ban (≤10 min TTL) could still mint fresh tokens.
	if user.IsBanned() {
		return nil, errors.NewWithCode(errors.ErrAuthUserBanned)
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

	// Generate tokens with scope + site_id embedded in access token.
	// RFC 9068 claims: `aud` = the client's site domain (the resource-server
	// identifier — see docs/auth/03 §5.1; site_id stays as the claim the
	// resource servers actually check), `client_id` = the requesting client.
	accessToken, err := s.signAccessToken(
		utils.TokenClaims{
			UserUUID:  user.UUID,
			ID:        user.ID,
			Email:     EmailForScope(authCode.Scope, user.Email),
			Name:      user.Name,
			Roles:     user.RoleNames(),
			SiteRoles: s.siteRoles(ctx, user.ID, siteID),
			Scope:     authCode.Scope,
			SiteID:    siteID,
			ClientID:  req.ClientID,
			RegisteredClaims: jwt.RegisteredClaims{
				Audience: clientAudience(client),
			},
		},
		15*time.Minute,
	)
	if err != nil {
		return nil, err
	}

	// refresh_token is an opaque random string (matched by DB value, never
	// signature-verified). Its lifetime is governed by the session row's
	// expires_at (per-client, defaults to 90 days) — see below.
	refreshTokenTTL := client.RefreshTokenTTL()
	refreshToken, err := utils.GenerateOpaqueRefreshToken()
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
		ExpiresAt:    time.Now().Add(refreshTokenTTL),
	}

	if err := s.sessionRepo.Create(ctx, session); err != nil {
		return nil, err
	}

	resp := &dto.TokenResponse{
		AccessToken:  accessToken,
		TokenType:    "Bearer",
		ExpiresIn:    900, // 15 minutes in seconds
		RefreshToken: refreshToken,
		Scope:        authCode.Scope,
	}

	// id_token: issued on the authorization_code grant when the `openid` scope
	// was granted and an id-token signer is configured. Minimal claims; the
	// client's nonce (if any) is echoed. See docs/auth/03 §5.2.
	if s.idSigner != nil && parseScopes(authCode.Scope)["openid"] {
		if idt, err := s.idSigner.Sign(user.UUID, req.ClientID, authCode.Nonce, 15*time.Minute); err != nil {
			slog.Warn("id_token sign failed", "client_id", req.ClientID, "err", err)
		} else {
			resp.IDToken = idt
		}
	}
	return resp, nil
}

// GetUserInfo returns user info for the authenticated user, filtered by scope.
// OIDC standard scopes:
//   - openid  → sub, id, roles (always included — core identity)
//   - profile → name, picture
//   - email   → email
//
// If scope is empty, all fields are returned — see ScopeGrants for why
// (a first-party password-login token negotiated no scope at all).
//
// `id` and `roles` are returned regardless of scope: `id` is a second
// representation of the same identity as `sub`, and `roles` is already
// embedded in the JWT this caller is using to authenticate. Withholding
// them on a /userinfo call would be theatre — the caller already has them.
func (s *OAuthService) GetUserInfo(ctx context.Context, userUUID, scope string, siteID uint) (*dto.UserInfoResponse, error) {
	user, err := s.userRepo.FindByUUIDWithRoles(ctx, userUUID)
	if err != nil {
		return nil, errors.NewWithCode(errors.ErrAuthUserNotFound)
	}

	roles := make([]string, 0, len(user.Roles))
	for _, r := range user.Roles {
		roles = append(roles, r.Name)
	}

	info := &dto.UserInfoResponse{
		ID:        user.ID,
		Sub:       user.UUID,
		Roles:     roles,
		SiteRoles: s.siteRoles(ctx, user.ID, siteID),
		UpdatedAt: user.UpdatedAt.Unix(), // documented Unix-timestamp contract
	}

	if ScopeGrants(scope, "profile") {
		info.Name = user.Name
		info.Picture = s.resolveAvatar(user)
	}
	// UserInfoResponse.Email carries omitempty, so a withheld address becomes an
	// absent key here — unlike /auth/me, where the field stays present and empty.
	// Both shapes are pinned in docs/integration/oauth/01 + 02.
	info.Email = EmailForScope(scope, user.Email)

	return info, nil
}

// resolveAvatar mirrors the frontend resolveAvatarUrl / imageHashUrl: prefer
// the image_service hash (build the main webp CDN URL), else the legacy
// avatar URL. Users who only ever uploaded via image_service have an empty
// legacy `avatar`, so without this userinfo.picture would be blank for them.
func (s *OAuthService) resolveAvatar(u *model.User) string {
	if u.AvatarImageHash != nil && len(*u.AvatarImageHash) >= 4 {
		h := *u.AvatarImageHash
		base := strings.TrimRight(s.cfg.ImageService.CDNBase, "/")
		return fmt.Sprintf("%s/%s/%s/%s.webp", base, h[0:2], h[2:4], h)
	}
	return u.Avatar
}

// clientAudience returns the access token's `aud` (RFC 9068): the client's
// site domain — the resource-server identifier per docs/auth/03 §5.1. Nil
// (claim omitted) for clients not bound to a site.
func clientAudience(client *sitemodel.OAuthClient) jwt.ClaimStrings {
	if client.Site != nil && client.Site.Domain != "" {
		return jwt.ClaimStrings{client.Site.Domain}
	}
	return nil
}

// parseScopes splits a space-separated scope string into a set
func parseScopes(scope string) map[string]bool {
	result := make(map[string]bool)
	for _, s := range strings.Fields(scope) {
		result[s] = true
	}
	return result
}

// ScopeGrants reports whether a token's granted scope includes `want`.
//
// An empty scope means no OAuth scope was ever negotiated — the token came
// from a first-party password login at the account center, not from an
// authorization-code grant — and grants every field. That is why
// /auth/login tokens still see the full profile.
//
// Exported because the same rule gates PII outside this package (GET/PATCH
// /auth/me, see handler.visibleEmail): a second copy of "empty means all"
// would be free to drift away from what /oauth/userinfo enforces.
func ScopeGrants(scope, want string) bool {
	if strings.TrimSpace(scope) == "" {
		return true
	}
	return parseScopes(scope)[want]
}

// EmailForScope returns the address only when the granted scope includes
// `email`, and "" otherwise. It is the one gate every surface that can hand a
// user's address to a client goes through: the access token's `email` claim,
// /oauth/userinfo, and GET/PATCH /auth/me.
//
// Only this one PII field is gated. The display fields (name, avatar, bio,
// moemoepoint, roles) stay open because any client can already read them via
// /users/batch — withholding them would break downstreams without buying any
// privacy.
func EmailForScope(scope, email string) string {
	if ScopeGrants(scope, "email") {
		return email
	}
	return ""
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
	// Site preloaded: its domain is the refreshed access token's `aud`.
	client, err := s.clientRepo.FindByClientIDWithSite(ctx, clientID)
	if err != nil {
		slog.Warn("oauth refresh reject", "stage", "client_lookup", "client_id", clientID, "err", err)
		return nil, errors.NewWithCode(errors.ErrOAuthInvalidClient)
	}

	// Grant-type allow-list check. A client whose Grants column doesn't
	// include "refresh_token" cannot use this path — even if it somehow
	// holds a valid refresh_token.
	if !client.IsGrantAllowed("refresh_token") {
		slog.Warn("oauth refresh reject", "stage", "grant_not_allowed", "client_id", clientID, "grants", client.Grants)
		return nil, errors.NewWithCode(errors.ErrOAuthInvalidGrant)
	}

	if client.IsPublic {
		// Public client — no secret required. Refresh-token possession
		// is the proof. (Server still validates the refresh_token below.)
	} else {
		if clientSecret == "" {
			slog.Warn("oauth refresh reject", "stage", "missing_secret", "client_id", clientID)
			return nil, errors.NewWithCode(errors.ErrOAuthInvalidClientSecret)
		}
		if err := s.verifyClientSecret(ctx, clientID, clientSecret); err != nil {
			slog.Warn("oauth refresh reject", "stage", "bad_secret", "client_id", clientID)
			return nil, err
		}
	}

	// Find session by CURRENT or PREVIOUS refresh token. The grace-window
	// logic below distinguishes the two.
	session, err := s.sessionRepo.FindByRefreshTokenOrPrev(ctx, refreshToken)
	if err != nil {
		slog.Warn("oauth refresh reject", "stage", "session_not_found",
			"client_id", clientID, "refresh_token_fp", tokenFingerprint(refreshToken), "err", err)
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
		slog.Warn("oauth refresh reject", "stage", "client_id_mismatch",
			"request_client_id", clientID, "session_client_id", session.ClientID,
			"session_id", session.ID, "user_id", session.UserID)
		return nil, errors.NewWithCode(errors.ErrAuthInvalidToken)
	}

	// Check if session is expired
	if session.IsExpired() {
		slog.Warn("oauth refresh reject", "stage", "session_expired",
			"session_id", session.ID, "user_id", session.UserID, "expires_at", session.ExpiresAt)
		_ = s.sessionRepo.Delete(ctx, session.ID)
		return nil, errors.NewWithCode(errors.ErrAuthTokenExpired)
	}

	// Get user with roles
	user, err := s.userRepo.FindByIDWithRoles(ctx, session.UserID)
	if err != nil {
		return nil, errors.NewWithCode(errors.ErrAuthUserNotFound)
	}
	// Defense-in-depth: ban deletes sessions (so refresh normally fails at
	// session lookup above), but re-check here so a lingering/raced session
	// can't refresh a banned/anonymized user's token.
	if user.IsBanned() {
		return nil, errors.NewWithCode(errors.ErrAuthUserBanned)
	}

	var siteID uint
	if client.SiteID != nil {
		siteID = *client.SiteID
	}

	// freshAccess mints a new 15-min access_token carrying the session's
	// scope + the client's site_id (without these a refreshed token would
	// lose its scope claim — privacy regression in /oauth/userinfo — and
	// its site binding — image_service cross-site check silently allows).
	freshAccess := func() (string, error) {
		return s.signAccessToken(
			utils.TokenClaims{
				UserUUID:  user.UUID,
				ID:        user.ID,
				Email:     EmailForScope(session.Scope, user.Email),
				Name:      user.Name,
				Roles:     user.RoleNames(),
				SiteRoles: s.siteRoles(ctx, user.ID, siteID),
				Scope:     session.Scope,
				SiteID:    siteID,
				ClientID:  clientID,
				RegisteredClaims: jwt.RegisteredClaims{
					Audience: clientAudience(client),
				},
			},
			15*time.Minute,
		)
	}

	// ── Refresh-token rotation with grace window ──────────────────────
	//
	// The presented token matched the session via current OR previous
	// column. Branch on which:
	if refreshToken != session.RefreshToken {
		// Matched PrevRefreshToken.
		if session.PrevTokenWithinGrace() {
			// Case B: a concurrent/retry caller racing a rotation that
			// already happened (the norm for SSR proxies firing several
			// in-flight requests when the access_token expires). DO NOT
			// rotate again — hand back a fresh access_token plus the
			// CURRENT refresh_token so this caller converges on the
			// winning rotation's token. Idempotent within the window.
			accessToken, gerr := freshAccess()
			if gerr != nil {
				return nil, gerr
			}
			slog.Info("oauth refresh grace-replay",
				"client_id", clientID, "session_id", session.ID, "user_id", session.UserID,
				"presented_fp", tokenFingerprint(refreshToken))
			return &dto.TokenResponse{
				AccessToken:  accessToken,
				TokenType:    "Bearer",
				ExpiresIn:    900,
				RefreshToken: session.RefreshToken,
			}, nil
		}
		// Case C: previous token presented AFTER the grace window — this
		// is replay of a long-rotated token = probable theft. Revoke the
		// whole session (defensive; forces a clean re-login).
		slog.Warn("oauth refresh reuse-detected; revoking session",
			"client_id", clientID, "session_id", session.ID, "user_id", session.UserID,
			"presented_fp", tokenFingerprint(refreshToken),
			"rotated_at", session.RotatedAt)
		_ = s.sessionRepo.Delete(ctx, session.ID)
		return nil, errors.NewWithCode(errors.ErrAuthInvalidToken)
	}

	// Case A: matched the CURRENT refresh_token — normal rotation.
	accessToken, err := freshAccess()
	if err != nil {
		return nil, err
	}

	// refresh_token rotation uses the client's current TTL — admins
	// shortening the TTL effectively rolls out via natural refresh.
	refreshTokenTTL := client.RefreshTokenTTL()
	newRefreshToken, err := utils.GenerateOpaqueRefreshToken()
	if err != nil {
		return nil, err
	}

	// Rotate atomically (compare-and-swap on the presented token). Two
	// concurrent refreshes that both read this current token would otherwise
	// each Save a different new token (lost update) → cookie/DB desync → the
	// next refresh presents a token that is neither current nor prev → a 401
	// that even a reload can't fix. The CAS lets exactly one win; the loser
	// converges on the winner's token below. (The grace window only catches the
	// sequential case where the racer presents the PREVIOUS token; it cannot fix
	// truly-overlapping rotations, which both take this current-token path.)
	now := time.Now()
	won, err := s.sessionRepo.RotateRefreshToken(
		ctx, session.ID, refreshToken, accessToken, newRefreshToken,
		now, now.Add(refreshTokenTTL),
	)
	if err != nil {
		slog.Error("oauth refresh reject", "stage", "session_update_failed",
			"session_id", session.ID, "user_id", session.UserID, "err", err)
		return nil, err
	}
	if !won {
		// Lost the race: a concurrent refresh rotated first. Converge on the
		// winner's current refresh token instead of returning a discarded one.
		fresh, ferr := s.sessionRepo.FindByID(ctx, session.ID)
		if ferr != nil {
			return nil, errors.NewWithCode(errors.ErrAuthInvalidToken)
		}
		slog.Info("oauth refresh rotation-race converged",
			"client_id", clientID, "session_id", session.ID, "user_id", session.UserID,
			"presented_fp", tokenFingerprint(refreshToken))
		return &dto.TokenResponse{
			AccessToken:  accessToken,
			TokenType:    "Bearer",
			ExpiresIn:    900,
			RefreshToken: fresh.RefreshToken,
		}, nil
	}

	slog.Debug("oauth refresh ok",
		"client_id", clientID, "session_id", session.ID, "user_id", session.UserID,
		"old_rt_fp", tokenFingerprint(refreshToken),
		"new_rt_fp", tokenFingerprint(newRefreshToken),
	)

	return &dto.TokenResponse{
		AccessToken:  accessToken,
		TokenType:    "Bearer",
		ExpiresIn:    900,
		RefreshToken: newRefreshToken,
	}, nil
}

// tokenFingerprint returns a non-reversible short identifier for a token
// suitable for logs: the first 8 chars + total length. Never log the raw
// token — it's a bearer credential.
func tokenFingerprint(tok string) string {
	if tok == "" {
		return "(empty)"
	}
	n := len(tok)
	prefix := tok
	if n > 8 {
		prefix = tok[:8]
	}
	return prefix + "..len=" + strconv.Itoa(n)
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
