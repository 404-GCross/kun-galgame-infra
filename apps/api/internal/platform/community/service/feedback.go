package service

import (
	"context"
	"time"

	"api/internal/platform/community/model"

	"gorm.io/gorm"
)

// FeedbackService owns the feedback (kind=2) status flow, duplicate merge, and
// accepted-answer marking (doc 11: Fider/Canny model).
type FeedbackService struct {
	db   *gorm.DB
	sink EventSink
}

func NewFeedbackService(db *gorm.DB, sink EventSink) *FeedbackService {
	return &FeedbackService{db: db, sink: sink}
}

// SetStatus sets a feedback thread's status and the official response. Only
// applies to kind=feedback threads. A map update (not a struct) so a zero
// fb_status (open) is written, not omitted.
func (s *FeedbackService) SetStatus(ctx context.Context, threadID int64, fbStatus int16, responderID int64, response *string) error {
	now := time.Now()
	updates := map[string]any{
		"fb_status":       fbStatus,
		"fb_responder_id": responderID,
		"fb_responded_at": now,
		"updated_at":      now,
	}
	if response != nil {
		updates["fb_response"] = *response
	}
	// Tenant guard folded into the WHERE (ruling 4): a feedback thread is always
	// site-local, so scoping the UPDATE by the caller's site costs no extra query
	// and makes a cross-tenant target land on 0 rows — the SAME ErrNotFeedback a
	// missing/wrong-kind id yields, so cross-tenant access is indistinguishable
	// from "does not exist" (no existence leak). An internal caller ("") skips it.
	res := feedbackScope(s.db.WithContext(ctx), callerSite(ctx), threadID).Updates(updates)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrNotFeedback
	}
	s.sink.Emit(Event{Kind: EventFeedbackStatusChanged, ThreadID: threadID, ActorID: responderID})
	return nil
}

// Merge redirects a duplicate feedback thread into another (reversible, Canny
// model): sets merged_into_id + closes it. Unmerge restores it.
func (s *FeedbackService) Merge(ctx context.Context, threadID, intoID int64) error {
	// Same site-scoped guard as SetStatus: a cross-tenant merge target lands on 0
	// rows → ErrNotFeedback, indistinguishable from a missing id.
	res := feedbackScope(s.db.WithContext(ctx), callerSite(ctx), threadID).
		Updates(map[string]any{
			"merged_into_id": intoID,
			"status":         model.ThreadStatusClosed,
			"updated_at":     time.Now(),
		})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrNotFeedback
	}
	return nil
}

// feedbackScope builds the base query that addresses ONE feedback thread,
// optionally narrowed to the caller's tenant. site == "" (a trusted internal
// caller) applies no tenant narrowing.
func feedbackScope(db *gorm.DB, site string, threadID int64) *gorm.DB {
	q := db.Model(&model.CommunityThread{}).
		Where("id = ? AND kind = ?", threadID, model.ThreadKindFeedback)
	if site != "" {
		q = q.Where("site = ?", site)
	}
	return q
}

// Unmerge reverses Merge: clears merged_into_id and reopens the thread.
func (s *FeedbackService) Unmerge(ctx context.Context, threadID int64) error {
	res := s.db.WithContext(ctx).Model(&model.CommunityThread{}).
		Where("id = ? AND kind = ?", threadID, model.ThreadKindFeedback).
		Updates(map[string]any{
			"merged_into_id": gorm.Expr("NULL"),
			"status":         model.ThreadStatusOpen,
			"updated_at":     time.Now(),
		})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrNotFeedback
	}
	return nil
}

// SetAnswer marks a post as the accepted answer of a feedback thread.
func (s *FeedbackService) SetAnswer(ctx context.Context, threadID, postID int64) error {
	res := s.db.WithContext(ctx).Model(&model.CommunityThread{}).
		Where("id = ? AND kind = ?", threadID, model.ThreadKindFeedback).
		Updates(map[string]any{"answer_post_id": postID, "updated_at": time.Now()})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrNotFeedback
	}
	return nil
}
