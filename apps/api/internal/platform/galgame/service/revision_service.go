package service

import (
	"context"

	"api/internal/platform/galgame/dto"
)

// The galgame revision/PR read+write surface retired with apps/wiki at E3b —
// the old-wire read adapters are gone and every user edit now flows through the
// editing engine (kungal's edit BFF → catalog edit face). The one native
// engine-log read that survives on the galgame surface is the merged-revision
// S2S feed below; the profile edit counters live in galgame_service.GetUserStats
// (also via editquery). galgame_revision / galgame_pr stay frozen until drop.

// ListRecentRevisions returns the merged-revision feed (edit-landed events) for
// downstream activity timelines. Mirrors MessageService.ListFeed's cursor shape
// (since_id + has_more). The cursor rides COALESCE(legacy_id, id), so consumer
// cursors saved against the old table survive the migration (the transform
// bumped the identity sequence above every legacy id).
func (s *GalgameService) ListRecentRevisions(ctx context.Context, sinceID int64, limit int) (*dto.RevisionFeedResponse, error) {
	if limit <= 0 {
		limit = 1000
	}
	if limit > 5000 {
		limit = 5000
	}
	items, err := s.editq.Feed(ctx, sinceID, limit)
	if err != nil {
		return nil, err
	}
	out := make([]dto.RevisionFeedItem, len(items))
	for i, rev := range items {
		out[i] = dto.RevisionFeedItem{
			ID:        rev.ID,
			GalgameID: rev.GalgameID,
			Revision:  rev.Revision,
			UserID:    rev.UserID,
			Action:    rev.Action,
			Created:   rev.Created.UTC().Format("2006-01-02T15:04:05Z"),
		}
	}
	return &dto.RevisionFeedResponse{
		Items:   out,
		HasMore: len(items) == limit,
	}, nil
}
