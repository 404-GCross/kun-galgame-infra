package service

import (
	"context"

	"api/internal/platform/auth/dto"
	"api/internal/platform/auth/model"
	"api/internal/platform/auth/repository"
	"api/pkg/errors"
)

// AdminService handles admin operations
type AdminService struct {
	userRepo    *repository.UserRepository
	sessionRepo *repository.SessionRepository
}

// NewAdminService creates a new AdminService
func NewAdminService(
	userRepo *repository.UserRepository,
	sessionRepo *repository.SessionRepository,
) *AdminService {
	return &AdminService{
		userRepo:    userRepo,
		sessionRepo: sessionRepo,
	}
}

// ListUsers returns a paginated list of users
func (s *AdminService) ListUsers(ctx context.Context, req *dto.UserListRequest) (*dto.UserListResponse, error) {
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
			UUID:        user.UUID,
			Name:        user.Name,
			Email:       user.Email,
			Avatar:      user.Avatar,
			Bio:         user.Bio,
			Moemoepoint: user.Moemoepoint,
			Status:      user.Status,
			Roles:       user.RoleNames(),
			CreatedAt:   user.CreatedAt.Format("2006-01-02T15:04:05Z"),
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

// GetUser returns a user by UUID
func (s *AdminService) GetUser(ctx context.Context, uuid string) (*dto.UserDetailResponse, error) {
	user, err := s.userRepo.FindByUUIDWithRoles(ctx, uuid)
	if err != nil {
		return nil, errors.NewWithCode(errors.ErrAuthUserNotFound)
	}

	// Get session count
	sessionCount, _ := s.sessionRepo.CountByUserID(ctx, user.ID)

	return &dto.UserDetailResponse{
		UserResponse: dto.UserResponse{
			UUID:        user.UUID,
			Name:        user.Name,
			Email:       user.Email,
			Avatar:      user.Avatar,
			Bio:         user.Bio,
			Moemoepoint: user.Moemoepoint,
			Status:      user.Status,
			Roles:       user.RoleNames(),
			CreatedAt:   user.CreatedAt.Format("2006-01-02T15:04:05Z"),
		},
		IP:           user.IP,
		SessionCount: int(sessionCount),
	}, nil
}

// UpdateUser updates a user
func (s *AdminService) UpdateUser(ctx context.Context, uuid string, req *dto.UpdateUserRequest) (*model.User, error) {
	user, err := s.userRepo.FindByUUID(ctx, uuid)
	if err != nil {
		return nil, errors.NewWithCode(errors.ErrAuthUserNotFound)
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

	return user, nil
}

// BanUser bans a user
func (s *AdminService) BanUser(ctx context.Context, uuid string) error {
	user, err := s.userRepo.FindByUUID(ctx, uuid)
	if err != nil {
		return errors.NewWithCode(errors.ErrAuthUserNotFound)
	}

	user.Status = 1 // 1 = banned
	if err := s.userRepo.Update(ctx, user); err != nil {
		return err
	}

	// Delete all sessions for this user
	return s.sessionRepo.DeleteByUserID(ctx, user.ID)
}

// UnbanUser unbans a user
func (s *AdminService) UnbanUser(ctx context.Context, uuid string) error {
	user, err := s.userRepo.FindByUUID(ctx, uuid)
	if err != nil {
		return errors.NewWithCode(errors.ErrAuthUserNotFound)
	}

	user.Status = 0 // 0 = normal
	return s.userRepo.Update(ctx, user)
}

// DeleteUserSessions deletes all sessions for a user
func (s *AdminService) DeleteUserSessions(ctx context.Context, uuid string) error {
	user, err := s.userRepo.FindByUUID(ctx, uuid)
	if err != nil {
		return errors.NewWithCode(errors.ErrAuthUserNotFound)
	}

	return s.sessionRepo.DeleteByUserID(ctx, user.ID)
}
