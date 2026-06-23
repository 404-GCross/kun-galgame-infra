package service

import (
	"context"
	"slices"
	"strings"
	"time"

	"api/internal/platform/auth/dto"
	"api/internal/platform/auth/model"
	"api/internal/platform/auth/repository"
	"api/pkg/errors"

	"gorm.io/datatypes"
	"gorm.io/gorm"
)

// CreatorReapplyCooldown is how long a user must wait after a DECLINE before
// re-applying. Approved users hold the role; pending users are blocked by the
// one-pending guard, so this only applies to declines.
const CreatorReapplyCooldown = 24 * time.Hour

// CreatorRoleName is the role granted on approval.
const CreatorRoleName = "creator"

// CreatorApplicationService is the central creator-application queue + review.
// Eligibility is gated downstream (forum/moyu); this owns the queue, the admin
// decision, and the role grant. See docs/auth/01-creator-role-design.md.
type CreatorApplicationService struct {
	repo         *repository.CreatorApplicationRepository
	userRepo     *repository.UserRepository
	userBatchSvc *UserBatchService
}

func NewCreatorApplicationService(repo *repository.CreatorApplicationRepository, userRepo *repository.UserRepository, userBatchSvc *UserBatchService) *CreatorApplicationService {
	return &CreatorApplicationService{repo: repo, userRepo: userRepo, userBatchSvc: userBatchSvc}
}

// Apply files a pending application for userID. Guards: not already a creator,
// no existing pending application, and past the re-apply cooldown after any
// decline. Eligibility itself is the downstream's responsibility — `evidence`
// is the downstream-provided hint shown to the reviewing admin.
func (s *CreatorApplicationService) Apply(ctx context.Context, userID uint, source, message string, evidence datatypes.JSON) (*model.CreatorApplication, error) {
	user, err := s.userRepo.FindByIDWithRoles(ctx, userID)
	if err != nil {
		return nil, errors.NewWithCode(errors.ErrAuthUserNotFound)
	}
	if slices.Contains(user.RoleNames(), CreatorRoleName) {
		return nil, errors.NewWithCode(errors.ErrCreatorAlreadyHas)
	}

	latest, err := s.repo.FindLatestByUser(ctx, userID)
	switch {
	case err == nil:
		switch latest.Status {
		case model.CreatorAppPending:
			return nil, errors.NewWithCode(errors.ErrCreatorAppPending)
		case model.CreatorAppDeclined:
			if latest.ReviewedAt != nil && time.Since(*latest.ReviewedAt) < CreatorReapplyCooldown {
				return nil, errors.NewWithCode(errors.ErrCreatorAppCooldown)
			}
		}
	case err == gorm.ErrRecordNotFound:
		// first-ever application — fine
	default:
		return nil, err
	}

	app := &model.CreatorApplication{
		UserID:   userID,
		Source:   source,
		Status:   model.CreatorAppPending,
		Message:  message,
		Evidence: evidence,
	}
	if err := s.repo.Create(ctx, app); err != nil {
		// Lost a race against a concurrent apply: the partial unique index
		// uq_creator_app_pending fired (the service-level guard above and the
		// index aren't atomic). Surface the clean "pending" error, not a 500.
		if strings.Contains(err.Error(), "uq_creator_app_pending") {
			return nil, errors.NewWithCode(errors.ErrCreatorAppPending)
		}
		return nil, err
	}
	return app, nil
}

// MyApplication returns the user's latest application, or nil if they never applied.
func (s *CreatorApplicationService) MyApplication(ctx context.Context, userID uint) (*model.CreatorApplication, error) {
	app, err := s.repo.FindLatestByUser(ctx, userID)
	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	return app, err
}

// AdminApplicationItem is one row of the admin review queue: the application
// plus its applicant's brief, resolved server-side so the admin UI renders
// names/avatars in a single round-trip. This is the clean alternative to the
// browser calling /users/batch — that endpoint is S2S-only (client Basic auth)
// and must not be hit from the frontend (it returns 401 with a user JWT).
type AdminApplicationItem struct {
	*model.CreatorApplication
	User *dto.UserBrief `json:"user"`
}

// List returns a page of applications for the admin queue, each enriched with
// its applicant brief (one batched user lookup for the whole page).
func (s *CreatorApplicationService) List(ctx context.Context, status string, page, limit int) ([]AdminApplicationItem, int64, error) {
	if page <= 0 {
		page = 1
	}
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	apps, total, err := s.repo.ListByStatus(ctx, status, page, limit)
	if err != nil {
		return nil, 0, err
	}

	ids := make([]uint, 0, len(apps))
	for i := range apps {
		ids = append(ids, apps[i].UserID)
	}
	briefByID := make(map[uint]*dto.UserBrief, len(ids))
	if len(ids) > 0 {
		res, err := s.userBatchSvc.GetBriefs(ctx, ids)
		if err != nil {
			return nil, 0, err
		}
		for i := range res.Users {
			briefByID[res.Users[i].ID] = &res.Users[i]
		}
	}

	items := make([]AdminApplicationItem, len(apps))
	for i := range apps {
		items[i] = AdminApplicationItem{
			CreatorApplication: &apps[i],
			User:               briefByID[apps[i].UserID],
		}
	}
	return items, total, nil
}

// Approve grants the creator role and marks the application approved. The role
// grant is idempotent; the status transition is atomic (a concurrent double
// review yields ErrCreatorAppNotPending).
func (s *CreatorApplicationService) Approve(ctx context.Context, id, reviewerID uint) error {
	app, err := s.repo.FindByID(ctx, id)
	if err != nil {
		return errors.NewWithCode(errors.ErrCreatorAppNotFound)
	}
	if app.Status != model.CreatorAppPending {
		return errors.NewWithCode(errors.ErrCreatorAppNotPending)
	}
	if err := s.userRepo.AddRole(ctx, app.UserID, CreatorRoleName); err != nil {
		return err
	}
	n, err := s.repo.Review(ctx, id, model.CreatorAppApproved, reviewerID, "")
	if err != nil {
		return err
	}
	if n == 0 {
		return errors.NewWithCode(errors.ErrCreatorAppNotPending)
	}
	return nil
}

// Decline marks the application declined with a reason. Atomic on the transition.
func (s *CreatorApplicationService) Decline(ctx context.Context, id, reviewerID uint, reason string) error {
	app, err := s.repo.FindByID(ctx, id)
	if err != nil {
		return errors.NewWithCode(errors.ErrCreatorAppNotFound)
	}
	if app.Status != model.CreatorAppPending {
		return errors.NewWithCode(errors.ErrCreatorAppNotPending)
	}
	n, err := s.repo.Review(ctx, id, model.CreatorAppDeclined, reviewerID, reason)
	if err != nil {
		return err
	}
	if n == 0 {
		return errors.NewWithCode(errors.ErrCreatorAppNotPending)
	}
	return nil
}
