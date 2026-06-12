package service

import (
	"context"

	"api/internal/platform/auth/dto"
	"api/internal/platform/auth/model"
	"api/internal/platform/auth/repository"
)

// UserBatchService backs the `GET /api/v1/users/batch` endpoint used by
// kungal / moyu / galgame_wiki to fetch a batch of user briefs in one
// round-trip (vs N RPCs for N users).
type UserBatchService struct {
	userRepo *repository.UserRepository
}

func NewUserBatchService(userRepo *repository.UserRepository) *UserBatchService {
	return &UserBatchService{userRepo: userRepo}
}

// GetBriefs returns a UserBrief for each found ID; not-found IDs are listed
// in the second return for the caller to surface to consumers.
func (s *UserBatchService) GetBriefs(ctx context.Context, ids []uint) (*dto.BatchGetUsersResponse, error) {
	users, err := s.userRepo.FindByIDsWithRoles(ctx, ids)
	if err != nil {
		return nil, err
	}

	found := make(map[uint]struct{}, len(users))
	briefs := make([]dto.UserBrief, 0, len(users))
	for _, u := range users {
		found[u.ID] = struct{}{}
		briefs = append(briefs, toBrief(&u))
	}

	notFound := make([]uint, 0)
	for _, id := range ids {
		if _, ok := found[id]; !ok {
			notFound = append(notFound, id)
		}
	}

	return &dto.BatchGetUsersResponse{
		Users:    briefs,
		NotFound: notFound,
	}, nil
}

// SearchByName returns users whose name matches `query` (case-insensitive
// substring). Ranking — exact > prefix > substring, alphabetical within —
// is done by the repository's CASE-based ORDER BY, so this layer is just a
// projection.
func (s *UserBatchService) SearchByName(ctx context.Context, query string, limit int) (*dto.SearchUsersResponse, error) {
	users, err := s.userRepo.SearchByName(ctx, query, limit)
	if err != nil {
		return nil, err
	}
	briefs := make([]dto.UserBrief, 0, len(users))
	for i := range users {
		briefs = append(briefs, toBrief(&users[i]))
	}
	return &dto.SearchUsersResponse{Users: briefs}, nil
}

// toBrief projects model.User → dto.UserBrief. Strips email + internal fields.
func toBrief(u *model.User) dto.UserBrief {
	roles := make([]string, 0, len(u.Roles))
	for _, r := range u.Roles {
		roles = append(roles, r.Name)
	}
	return dto.UserBrief{
		ID:              u.ID,
		UUID:            u.UUID,
		Name:            u.Name,
		Avatar:          u.Avatar,
		AvatarImageHash: u.AvatarImageHash,
		Bio:             u.Bio,
		Status:          u.Status,
		Roles:           roles,
		CreatedAt:       u.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"),
	}
}
