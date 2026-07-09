package service

import (
	"context"
	"time"

	"api/internal/platform/community/model"
	"api/internal/platform/community/repository"
	"api/internal/platform/community/sanitize"

	"gorm.io/gorm"
)

// PostService owns replying (subsequent posts) and post reads.
type PostService struct {
	db     *gorm.DB
	posts  *repository.PostRepository
	trusts *repository.TrustRepository
	sink   EventSink
}

func NewPostService(db *gorm.DB, sink EventSink) *PostService {
	return &PostService{
		db:     db,
		posts:  repository.NewPostRepository(db),
		trusts: repository.NewTrustRepository(db),
		sink:   sink,
	}
}

// ReplyParams describes a reply to an existing thread.
type ReplyParams struct {
	ThreadID      int64
	AuthorID      int64
	BodyRaw       string
	RootPostID    *int64 // sub-thread grouping (kungal render); nil = top level
	ReplyToPostID *int64
	TargetUserID  *int64 // recipient of a directed reply → reply.to_you event
}

// Reply appends a post to a thread: TL0 sandbox check → cook → allocate the
// next post_number → insert → maintain the thread's denormalized counters, all
// in one transaction (doc 11 §4.3). A newcomer with holds remaining has the
// post held (status=hidden) and one hold spent (the review queue that surfaces
// held posts is step 04).
func (s *PostService) Reply(ctx context.Context, p ReplyParams) (*model.CommunityPost, error) {
	cooked := sanitize.Cook(p.BodyRaw)
	level, err := trustLevel(s.trusts, p.AuthorID)
	if err != nil {
		return nil, err
	}
	if err := checkContentSandbox(level, cooked); err != nil {
		return nil, err
	}
	if isSandboxed(level) {
		n, err := s.posts.CountByAuthorSince(p.AuthorID, time.Now().Add(-sandboxWindow))
		if err != nil {
			return nil, err
		}
		if n >= tl0MaxRepliesPerDay {
			return nil, &SandboxError{Reason: "daily reply limit"}
		}
	}

	now := time.Now()
	var post model.CommunityPost
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		thread, err := repository.GetThreadTx(tx, p.ThreadID)
		if err != nil {
			return err
		}
		if thread == nil {
			return ErrThreadNotFound
		}
		if thread.Status != model.ThreadStatusOpen {
			return ErrThreadNotOpen
		}
		trust, err := repository.GetOrCreateTrustTx(tx, p.AuthorID)
		if err != nil {
			return err
		}
		held := trust.FirstPostsHeldRemaining > 0

		posted, err := repository.AuthorHasPostedTx(tx, p.ThreadID, p.AuthorID)
		if err != nil {
			return err
		}
		// Atomically allocate the next post_number (row-locked) BEFORE inserting
		// the post, so concurrent replies never collide on the number.
		number, err := repository.AllocateReplyTx(tx, p.ThreadID, now, !posted)
		if err != nil {
			return err
		}
		post = model.CommunityPost{
			ThreadID: p.ThreadID, PostNumber: number,
			RootPostID: p.RootPostID, ReplyToPostID: p.ReplyToPostID, TargetUserID: p.TargetUserID,
			AuthorID:   p.AuthorID,
			ContentRaw: p.BodyRaw, ContentHTML: cooked.HTML, SanitizerVersion: int32(cooked.Version),
			// A reply inherits the thread's content rating by default (invariant
			// 12: default from the anchor/thread). A per-reply override is a
			// Phase-B API concern.
			ContentRating: thread.ContentRating,
			Status:        postStatus(held),
		}
		if err := repository.CreatePostTx(tx, &post); err != nil {
			return err
		}
		if held {
			// A held newcomer post enters the review queue (doc 11 §6 layer 5);
			// releasing it is a queue approve (step 03's ReviewService).
			if _, err := repository.EnqueueReviewIfAbsentTx(tx, thread.Site, post.ID, model.ReviewSourceFirstPostHold); err != nil {
				return err
			}
			return repository.DecrementHoldTx(tx, p.AuthorID)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	s.sink.Emit(Event{Kind: EventPostCreated, ThreadID: p.ThreadID, PostID: post.ID, ActorID: p.AuthorID})
	if p.TargetUserID != nil && *p.TargetUserID != p.AuthorID {
		s.sink.Emit(Event{Kind: EventReplyToYou, ThreadID: p.ThreadID, PostID: post.ID, ActorID: p.AuthorID, TargetID: *p.TargetUserID})
	}
	return &post, nil
}

// ListPosts returns a thread's posts after a post_number, ascending (keyset).
func (s *PostService) ListPosts(threadID int64, afterNumber int32, limit int) ([]model.CommunityPost, error) {
	return s.posts.ListByThread(threadID, afterNumber, clampLimit(limit))
}
