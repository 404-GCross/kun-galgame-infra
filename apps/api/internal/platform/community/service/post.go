package service

import (
	"context"
	"log/slog"
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
	check  *CheckService // nil = the pre-write word-list gate is off (default)
}

// PostOption configures optional PostService collaborators (step 06: the
// synchronous pre-write word-list gate). Existing callers pass none — the gate
// is off, so behaviour is unchanged (default-safe).
type PostOption func(*PostService)

// WithPostChecker installs the synchronous Tier0 word-list gate.
func WithPostChecker(c *CheckService) PostOption {
	return func(s *PostService) { s.check = c }
}

func NewPostService(db *gorm.DB, sink EventSink, opts ...PostOption) *PostService {
	s := &PostService{
		db:     db,
		posts:  repository.NewPostRepository(db),
		trusts: repository.NewTrustRepository(db),
		sink:   sink,
	}
	for _, o := range opts {
		o(s)
	}
	return s
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

	// Synchronous Tier0 word-list gate (step 06), strictly BEFORE the tx — the
	// HTTP check never runs inside a transaction (charter community ruling). deny
	// blocks the write outright; hold publishes normally but enqueues a review
	// item inside the tx below. A disabled gate / any checker failure is allow.
	suspectHold := false
	switch s.check.Decision(ctx, callerSite(ctx), p.BodyRaw, &p.AuthorID) {
	case checkDeny:
		return nil, ErrContentBlocked
	case checkHold:
		suspectHold = true
	}

	now := time.Now()
	var post model.CommunityPost
	var enqueuedItemID int64
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
		// Tenant guard on the already-loaded thread (ruling 4): a cross-tenant
		// caller is answered as "not found", indistinguishable from a bad id, and
		// BEFORE the open-status check so it cannot probe the thread's state.
		if crossTenantCtx(ctx, thread.Site, thread.AnchorKind) {
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
			itemID, created, err := repository.EnqueueReviewIfAbsentTx(tx, thread.Site, post.ID, model.ReviewSourceFirstPostHold)
			if err != nil {
				return err
			}
			if created {
				enqueuedItemID = itemID
			}
			return repository.DecrementHoldTx(tx, p.AuthorID)
		}
		if suspectHold {
			// hold (suspect-only): the post stays visible but enters the review
			// queue (source=suspect_words) — "enqueue, don't block" (doc 18). NOT
			// the TL0 held/hidden status. IfAbsent converges with a concurrent
			// first-post hold to a single item.
			itemID, created, err := repository.EnqueueReviewIfAbsentTx(tx, thread.Site, post.ID, model.ReviewSourceSuspectWords)
			if err != nil {
				return err
			}
			if created {
				enqueuedItemID = itemID
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	s.sink.Emit(Event{Kind: EventPostCreated, ThreadID: p.ThreadID, PostID: post.ID, ActorID: p.AuthorID})
	// Best-effort forward a held-post review item to the trust inbox after commit
	// (step 03 outbox).
	if enqueuedItemID != 0 {
		s.sink.Emit(Event{Kind: EventReviewEnqueued, ThreadID: p.ThreadID, PostID: post.ID, ReviewItemID: enqueuedItemID})
	}
	if targetUserID != nil && *targetUserID != p.AuthorID {
		s.sink.Emit(Event{Kind: EventReplyToYou, ThreadID: p.ThreadID, PostID: post.ID, ActorID: p.AuthorID, TargetID: *targetUserID})
	}
	return &post, nil
}

// ListPosts returns a thread's posts after a post_number, ascending (keyset).
func (s *PostService) ListPosts(threadID int64, afterNumber int32, limit int) ([]model.CommunityPost, error) {
	return s.posts.ListByThread(threadID, afterNumber, clampLimit(limit))
}

// EditParams describes an actor editing a post's body.
type EditParams struct {
	PostID   int64
	AuthorID int64 // the acting user; must equal the post's author unless AsModerator
	BodyRaw  string
	// AsModerator is the mod-actor variant (docs/proj/17 decision 3): the calling
	// site declares that AuthorID is one of ITS moderators (the site holds the role
	// tables; community trusts the assertion like every other BFF-supplied identity,
	// doc 01 §2). The author-match check is skipped and the action is audit-logged.
	AsModerator bool
}

// Edit rewrites a post (invariant 6: re-cook raw→cooked at the current sanitizer
// version and stamp edited_at). Author-only — author_id must match the post's
// author — unless the caller declares the mod-actor variant (AsModerator), which
// edits any post and leaves a structured audit log (the minimal audit surface —
// no table, matching the review queue's decided_by precedent). Only a VISIBLE
// post is editable either way: a held/hidden or tombstoned post is not an
// editable surface (a held post is released via the review queue, not an edit).
// The TL0 sandbox per-post content caps (links/images/@mentions) apply to the
// edited body too, so editing is not an escape hatch out of the newcomer
// sandbox. The daily create-rate caps do NOT apply — an edit is not a new post —
// so the sandbox day-window count is left untouched.
func (s *PostService) Edit(ctx context.Context, p EditParams) (*model.CommunityPost, error) {
	cooked := sanitize.Cook(p.BodyRaw)
	level, err := trustLevel(s.trusts, p.AuthorID)
	if err != nil {
		return nil, err
	}
	if err := checkContentSandbox(level, cooked); err != nil {
		return nil, err
	}

	// Synchronous Tier0 word-list gate (step 06), strictly BEFORE the tx. deny
	// blocks the edit (nothing persisted); hold lets the edit through but enqueues
	// a suspect review item inside the tx (IfAbsent → editing twice never
	// duplicates). Fail-open on any checker failure.
	suspectHold := false
	switch s.check.Decision(ctx, callerSite(ctx), p.BodyRaw, &p.AuthorID) {
	case checkDeny:
		return nil, ErrContentBlocked
	case checkHold:
		suspectHold = true
	}

	now := time.Now()
	var post model.CommunityPost
	var enqueuedItemID int64
	modActed := false
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		existing, err := repository.GetPostTx(tx, p.PostID)
		if err != nil {
			return err
		}
		if existing == nil {
			return ErrPostNotFound
		}
		// Tenant guard: a post is addressed by its global id, so confirm its thread
		// belongs to the caller before any author/state check leaks. Edit loads
		// only the post, so this is the sanctioned single join to the thread; a
		// cross-tenant hit is answered as "post not found".
		thread, err := repository.GetThreadTx(tx, existing.ThreadID)
		if err != nil {
			return err
		}
		if thread == nil || crossTenantCtx(ctx, thread.Site, thread.AnchorKind) {
			return ErrPostNotFound
		}
		if existing.AuthorID != p.AuthorID {
			if !p.AsModerator {
				return ErrNotAuthor
			}
			modActed = true
		}
		if existing.Status != model.PostStatusVisible {
			return ErrPostNotEditable
		}
		// The edited_by_moderator bookkeeping bit tracks the LATEST edit's actor:
		// a cross-author mod edit sets it, an author self-edit (including a mod
		// editing their own post) clears it back to false.
		if err := repository.UpdatePostContentTx(tx, p.PostID, p.BodyRaw, cooked.HTML, int32(cooked.Version), now, modActed); err != nil {
			return err
		}
		// Reflect the write in the returned view (post_number/status/author are
		// unchanged; the DB round-trip is unnecessary).
		existing.ContentRaw = p.BodyRaw
		existing.ContentHTML = cooked.HTML
		existing.SanitizerVersion = int32(cooked.Version)
		existing.EditedAt = &now
		existing.EditedByModerator = modActed
		if suspectHold {
			// hold (suspect-only) on an edit: enqueue on the thread's tenant site;
			// the post stays visible. IfAbsent is idempotent — a second edit that
			// still trips suspect never adds a duplicate item.
			itemID, created, eqErr := repository.EnqueueReviewIfAbsentTx(tx, thread.Site, existing.ID, model.ReviewSourceSuspectWords)
			if eqErr != nil {
				return eqErr
			}
			if created {
				enqueuedItemID = itemID
			}
		}
		post = *existing
		return nil
	})
	if err != nil {
		return nil, err
	}
	if modActed {
		slog.Info("community mod edit", "post_id", post.ID, "thread_id", post.ThreadID,
			"post_author_id", post.AuthorID, "moderator_id", p.AuthorID)
	}
	// A successful edit re-emits the content event so the step-04 scanning sink
	// re-scans the new raw body (a no-op under NoopSink / the forwarding sink).
	s.sink.Emit(Event{Kind: EventPostEdited, ThreadID: post.ThreadID, PostID: post.ID, ActorID: p.AuthorID})
	// A freshly-enqueued suspect item forwards to the trust inbox after commit
	// (step 03 outbox), same as the create path.
	if enqueuedItemID != 0 {
		s.sink.Emit(Event{Kind: EventReviewEnqueued, ThreadID: post.ThreadID, PostID: post.ID, ReviewItemID: enqueuedItemID})
	}
	return &post, nil
}

