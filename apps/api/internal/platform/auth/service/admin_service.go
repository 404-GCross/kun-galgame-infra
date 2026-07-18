package service

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"api/internal/platform/auth/dto"
	"api/internal/platform/auth/model"
	"api/internal/platform/auth/repository"
	siterepo "api/internal/platform/site/repository"
	"api/pkg/errors"
	"api/pkg/imageclient"
)

// adminProtected reports whether the target is an admin. Destructive admin-API
// ops (ban / anonymize / force-logout) are refused on admins so a single rogue
// or compromised admin can't lock out, force-logout, or irreversibly wipe a
// peer admin or the platform owner (there is no superadmin tier; admin is the
// top). To act on an admin, first demote them (remove the admin role) or use
// direct DB access.
func adminProtected(u *model.User) bool {
	return slices.Contains(u.RoleNames(), "admin")
}

// piiOrRedacted returns the value only when the caller may see PII (the ren
// role); otherwise it returns "". Email and (in the detail view) IP are the
// two contact identifiers ren-gated out of the admin user list — ordinary
// admins manage users without seeing how to reach them off-platform.
func piiOrRedacted(canSeePII bool, value string) string {
	if canSeePII {
		return value
	}
	return ""
}

// origEmailForAdmin returns the preserved pre-anonymize email, but only to ren
// callers (canSeePII). Empty (→ omitempty) when not ren or never set — so the
// retained PII reaches the admin UI for ren alone. nil-safe.
func origEmailForAdmin(canSeePII bool, v *string) string {
	if canSeePII && v != nil {
		return *v
	}
	return ""
}

// AdminService handles admin operations
type AdminService struct {
	userRepo     *repository.UserRepository
	sessionRepo  *repository.SessionRepository
	siteRoleRepo *repository.UserSiteRoleRepository
	siteRepo     *siterepo.SiteRepository
	// imgClient GCs a user's image_service avatar on anonymize. nil when the
	// image client is unconfigured — anonymize then just nulls the reference
	// and lets image_service's own GC reclaim the binary.
	imgClient *imageclient.Client
}

// NewAdminService creates a new AdminService. imgClient may be nil.
func NewAdminService(
	userRepo *repository.UserRepository,
	sessionRepo *repository.SessionRepository,
	siteRoleRepo *repository.UserSiteRoleRepository,
	siteRepo *siterepo.SiteRepository,
	imgClient *imageclient.Client,
) *AdminService {
	return &AdminService{
		userRepo:     userRepo,
		sessionRepo:  sessionRepo,
		siteRoleRepo: siteRoleRepo,
		siteRepo:     siteRepo,
		imgClient:    imgClient,
	}
}

// ListUsers returns a paginated list of users. canSeePII gates the per-user
// email column to ren callers (see piiOrRedacted).
func (s *AdminService) ListUsers(ctx context.Context, req *dto.UserListRequest, canSeePII bool) (*dto.UserListResponse, error) {
	// Set defaults
	if req.Page < 1 {
		req.Page = 1
	}
	if req.Limit < 1 || req.Limit > 100 {
		req.Limit = 20
	}

	users, total, err := s.userRepo.FindAllPaginated(ctx, req.Page, req.Limit, req.Search, req.Status, req.SortBy, req.SortDesc)
	if err != nil {
		return nil, err
	}

	// Convert to response
	userResponses := make([]dto.UserResponse, len(users))
	for i, user := range users {
		userResponses[i] = dto.UserResponse{
			ID:              user.ID,
			UUID:            user.UUID,
			Name:            user.Name,
			Email:           piiOrRedacted(canSeePII, user.Email),
			Avatar:          user.Avatar,
			AvatarImageHash: user.AvatarImageHash,
			Bio:             user.Bio,
			Moemoepoint:     user.Moemoepoint,
			Status:          user.Status,
			IsAnonymized:    user.IsAnonymized(),
			OriginalEmail:   origEmailForAdmin(canSeePII, user.OriginalEmail),
			Roles:           user.RoleNames(),
			CreatedAt:       user.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"),
		}
	}

	totalPages := int(total) / req.Limit
	if int(total)%req.Limit > 0 {
		totalPages++
	}

	return &dto.UserListResponse{
		Users:      userResponses,
		Total:      total,
		Page:       req.Page,
		Limit:      req.Limit,
		TotalPages: totalPages,
	}, nil
}

