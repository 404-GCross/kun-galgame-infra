package service

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"math/big"
	"strings"
	"time"

	"api/internal/infrastructure/cache"
	"api/internal/infrastructure/mail"
	"api/internal/platform/auth/dto"
	"api/internal/platform/auth/model"
	"api/internal/platform/auth/repository"
	"api/pkg/config"
	"api/pkg/errors"
	"api/pkg/utils"
)

// emailChangeData stores the verification code and new email in Redis
type emailChangeData struct {
	Code     string `json:"code"`
	NewEmail string `json:"new_email"`
}

// registerGiftPoints is the one-off welcome grant every new account starts
// with ("鲲给予你的第一份礼物").
const registerGiftPoints = 7

// AuthService handles authentication logic
type AuthService struct {
	userRepo          *repository.UserRepository
	sessionRepo       *repository.SessionRepository
	passwordResetRepo *repository.PasswordResetRepository
	mailer            *mail.Mailer
	cache             *cache.RedisCache
	cfg               *config.Config
	// moemoepointSvc grants the registration welcome gift. Optional (nil =
	// no gift) so the lighter NewAuthService constructor / tests stay valid;
	// wired in via WithMoemoepoint during app startup.
	moemoepointSvc *MoemoepointService
}

// WithMoemoepoint wires the moemoepoint service used to grant the registration
// welcome gift. Returns the service for fluent chaining; nil disables the gift.
func (s *AuthService) WithMoemoepoint(mp *MoemoepointService) *AuthService {
	s.moemoepointSvc = mp
	return s
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
	cache *cache.RedisCache,
	cfg *config.Config,
) *AuthService {
	return &AuthService{
		userRepo:          userRepo,
		sessionRepo:       sessionRepo,
		passwordResetRepo: passwordResetRepo,
		mailer:            mailer,
		cache:             cache,
		cfg:               cfg,
	}
}

// SendRegisterCode generates a 6-digit verification code, stashes it in
// Redis under `register_code:{email}`, and emails it to that address. The
// code is consumed by Register; without it, no account can be created
// (per the unified-registration design — see
// docs/integration/oauth/05-registration.md).
//
// Pre-flight checks (name + email uniqueness) run BEFORE generating the
// code, so a user trying to re-register a taken email fails fast without
// burning an email send. They also defend against the race where two
// users send a code for the same name/email simultaneously — Register
// re-checks after consuming the code, but failing here keeps the error
// path local to the send-code call.
//
// Rate limit: per-email Redis key with the same TTL as the code itself
// (cfg.Auth.VerificationCodeTTL, default 15 min) doubles as a "you sent
// a code recently" gate — re-entering SendRegisterCode for the same
// email within the TTL window returns ErrAuthEmailChangeTooFrequent
// (reused error code: same semantic).
func (s *AuthService) SendRegisterCode(ctx context.Context, name, email string) error {
	if s.cache == nil {
		// No Redis = cannot store verification code = cannot guarantee
		// the user is who they say they are. Fail closed instead of
		// silently bypassing verification.
		return fmt.Errorf("cache not configured")
	}

	// Fail-fast uniqueness checks — avoid burning an email send + a
	// Redis slot when the name/email is already taken.
	nameExists, err := s.userRepo.ExistsByName(ctx, name)
	if err != nil {
		return err
	}
	if nameExists {
		return errors.NewWithCode(errors.ErrAuthNameExists)
	}
	emailExists, err := s.userRepo.ExistsByEmail(ctx, email)
	if err != nil {
		return err
	}
	if emailExists {
		return errors.NewWithCode(errors.ErrAuthEmailExists)
	}

	// Rate limit: refuse to send a second code while a previous one is
	// still live. Forces a 10-minute cool-down — long enough to deter
	// spam, short enough that a user who lost the first email can just
	// wait it out. Mirrors SendEmailChangeCode.
	//
	// Keyed by email (lower-cased for safety) — sender identity is the
	// email itself, no user UUID exists yet at this stage.
	emailKey := strings.ToLower(strings.TrimSpace(email))
	redisKey := fmt.Sprintf("register_code:%s", emailKey)
	existing, _ := s.cache.Get(redisKey)
	if existing != nil {
		return errors.NewWithCode(errors.ErrAuthEmailChangeTooFrequent)
	}

	code, err := generateNumericCode(6)
	if err != nil {
		return err
	}

	if err := s.cache.Set(redisKey, []byte(code), s.cfg.Auth.VerificationCodeTTL); err != nil {
		return err
	}

	if s.mailer != nil {
		ttlMinutes := int(s.cfg.Auth.VerificationCodeTTL.Minutes())
		if err := s.mailer.SendRegisterCodeEmail(email, name, code, ttlMinutes); err != nil {
			return fmt.Errorf("failed to send email: %w", err)
		}
	}

	return nil
}

