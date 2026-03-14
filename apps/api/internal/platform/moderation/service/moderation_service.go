package service

import (
	"context"

	"api/internal/platform/moderation/model"
	"api/internal/platform/moderation/providers"
	"api/internal/platform/moderation/repository"
	"api/pkg/utils"
)

// ModerationService handles moderation business logic
type ModerationService struct {
	moderationRepo *repository.ModerationRepository
	providers      []providers.Provider
}

// NewModerationService creates a new ModerationService
func NewModerationService(
	moderationRepo *repository.ModerationRepository,
	providerList []providers.Provider,
) *ModerationService {
	return &ModerationService{
		moderationRepo: moderationRepo,
		providers:      providerList,
	}
}

// CreateJob creates a new moderation job
func (s *ModerationService) CreateJob(ctx context.Context, job *model.Job) error {
	return s.moderationRepo.CreateJob(ctx, job)
}

// GetJob gets a job by ID
func (s *ModerationService) GetJob(ctx context.Context, id uint) (*model.Job, error) {
	return s.moderationRepo.FindJobByID(ctx, id)
}

// ListJobs lists jobs with pagination
func (s *ModerationService) ListJobs(ctx context.Context, p utils.Pagination) (utils.PaginatedResult[model.Job], error) {
	p.Normalize()
	jobs, total, err := s.moderationRepo.ListJobs(ctx, p.Offset(), p.Limit())
	if err != nil {
		return utils.PaginatedResult[model.Job]{}, err
	}
	return utils.NewPaginatedResult(jobs, total, p.Page, p.PageSize), nil
}

// ProcessJob processes a moderation job
func (s *ModerationService) ProcessJob(ctx context.Context, job *model.Job) error {
	// Update status to processing
	job.Status = "processing"
	if err := s.moderationRepo.UpdateJob(ctx, job); err != nil {
		return err
	}

	// Try each provider
	for _, provider := range s.providers {
		req := providers.ModerationRequest{
			ContentType:   providers.ContentType(job.ContentType),
			Text:          "", // TODO: fetch actual content
			PolicyVersion: job.PolicyVersion,
		}

		resp, err := provider.Moderate(ctx, req)
		if err != nil {
			continue
		}

		// Save result
		result := &model.Result{
			JobID:      job.ID,
			Provider:   provider.Name(),
			Decision:   model.Decision(resp.Decision),
			Confidence: resp.Confidence,
		}
		if err := s.moderationRepo.CreateResult(ctx, result); err != nil {
			return err
		}

		// Update job
		job.Status = "completed"
		job.Decision = model.Decision(resp.Decision)
		job.Confidence = resp.Confidence
		job.Reason = resp.Reason
		return s.moderationRepo.UpdateJob(ctx, job)
	}

	// All providers failed
	job.Status = "failed"
	return s.moderationRepo.UpdateJob(ctx, job)
}

// ManualReview performs manual review on a job
func (s *ModerationService) ManualReview(ctx context.Context, jobID uint, decision model.Decision, reviewerUUID string) error {
	job, err := s.moderationRepo.FindJobByID(ctx, jobID)
	if err != nil {
		return err
	}

	job.Status = "completed"
	job.Decision = decision
	job.ReviewedBy = &reviewerUUID

	return s.moderationRepo.UpdateJob(ctx, job)
}