// GetUser returns a user by UUID. canSeePII gates email + IP to ren callers
// (see piiOrRedacted).
func (s *AdminService) GetUser(ctx context.Context, uuid string, canSeePII bool) (*dto.UserDetailResponse, error) {
	user, err := s.userRepo.FindByUUIDWithRoles(ctx, uuid)
	if err != nil {
		return nil, errors.NewWithCode(errors.ErrAuthUserNotFound)
	}

	// Get session count
	sessionCount, _ := s.sessionRepo.CountByUserID(ctx, user.ID)

	// Site-scoped role grants (best-effort — a lookup error just omits them).
	siteRoles, _ := s.listSiteRoles(ctx, user.ID)

	return &dto.UserDetailResponse{
		UserResponse: dto.UserResponse{
			ID:              user.ID,
			UUID:            user.UUID,
			Name:            user.Name,
			Email:           piiOrRedacted(canSeePII, user.Email),
			Avatar:          user.Avatar,
			AvatarImageHash: user.AvatarImageHash,
			Bio:             user.Bio,
			Moemoepoint:     user.Moemoepoint,
			Status:          user.Status,
			IsAnonymized:    user.IsAnonymized(),
			OriginalEmail:   origEmailForAdmin(canSeePII, user.OriginalEmail),
			Roles:           user.RoleNames(),
			CreatedAt:       user.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"),
		},
		IP:           piiOrRedacted(canSeePII, user.IP),
		SessionCount: int(sessionCount),
		SiteRoles:    siteRoles,
	}, nil
}

// AssignRole grants a role to a user and returns the user's resulting role
// names. WHO may grant WHICH role is enforced by the handler
// (callerCanManageRole); this method only performs the (idempotent) change.
func (s *AdminService) AssignRole(ctx context.Context, uuid, role string) ([]string, error) {
	user, err := s.userRepo.FindByUUID(ctx, uuid)
	if err != nil {
		return nil, errors.NewWithCode(errors.ErrAuthUserNotFound)
	}
	if err := s.userRepo.AddRole(ctx, user.ID, role); err != nil {
		return nil, err
	}
	return s.userRoleNames(ctx, uuid)
}

// RevokeRole removes a role from a user and returns the resulting role names.
func (s *AdminService) RevokeRole(ctx context.Context, uuid, role string) ([]string, error) {
	user, err := s.userRepo.FindByUUID(ctx, uuid)
	if err != nil {
		return nil, errors.NewWithCode(errors.ErrAuthUserNotFound)
	}
	if err := s.userRepo.RemoveRole(ctx, user.ID, role); err != nil {
		return nil, err
	}
	return s.userRoleNames(ctx, uuid)
}

// userRoleNames reloads a user's role names after a grant/revoke.
func (s *AdminService) userRoleNames(ctx context.Context, uuid string) ([]string, error) {
	u, err := s.userRepo.FindByUUIDWithRoles(ctx, uuid)
	if err != nil {
		return nil, err
	}
	return u.RoleNames(), nil
}

