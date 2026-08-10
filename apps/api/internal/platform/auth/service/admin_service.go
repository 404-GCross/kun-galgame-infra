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

func adminProtected(u *model.User) bool {
	return slices.Contains(u.RoleNames(), "admin")
}

func piiOrRedacted(canSeePII bool, value string) string {
	if canSeePII {
		return value
	}
	return ""
}

func origEmailForAdmin(canSeePII bool, v *string) string {
	if canSeePII && v != nil {
		return *v
	}
	return ""
}

type AdminService struct {
	userRepo     *repository.UserRepository
	sessionRepo  *repository.SessionRepository
	siteRoleRepo *repository.UserSiteRoleRepository
	siteRepo     *siterepo.SiteRepository
	imgClient    *imageclient.Client
}

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

func (s *AdminService) ListUsers(ctx context.Context, req *dto.UserListRequest, canSeePII bool) (*dto.UserListResponse, error) {
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

func (s *AdminService) GetUser(ctx context.Context, uuid string, canSeePII bool) (*dto.UserDetailResponse, error) {
	user, err := s.userRepo.FindByUUIDWithRoles(ctx, uuid)
	if err != nil {
		return nil, errors.NewWithCode(errors.ErrAuthUserNotFound)
	}

	sessionCount, _ := s.sessionRepo.CountByUserID(ctx, user.ID)

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

func (s *AdminService) userRoleNames(ctx context.Context, uuid string) ([]string, error) {
	u, err := s.userRepo.FindByUUIDWithRoles(ctx, uuid)
	if err != nil {
		return nil, err
	}
	return u.RoleNames(), nil
}

func (s *AdminService) UpdateUser(ctx context.Context, uuid string, req *dto.UpdateUserRequest) (*model.User, error) {
	user, err := s.userRepo.FindByUUIDWithRoles(ctx, uuid)
	if err != nil {
		return nil, errors.NewWithCode(errors.ErrAuthUserNotFound)
	}
	wasBanned := user.IsBanned()

	if req.Status != nil && *req.Status == 1 && !wasBanned && adminProtected(user) {
		return nil, errors.NewWithCode(errors.ErrForbidden)
	}

	if req.Name != nil {
		exists, _ := s.userRepo.ExistsByNameExcluding(ctx, *req.Name, uuid)
		if exists {
			return nil, errors.NewWithCode(errors.ErrAuthNameExists)
		}
		user.Name = *req.Name
	}
	if req.Email != nil {
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

	if !wasBanned && user.IsBanned() {
		if err := s.sessionRepo.DeleteByUserID(ctx, user.ID); err != nil {
			slog.Warn("update-user: revoke sessions on ban failed", "user_id", user.ID, "err", err)
		}
	}

	return user, nil
}

func (s *AdminService) BanUser(ctx context.Context, uuid string) error {
	user, err := s.userRepo.FindByUUIDWithRoles(ctx, uuid)
	if err != nil {
		return errors.NewWithCode(errors.ErrAuthUserNotFound)
	}
	if adminProtected(user) {
		return errors.NewWithCode(errors.ErrForbidden)
	}

	user.Status = 1
	if err := s.userRepo.Update(ctx, user); err != nil {
		return err
	}

	if err := s.sessionRepo.DeleteByUserID(ctx, user.ID); err != nil {
		slog.Warn("ban-user: session purge failed; user is banned, sessions linger",
			"user_id", user.ID, "err", err)
	}
	return nil
}

func (s *AdminService) UnbanUser(ctx context.Context, uuid string) error {
	user, err := s.userRepo.FindByUUID(ctx, uuid)
	if err != nil {
		return errors.NewWithCode(errors.ErrAuthUserNotFound)
	}

	if user.IsAnonymized() {
		return errors.NewWithCode(errors.ErrOperationFailed)
	}

	user.Status = 0
	return s.userRepo.Update(ctx, user)
}

func (s *AdminService) AnonymizeUser(ctx context.Context, uuid string) error {
	user, err := s.userRepo.FindByUUIDWithRoles(ctx, uuid)
	if err != nil {
		return errors.NewWithCode(errors.ErrAuthUserNotFound)
	}
	if adminProtected(user) {
		return errors.NewWithCode(errors.ErrForbidden)
	}
	if user.IsAnonymized() {
		return nil
	}

	var oldAvatarHash string
	if user.AvatarImageHash != nil {
		oldAvatarHash = *user.AvatarImageHash
	}

	origEmail := user.Email
	user.OriginalEmail = &origEmail

	user.Name = fmt.Sprintf("已注销#%d", user.ID)
	user.Email = fmt.Sprintf("deleted-%d@anonymized.invalid", user.ID)
	user.Password = nil
	user.KungalPassword = nil
	user.MoyuPassword = nil
	user.Avatar = ""
	user.AvatarImageHash = nil
	user.Bio = ""
	user.IP = ""
	user.Status = 1
	now := time.Now()
	user.AnonymizedAt = &now

	if err := s.userRepo.Update(ctx, user); err != nil {
		return err
	}

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

	if err := s.sessionRepo.DeleteByUserID(ctx, user.ID); err != nil {
		slog.Warn("anonymize: session purge failed; user is anonymized, sessions linger",
			"user_id", user.ID, "err", err)
	}
	return nil
}

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