// Register registers a new user and immediately issues an access/refresh
// token pair, mirroring Login's signature. Rationale: per the unified
// registration flow (docs/integration/oauth/05-registration.md), the OAuth
// web frontend must auto-login the freshly-registered user so it can chain
// into /oauth/authorize and complete the SSO round-trip back to kungal /
// moyu / wiki — adding a manual "you registered, now please log in" gate
// would defeat the whole purpose of the redirect handoff.
//
// `req.UserAgent` / `req.IPAddress` are set by the handler (same pattern
// as Login) so the new Session row records the registration context. A
// new user has no roles yet — the empty `Roles` slice on the returned user
// is intentional and the JWT claims reflect that.
//
// `req.Code` is consumed against the Redis token planted by
// SendRegisterCode — proof that the user controls the email address.
// On success the Redis key is deleted so the same code can't be replayed
// (the WriteSnapshot reads-and-checks in this same function, not in a
// separate roundtrip).
func (s *AuthService) Register(ctx context.Context, req *dto.RegisterRequest) (*dto.TokenPair, *model.User, error) {
	if s.cache == nil {
		return nil, nil, fmt.Errorf("cache not configured")
	}

	// Verify the registration code BEFORE any uniqueness checks. Even if
	// the email is already taken, we don't want to leak that fact via
	// a code-failure error — first prove ownership.
	emailKey := strings.ToLower(strings.TrimSpace(req.Email))
	redisKey := fmt.Sprintf("register_code:%s", emailKey)
	storedCode, err := s.cache.Get(redisKey)
	if err != nil || storedCode == nil {
		return nil, nil, errors.NewWithCode(errors.ErrAuthCodeExpired)
	}
	// Constant-time compare to defend against timing side channels on
	// guessing 6-digit codes. Length already validated upstream so a
	// non-equal-length input fails the compare cleanly.
	if subtle.ConstantTimeCompare(storedCode, []byte(req.Code)) != 1 {
		return nil, nil, errors.NewWithCode(errors.ErrAuthCodeInvalid)
	}

	// Check if email exists (defense in depth — SendRegisterCode also
	// checked, but a race could let two parallel registrations slip past
	// that gate).
	exists, err := s.userRepo.ExistsByEmail(ctx, req.Email)
	if err != nil {
		return nil, nil, err
	}
	if exists {
		return nil, nil, errors.NewWithCode(errors.ErrAuthEmailExists)
	}

	// Check if name exists
	exists, err = s.userRepo.ExistsByName(ctx, req.Name)
	if err != nil {
		return nil, nil, err
	}
	if exists {
		return nil, nil, errors.NewWithCode(errors.ErrAuthNameExists)
	}

	// Hash password
	hashedPassword, err := utils.HashPassword(req.Password)
	if err != nil {
		return nil, nil, err
	}

	// Create user
	user := &model.User{
		Name:     req.Name,
		Email:    req.Email,
		Password: &hashedPassword,
	}

	if err := s.userRepo.Create(ctx, user); err != nil {
		return nil, nil, err
	}

	// Issue tokens + create session — identical to Login's tail. No role
	// preload needed: a brand-new user has no roles, generateTokens emits
	// an empty `roles` claim, and the response's `Roles: user.RoleNames()`
	// resolves to `[]` for free.
	tokens, err := s.generateTokens(user)
	if err != nil {
		return nil, nil, err
	}

	session := &model.Session{
		UserID:       user.ID,
		SessionToken: tokens.AccessToken,
		RefreshToken: tokens.RefreshToken,
		UserAgent:    req.UserAgent,
		IPAddress:    req.IPAddress,
		ExpiresAt:    time.Now().Add(7 * 24 * time.Hour),
	}
	if err := s.sessionRepo.Create(ctx, session); err != nil {
		return nil, nil, err
	}

	// Consume the registration code so it can't be replayed (the rate-
	// limit window would naturally expire it after cfg.Auth.VerificationCodeTTL,
	// but deleting now also frees the per-email lock immediately — useful
	// if the user accidentally re-submits and we want a clean error rather
	// than "too frequent"). Best-effort: deletion failure is non-fatal
	// since the account already exists at this point.
	_ = s.cache.Delete(redisKey)

	// Welcome gift: every new account starts with a few moemoepoint
	// ("鲲给予你的第一份礼物"). Best-effort + idempotent (stable key) — a rare
	// failure must not fail an otherwise-successful registration, and the
	// stable key makes a later re-grant a no-op. Reflect the new balance in
	// the returned user so the response shows it immediately.
	if s.moemoepointSvc != nil {
		res, gErr := s.moemoepointSvc.Adjust(ctx, AdjustParams{
			UserID:         user.ID,
			Delta:          registerGiftPoints,
			Reason:         model.MoemoepointReasonRegisterGift,
			SourceApp:      "oauth",
			IdempotencyKey: fmt.Sprintf("oauth:register_gift:%d", user.ID),
			Note:           "鲲给予你的第一份礼物",
		})
		if gErr != nil {
			slog.Warn("register welcome gift failed (best-effort)", "user_id", user.ID, "err", gErr)
		} else {
			user.Moemoepoint = res.Balance
		}
	}

	return tokens, user, nil
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
		// Username login: pre-check format against the same rule as
		// register / change-name. If the input can't possibly be a valid
		// registered username, skip the DB lookup and fail with the same
		// generic error as "user not found" — this avoids leaking "your
		// name format is invalid" vs "no such user" via timing or
		// distinct error codes (account-enumeration defense). Legacy
		// accounts with non-conforming names can still log in via email.
		if !utils.IsValidName(req.Account) {
			return nil, nil, errors.NewWithCode(errors.ErrAuthUserNotFound)
		}
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

// RefreshToken refreshes an access token (the /auth/refresh cookie path
// used by the OAuth admin UI). Same refresh-token rotation grace-window
// logic as OAuthService.RefreshWithClient — see model.Session docs.
func (s *AuthService) RefreshToken(ctx context.Context, refreshToken string) (*dto.TokenPair, error) {
	// Find session by CURRENT or PREVIOUS refresh token.
	session, err := s.sessionRepo.FindByRefreshTokenOrPrev(ctx, refreshToken)
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

	// Defense-in-depth: a user banned via the no-session-revocation path
	// (AdminService.UpdateUser can set Status=1 without deleting sessions)
	// keeps a live session row; revoke it here so the refresh cookie can't
	// keep minting fresh access tokens. Mirrors OAuthService.RefreshWithClient.
	if user.IsBanned() {
		_ = s.sessionRepo.Delete(ctx, session.ID)
		return nil, errors.NewWithCode(errors.ErrAuthUserBanned)
	}

	// Generate new tokens
	tokens, err := s.generateTokens(user)
	if err != nil {
		return nil, err
	}

	if refreshToken != session.RefreshToken {
		// Matched PrevRefreshToken.
		if session.PrevTokenWithinGrace() {
			// Case B: concurrent/retry within grace — don't rotate.
			// Fresh access_token + the CURRENT refresh_token.
			return &dto.TokenPair{
				AccessToken:  tokens.AccessToken,
				RefreshToken: session.RefreshToken,
			}, nil
		}
		// Case C: replay after grace → probable theft → revoke session.
		_ = s.sessionRepo.Delete(ctx, session.ID)
		return nil, errors.NewWithCode(errors.ErrAuthInvalidToken)
	}

	// Case A: matched current — normal rotation.
	now := time.Now()
	session.SessionToken = tokens.AccessToken
	session.PrevRefreshToken = session.RefreshToken
	session.RotatedAt = &now
	session.RefreshToken = tokens.RefreshToken
	session.ExpiresAt = now.Add(7 * 24 * time.Hour)

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

	// Consume the token FIRST (atomic single-use claim). If a later step
	// fails, the residual state is token-consumed-but-password-unchanged,
	// which is fail-safe (user just requests a new link) — never the
	// dangerous password-changed-but-token-still-valid replay window.
	if err := s.passwordResetRepo.MarkAsUsed(ctx, reset.ID); err != nil {
		return err // ErrAuthInvalidToken on already-used / raced replay
	}

	if err := s.userRepo.UpdatePassword(ctx, user.UUID, hashedPassword); err != nil {
		return err
	}

	// Force re-login everywhere. Log (don't silently drop) a purge failure:
	// the reset already succeeded so we don't fail the response, but a failed
	// purge leaving old sessions alive must be visible.
	if err := s.sessionRepo.DeleteByUserID(ctx, user.ID); err != nil {
		slog.Warn("reset-password: session purge failed; old sessions may persist",
			"user_id", user.ID, "err", err)
	}

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
			ID:       user.ID,
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

// SendEmailChangeCode sends a verification code to the user's current email for email change
func (s *AuthService) SendEmailChangeCode(ctx context.Context, userUUID, newEmail string) error {
	user, err := s.userRepo.FindByUUID(ctx, userUUID)
	if err != nil {
		return errors.NewWithCode(errors.ErrAuthUserNotFound)
	}

	// Check if new email is the same as current
	if strings.EqualFold(user.Email, newEmail) {
		return errors.NewWithCode(errors.ErrAuthEmailSameAsCurrent)
	}

	// Check if new email is already taken
	exists, err := s.userRepo.ExistsByEmailExcluding(ctx, newEmail, userUUID)
	if err != nil {
		return err
	}
	if exists {
		return errors.NewWithCode(errors.ErrAuthEmailExists)
	}

	// Rate limit: check if a code was already sent recently
	redisKey := fmt.Sprintf("email_change:%s", userUUID)
	if s.cache != nil {
		existing, _ := s.cache.Get(redisKey)
		if existing != nil {
			return errors.NewWithCode(errors.ErrAuthEmailChangeTooFrequent)
		}
	}

	// Generate 6-digit code
	code, err := generateNumericCode(6)
	if err != nil {
		return err
	}

	// Store in Redis
	if s.cache != nil {
		data, _ := json.Marshal(emailChangeData{Code: code, NewEmail: newEmail})
		if err := s.cache.Set(redisKey, data, s.cfg.Auth.VerificationCodeTTL); err != nil {
			return err
		}
	}

	// Send verification code to the OLD email
	if s.mailer != nil {
		ttlMinutes := int(s.cfg.Auth.VerificationCodeTTL.Minutes())
		if err := s.mailer.SendEmailChangeCodeEmail(user.Email, user.Name, code, ttlMinutes); err != nil {
			return fmt.Errorf("failed to send email: %w", err)
		}
	}

	return nil
}

// UpdateProfile applies a partial profile update for the authenticated
// user (name / avatar / avatar_image_hash / bio). Only non-nil fields in
// `req` are touched.
//
// Returns the updated user with roles preloaded, suitable for projecting
// directly into UserResponse — saves the caller from doing a separate
// FindByUUIDWithRoles.
//
// If `req.Name` is provided and would collide with another user, returns
// ErrAuthNameExists. There is no email validation here — emails go
// through the verified-by-code flow in ChangeEmail.
func (s *AuthService) UpdateProfile(ctx context.Context, userUUID string, req *dto.UpdateProfileRequest) (*model.User, error) {
	fields := map[string]any{}

	if req.Name != nil {
		exists, err := s.userRepo.ExistsByNameExcluding(ctx, *req.Name, userUUID)
		if err != nil {
			return nil, err
		}
		if exists {
			return nil, errors.NewWithCode(errors.ErrAuthNameExists)
		}
		fields["name"] = *req.Name
	}
	if req.Avatar != nil {
		fields["avatar"] = *req.Avatar
	}
	if req.AvatarImageHash != nil {
		// Pointer-typed column. Empty string here clears it (we want NULL
		// semantically, but GORM Updates with a string sets the column to
		// '' which is fine — frontend's resolveAvatarUrl falls back to
		// Avatar when AvatarImageHash is nil OR empty).
		fields["avatar_image_hash"] = *req.AvatarImageHash
	}
	if req.Bio != nil {
		fields["bio"] = *req.Bio
	}

	if err := s.userRepo.UpdateProfile(ctx, userUUID, fields); err != nil {
		return nil, err
	}

	return s.userRepo.FindByUUIDWithRoles(ctx, userUUID)
}

// ChangeEmail verifies the code and updates the user's email
func (s *AuthService) ChangeEmail(ctx context.Context, userUUID, code, newEmail string) error {
	redisKey := fmt.Sprintf("email_change:%s", userUUID)

	// Read from Redis
	if s.cache == nil {
		return fmt.Errorf("cache not configured")
	}

	raw, err := s.cache.Get(redisKey)
	if err != nil || raw == nil {
		return errors.NewWithCode(errors.ErrAuthCodeExpired)
	}

	var data emailChangeData
	if err := json.Unmarshal(raw, &data); err != nil {
		return errors.NewWithCode(errors.ErrAuthCodeInvalid)
	}

	// Verify code and new email match
	if data.Code != code || !strings.EqualFold(data.NewEmail, newEmail) {
		return errors.NewWithCode(errors.ErrAuthCodeInvalid)
	}

	// Re-check email uniqueness (race condition guard)
	exists, err := s.userRepo.ExistsByEmailExcluding(ctx, newEmail, userUUID)
	if err != nil {
		return err
	}
	if exists {
		return errors.NewWithCode(errors.ErrAuthEmailExists)
	}

	// Update email
	if err := s.userRepo.UpdateEmail(ctx, userUUID, newEmail); err != nil {
		return err
	}

	// Delete Redis key
	_ = s.cache.Delete(redisKey)

	return nil
}

// generateNumericCode generates a cryptographically secure N-digit numeric code
func generateNumericCode(length int) (string, error) {
	max := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(length)), nil)
	n, err := rand.Int(rand.Reader, max)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%0*d", length, n), nil
}

// generateSecureToken generates a cryptographically secure random token
func generateSecureToken(length int) (string, error) {
	bytes := make([]byte, length)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes), nil
}