// UpdateUser updates a user
func (s *AdminService) UpdateUser(ctx context.Context, uuid string, req *dto.UpdateUserRequest) (*model.User, error) {
	user, err := s.userRepo.FindByUUIDWithRoles(ctx, uuid)
	if err != nil {
		return nil, errors.NewWithCode(errors.ErrAuthUserNotFound)
	}
	wasBanned := user.IsBanned()

	// Banning an admin through this generic edit path carries the same
	// destructive force as BanUser, so gate it identically — otherwise
	// UpdateUser is a bypass of the adminProtected check (a rogue admin could
	// lock out a peer admin via Status=1). Non-ban edits and unbanning a
	// (somehow) banned admin stay allowed.
	if req.Status != nil && *req.Status == 1 && !wasBanned && adminProtected(user) {
		return nil, errors.NewWithCode(errors.ErrForbidden)
	}

	// Update fields
	if req.Name != nil {
		// Check if name is taken
		exists, _ := s.userRepo.ExistsByNameExcluding(ctx, *req.Name, uuid)
		if exists {
			return nil, errors.NewWithCode(errors.ErrAuthNameExists)
		}
		user.Name = *req.Name
	}
	if req.Email != nil {
		// Check if email is taken
		exists, _ := s.userRepo.ExistsByEmailExcluding(ctx, *req.Email, uuid)
		if exists {
			return nil, errors.NewWithCode(errors.ErrAuthEmailExists)
		}
		user.Email = *req.Email
	}
	if req.Avatar != nil {
		user.Avatar = *req.Avatar
	}
	if req.Bio != nil {
		user.Bio = *req.Bio
	}
	if req.Status != nil {
		user.Status = *req.Status
	}

	if err := s.userRepo.Update(ctx, user); err != nil {
		return nil, err
	}

	// Newly banned via this path → revoke sessions so behavior matches
	// BanUser (otherwise a lingering session can keep refreshing tokens; the
	// AuthService.RefreshToken guard is the second line of defense).
	if !wasBanned && user.IsBanned() {
		if err := s.sessionRepo.DeleteByUserID(ctx, user.ID); err != nil {
			slog.Warn("update-user: revoke sessions on ban failed", "user_id", user.ID, "err", err)
		}
	}

	return user, nil
}

// BanUser bans a user
func (s *AdminService) BanUser(ctx context.Context, uuid string) error {
	user, err := s.userRepo.FindByUUIDWithRoles(ctx, uuid)
	if err != nil {
		return errors.NewWithCode(errors.ErrAuthUserNotFound)
	}
	if adminProtected(user) {
		return errors.NewWithCode(errors.ErrForbidden)
	}

	user.Status = 1 // 1 = banned
	if err := s.userRepo.Update(ctx, user); err != nil {
		return err
	}

	// Best-effort session revoke: the ban (status=1) is the security boundary —
	// the live Auth middleware re-checks IsBanned per request, so a lingering
	// session can't act. Log a purge failure rather than reporting the whole
	// (already-succeeded) ban as a failure. Matches UpdateUser's ban path.
	if err := s.sessionRepo.DeleteByUserID(ctx, user.ID); err != nil {
		slog.Warn("ban-user: session purge failed; user is banned, sessions linger",
			"user_id", user.ID, "err", err)
	}
	return nil
}

// UnbanUser unbans a user
func (s *AdminService) UnbanUser(ctx context.Context, uuid string) error {
	user, err := s.userRepo.FindByUUID(ctx, uuid)
	if err != nil {
		return errors.NewWithCode(errors.ErrAuthUserNotFound)
	}

	// Anonymization is terminal — its PII scrub can't be undone, so don't
	// let "unban" silently resurrect a half-wiped account.
	if user.IsAnonymized() {
		return errors.NewWithCode(errors.ErrOperationFailed)
	}

	user.Status = 0 // 0 = normal
	return s.userRepo.Update(ctx, user)
}

