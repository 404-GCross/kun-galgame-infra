package service

import (
	"context"
	"encoding/json"

	"api/internal/platform/galgame/dto"
	"api/internal/platform/galgame/model"
	"api/internal/platform/galgame/repository"
)

// MessageService converts raw galgame_message rows to wire-format
// MessageResponse, enriching them with the linked galgame brief (always)
// and the actor user brief (admin queue only).
type MessageService struct {
	messageRepo *repository.MessageRepository
	galgameRepo *repository.GalgameRepository
	userRepo    *repository.UserReadonlyRepository
}

// NewMessageService creates a MessageService.
func NewMessageService(
	m *repository.MessageRepository,
	g *repository.GalgameRepository,
	u *repository.UserReadonlyRepository,
) *MessageService {
	return &MessageService{messageRepo: m, galgameRepo: g, userRepo: u}
}

// ListMine returns messages targeting the given user, newest first.
func (s *MessageService) ListMine(ctx context.Context, userID int, sinceID int64, limit int) ([]dto.MessageResponse, int64, error) {
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}
	items, total, err := s.messageRepo.ListMine(ctx, userID, sinceID, limit)
	if err != nil {
		return nil, 0, err
	}
	resp, err := s.enrich(ctx, items, false)
	return resp, total, err
}

// ListFeed returns user-targeted messages id-ascending for cron consumers.
// has_more is true when the result hits the limit (potential next page).
func (s *MessageService) ListFeed(ctx context.Context, sinceID int64, limit int) (*dto.MessageFeedResponse, error) {
	if limit <= 0 {
		limit = 1000
	}
	if limit > 5000 {
		limit = 5000
	}
	items, err := s.messageRepo.ListFeed(ctx, sinceID, limit)
	if err != nil {
		return nil, err
	}
	resp, err := s.enrich(ctx, items, false)
	if err != nil {
		return nil, err
	}
	return &dto.MessageFeedResponse{
		Items:   resp,
		HasMore: len(items) == limit,
	}, nil
}

// ListAdminQueue returns un-handled submission/edit messages for the admin UI.
// Includes actor user brief for display.
func (s *MessageService) ListAdminQueue(ctx context.Context, types []string, page, limit int) ([]dto.MessageResponse, int64, error) {
	if page < 1 {
		page = 1
	}
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}
	if len(types) == 0 {
		types = []string{model.MessageTypeSubmitted, model.MessageTypeEditedPending}
	}
	items, total, err := s.messageRepo.ListAdminQueue(ctx, types, page, limit)
	if err != nil {
		return nil, 0, err
	}
	resp, err := s.enrich(ctx, items, true)
	return resp, total, err
}

// enrich joins galgame brief and (optionally) actor brief onto raw messages.
//
// includeActor: admin queue needs to render "who submitted"; user notifications
// don't (actor is admin from the user's perspective, just shows up as "system").
func (s *MessageService) enrich(ctx context.Context, items []model.GalgameMessage, includeActor bool) ([]dto.MessageResponse, error) {
	if len(items) == 0 {
		return []dto.MessageResponse{}, nil
	}

	// Collect IDs for batch lookups
	galgameIDSet := make(map[int]struct{}, len(items))
	userIDSet := make(map[int]struct{})
	for _, m := range items {
		galgameIDSet[m.GalgameID] = struct{}{}
		if includeActor && m.ActorUserID != nil {
			userIDSet[*m.ActorUserID] = struct{}{}
		}
	}

	galgameIDs := make([]int, 0, len(galgameIDSet))
	for id := range galgameIDSet {
		galgameIDs = append(galgameIDs, id)
	}
	// Lookup w/ admin viewer (userID=0 path) — admin queue cares about status=3,
	// /mine and /feed return status=0 entries plus the user's own. Since the
	// message has a target_user_id we trust, we just take the current state
	// of the galgame regardless of status (admin or owner is the audience).
	galgames, err := s.galgameRepo.FindByIDsAny(ctx, galgameIDs)
	if err != nil {
		return nil, err
	}
	// FindByIDsAny only selects scalar columns (no Cover preload), so
	// EffectiveBannerHash would always be nil and the queue thumbnail blank
	// for user-submitted galgames (whose banner lives only in galgame_cover,
	// not the legacy banner URL). One batch lookup of pinned covers fills it
	// — mirrors BatchGetWithViewer. Non-fatal on error: fall back to nil.
	pinned, err := s.galgameRepo.PinnedCoverHashes(ctx, galgameIDs)
	if err != nil {
		pinned = map[int]string{}
	}
	galgameBrief := make(map[int]*dto.MessageGalgameBrief, len(galgames))
	for i := range galgames {
		g := galgames[i]
		var effective *string
		if h, ok := pinned[g.ID]; ok {
			effective = &h
		}
		galgameBrief[g.ID] = &dto.MessageGalgameBrief{
			ID:                  g.ID,
			NameEnUS:            g.NameEnUS,
			NameJaJP:            g.NameJaJP,
			NameZhCN:            g.NameZhCN,
			NameZhTW:            g.NameZhTW,
			Banner:              g.Banner,
			EffectiveBannerHash: effective,
			Status:              g.Status,
			UserID:              g.UserID,
		}
	}

	var userBrief map[int]*dto.UserBrief
	if includeActor && len(userIDSet) > 0 {
		uids := make([]int, 0, len(userIDSet))
		for id := range userIDSet {
			uids = append(uids, id)
		}
		userBrief, _ = s.userRepo.FindByIDs(ctx, uids)
	}

	out := make([]dto.MessageResponse, 0, len(items))
	for _, m := range items {
		// Parse jsonb payload back into a generic any value; on error pass
		// through nil rather than failing the whole response.
		var payload any
		if len(m.Payload) > 0 {
			_ = json.Unmarshal(m.Payload, &payload)
		}

		row := dto.MessageResponse{
			ID:           m.ID,
			Type:         m.Type,
			GalgameID:    m.GalgameID,
			Galgame:      galgameBrief[m.GalgameID], // may be nil if galgame deleted
			ActorUserID:  m.ActorUserID,
			TargetUserID: m.TargetUserID,
			Payload:      payload,
			CreatedAt:    m.CreatedAt,
		}
		if includeActor && m.ActorUserID != nil && userBrief != nil {
			row.Actor = userBrief[*m.ActorUserID]
		}
		out = append(out, row)
	}
	return out, nil
}
