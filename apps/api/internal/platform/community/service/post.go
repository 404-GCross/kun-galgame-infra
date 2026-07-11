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
	// The BFF passes only reply_to_post_id; the primitive completes the two blood
	// pointers from the parent row — root_post_id (the sub-thread's top ancestor)
	// and target_user_id (the parent's author) — so nested replies render indented
	// with a "▸ recipient" label and fire a reply-to-you event (docs/proj/16 #8).
	// An explicit caller value always wins.
	rootPostID, targetUserID := p.RootPostID, p.TargetUserID
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
		// Complete the reply's root/target from its parent post (same thread only).
		if p.ReplyToPostID != nil && (rootPostID == nil || targetUserID == nil) {
			parent, perr := repository.GetPostTx(tx, *p.ReplyToPostID)
			if perr != nil {
				return perr
			}
			if parent != nil && parent.ThreadID == p.ThreadID {
				if targetUserID == nil {
					author := parent.AuthorID
					targetUserID = &author
				}
				if rootPostID == nil {
					if parent.RootPostID != nil {
						rootPostID = parent.RootPostID // inherit the parent's sub-thread root
					} else {
						top := parent.ID // parent is top-level → it is the root
						rootPostID = &top
					}
				}
			}
		}
		post = model.CommunityPost{
			ThreadID: p.ThreadID, PostNumber: number,
			RootPostID: rootPostID, ReplyToPostID: p.ReplyToPostID, TargetUserID: targetUserID,
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
	if targetUserID != nil && *targetUserID != p.AuthorID {
		s.sink.Emit(Event{Kind: EventReplyToYou, ThreadID: p.ThreadID, PostID: post.ID, ActorID: p.AuthorID, TargetID: *targetUserID})
	}
	return &post, nil
}

// ListPosts returns a thread's posts after a post_number, ascending (keyset).
func (s *PostService) ListPosts(threadID int64, afterNumber int32, limit int) ([]model.CommunityPost, error) {
	return s.posts.ListByThread(threadID, afterNumber, clampLimit(limit))
}

// EditParams describes an author editing their own post's body.
type EditParams struct {
	PostID   int64
	AuthorID int64 // the acting user; must equal the post's author
	BodyRaw  string
}

// Edit rewrites an author's own post (invariant 6: re-cook raw→cooked at the
// current sanitizer version and stamp edited_at). Author-only — author_id must
// match the post's author — and only a VISIBLE post is editable: a held/hidden
// or tombstoned post is not an editable surface. The TL0 sandbox per-post
// content caps (links/images/@mentions) apply to the edited body too, so editing
// is not an escape hatch out of the newcomer sandbox. The daily create-rate caps
// do NOT apply — an edit is not a new post — so the sandbox day-window count is
// left untouched.
func (s *PostService) Edit(ctx context.Context, p EditParams) (*model.CommunityPost, error) {
	cooked := sanitize.Cook(p.BodyRaw)
	level, err := trustLevel(s.trusts, p.AuthorID)
	if err != nil {
		return nil, err
	}
	if err := checkContentSandbox(level, cooked); err != nil {
		return nil, err
	}

	now := time.Now()
	var post model.CommunityPost
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		existing, err := repository.GetPostTx(tx, p.PostID)
		if err != nil {
			return err
		}
		if existing == nil {
			return ErrPostNotFound
		}
		if existing.AuthorID != p.AuthorID {
			return ErrNotAuthor
		}
		if existing.Status != model.PostStatusVisible {
			return ErrPostNotEditable
		}
		if err := repository.UpdatePostContentTx(tx, p.PostID, p.BodyRaw, cooked.HTML, int32(cooked.Version), now); err != nil {
			return err
		}
		// Reflect the write in the returned view (post_number/status/author are
		// unchanged; the DB round-trip is unnecessary).
		existing.ContentRaw = p.BodyRaw
		existing.ContentHTML = cooked.HTML
		existing.SanitizerVersion = int32(cooked.Version)
		existing.EditedAt = &now
		post = *existing
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &post, nil
}

// Delete tombstones an author's own post (self-delete): status → deleted while
// the post_number is PRESERVED, so the thread numbering never collapses
// (invariant 13). This is the SAME terminal state a moderator reject produces
// (ReviewService.Reject) — the only difference is the actor (the author here vs a
// moderator there); both leave a numbered placeholder instead of removing the
// row, and the two paths coexist (a post already tombstoned by one is an
// idempotent no-op for the other). posts_count is NOT decremented: the tombstone
// still occupies its post_number, so the counter (which tracks numbers allocated,
// not live posts) stays consistent with highest_post_number — matching the
// mod-reject path, which likewise leaves the count untouched.
func (s *PostService) Delete(ctx context.Context, postID, authorID int64) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		existing, err := repository.GetPostTx(tx, postID)
		if err != nil {
			return err
		}
		if existing == nil {
			return ErrPostNotFound
		}
		if existing.AuthorID != authorID {
			return ErrNotAuthor
		}
		if existing.Status == model.PostStatusDeleted {
			return nil // already tombstoned → idempotent no-op
		}
		return repository.SetPostStatusTx(tx, postID, model.PostStatusDeleted)
	})
}