// AnonymizeUser scrubs a user's PII and locks the account (Status=1 +
// AnonymizedAt). Irreversible — for severe spam / PII abuse. It deliberately
// does NOT delete the user row: the id/uuid are referenced as foreign keys
// across multiple services (galgame.user_id, galgame_message.actor_user_id,
// image first_uploader_sub, …), so a hard delete would orphan those and
// break joins / revision history everywhere. Keeping the row with scrubbed
// fields lets downstream render "已注销用户" instead. Also revokes all
// sessions (force logout across every OAuth client) and GCs the avatar.
func (s *AdminService) AnonymizeUser(ctx context.Context, uuid string) error {
	user, err := s.userRepo.FindByUUIDWithRoles(ctx, uuid)
	if err != nil {
		return errors.NewWithCode(errors.ErrAuthUserNotFound)
	}
	if adminProtected(user) {
		return errors.NewWithCode(errors.ErrForbidden)
	}
	if user.IsAnonymized() {
		return nil // already anonymized — idempotent no-op
	}

	// Capture the avatar hash before scrubbing so we can GC it afterward.
	var oldAvatarHash string
	if user.AvatarImageHash != nil {
		oldAvatarHash = *user.AvatarImageHash
	}

	// Preserve the original email before scrubbing (备查). Copy the value —
	// user.Email is overwritten on the next line. The IsAnonymized() early
	// return above keeps this idempotent: a second anonymize won't clobber
	// OriginalEmail with the deleted- placeholder.
	origEmail := user.Email
	user.OriginalEmail = &origEmail

	// Name + Email are NOT NULL + unique, so replace with deterministic
	// placeholders derived from the immutable id (guaranteed unique, and
	// short enough for the name's varchar(17)).
	user.Name = fmt.Sprintf("已注销#%d", user.ID)
	user.Email = fmt.Sprintf("deleted-%d@anonymized.invalid", user.ID)
	user.Password = nil
	user.KungalPassword = nil
	user.MoyuPassword = nil
	user.Avatar = ""
	user.AvatarImageHash = nil
	user.Bio = ""
	user.IP = ""
	user.Status = 1 // banned — IsBanned() locks them out
	now := time.Now()
	user.AnonymizedAt = &now // terminal marker (distinct from a plain ban)

	if err := s.userRepo.Update(ctx, user); err != nil {
		return err
	}

	// GC the avatar binary (best-effort — never fails the anonymize). Only
	// when (a) the image client is configured, and (b) no OTHER user shares
	// the hash, since image_service dedups content and has no refcount; the
	// soft-delete is also site-scoped + recoverable-until-GC on that side.
	if s.imgClient != nil && oldAvatarHash != "" {
		others, cErr := s.userRepo.CountByAvatarHash(ctx, oldAvatarHash, user.ID)
		switch {
		case cErr != nil:
			slog.Warn("anonymize: avatar ref-count failed; skipping GC", "user_id", user.ID, "err", cErr)
		case others > 0:
			slog.Info("anonymize: avatar hash shared, leaving binary", "user_id", user.ID, "shared_by", others)
		default:
			if dErr := s.imgClient.Delete(ctx, oldAvatarHash); dErr != nil {
				slog.Warn("anonymize: avatar GC failed (reference already cleared)", "user_id", user.ID, "err", dErr)
			}
		}
	}

	// Force logout everywhere — kills all refresh tokens across all clients.
	// Best-effort: the PII scrub + status=1 already committed; a session-purge
	// failure must not report the (succeeded, irreversible) anonymize as failed.
	if err := s.sessionRepo.DeleteByUserID(ctx, user.ID); err != nil {
		slog.Warn("anonymize: session purge failed; user is anonymized, sessions linger",
			"user_id", user.ID, "err", err)
	}
	return nil
}

// DeleteUserSessions deletes all sessions for a user
func (s *AdminService) DeleteUserSessions(ctx context.Context, uuid string) error {
	user, err := s.userRepo.FindByUUIDWithRoles(ctx, uuid)
	if err != nil {
		return errors.NewWithCode(errors.ErrAuthUserNotFound)
	}
	if adminProtected(user) {
		return errors.NewWithCode(errors.ErrForbidden)
	}

	return s.sessionRepo.DeleteByUserID(ctx, user.ID)
}