// Delete tombstones a post: status → deleted while the post_number is
// PRESERVED, so the thread numbering never collapses (invariant 13). Author
// self-delete by default; asModerator is the mod-actor variant (docs/proj/17
// decision 3) — the calling site declares actorID is one of its moderators, the
// author-match check is skipped, and the action is audit-logged (minimal audit
// surface, no table). Both produce the SAME terminal state a moderator reject
// does (ReviewService.Reject) — only the actor differs; every path leaves a
// numbered placeholder instead of removing the row, and they coexist (a post
// already tombstoned by one is an idempotent no-op for the others). posts_count
// is NOT decremented: the tombstone still occupies its post_number, so the
// counter (which tracks numbers allocated, not live posts) stays consistent with
// highest_post_number — matching the mod-reject path, which likewise leaves the
// count untouched.
func (s *PostService) Delete(ctx context.Context, postID, actorID int64, asModerator bool) error {
	modActed := false
	var threadID, authorID int64
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		existing, err := repository.GetPostTx(tx, postID)
		if err != nil {
			return err
		}
		if existing == nil {
			return ErrPostNotFound
		}
		// Tenant guard: the sanctioned single join to the post's thread (Delete
		// otherwise loads only the post) — a cross-tenant caller is answered as
		// "post not found" before the author check.
		thread, err := repository.GetThreadTx(tx, existing.ThreadID)
		if err != nil {
			return err
		}
		if thread == nil || crossTenantCtx(ctx, thread.Site, thread.AnchorKind) {
			return ErrPostNotFound
		}
		if existing.AuthorID != actorID {
			if !asModerator {
				return ErrNotAuthor
			}
			modActed = true
		}
		threadID, authorID = existing.ThreadID, existing.AuthorID
		if existing.Status == model.PostStatusDeleted {
			return nil // already tombstoned → idempotent no-op
		}
		return repository.SetPostStatusTx(tx, postID, model.PostStatusDeleted)
	})
	if err != nil {
		return err
	}
	if modActed {
		slog.Info("community mod delete", "post_id", postID, "thread_id", threadID,
			"post_author_id", authorID, "moderator_id", actorID)
	}
	return nil
}
