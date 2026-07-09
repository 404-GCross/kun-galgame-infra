package service

import (
	"context"
	"strings"
	"time"

	"api/internal/platform/community/model"
	"api/internal/platform/community/repository"
	"api/internal/platform/community/sanitize"

	"gorm.io/datatypes"
	"gorm.io/gorm"
)

// ThreadService owns thread creation (the three shapes) and thread reads.
type ThreadService struct {
	db      *gorm.DB
	threads *repository.ThreadRepository
	trusts  *repository.TrustRepository
	sink    EventSink
}

func NewThreadService(db *gorm.DB, sink EventSink) *ThreadService {
	return &ThreadService{
		db:      db,
		threads: repository.NewThreadRepository(db),
		trusts:  repository.NewTrustRepository(db),
		sink:    sink,
	}
}

// OpenThreadParams describes a topic/feedback thread being opened with its
// opening post (#1).
type OpenThreadParams struct {
	Site              string
	AuthorID          int64
	AnchorKind        int16
	AnchorID          string
	Title             string
	ContentRating     int16
	BodyRaw           string
	HeaderImageHashes datatypes.JSON // optional; nil for none
}

// OpenTopic opens a board topic (kind=0) with its opening post.
func (s *ThreadService) OpenTopic(ctx context.Context, p OpenThreadParams) (*model.CommunityThread, *model.CommunityPost, error) {
	return s.openWithFirstPost(ctx, model.ThreadKindTopic, p)
}

// OpenFeedback opens a feedback thread (kind=2, fb_status=open) with its
// opening post.
func (s *ThreadService) OpenFeedback(ctx context.Context, p OpenThreadParams) (*model.CommunityThread, *model.CommunityPost, error) {
	return s.openWithFirstPost(ctx, model.ThreadKindFeedback, p)
}

func (s *ThreadService) openWithFirstPost(ctx context.Context, kind int16, p OpenThreadParams) (*model.CommunityThread, *model.CommunityPost, error) {
	cooked := sanitize.Cook(p.BodyRaw)
	level, err := trustLevel(s.trusts, p.AuthorID)
	if err != nil {
		return nil, nil, err
	}
	if err := checkContentSandbox(level, cooked); err != nil {
		return nil, nil, err
	}
	if isSandboxed(level) {
		n, err := s.threads.CountOpenedByCreatorSince(p.AuthorID, time.Now().Add(-sandboxWindow))
		if err != nil {
			return nil, nil, err
		}
		if n >= tl0MaxTopicsPerDay {
			return nil, nil, &SandboxError{Reason: "daily topic limit"}
		}
	}

	now := time.Now()
	var thread model.CommunityThread
	var post model.CommunityPost
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		trust, err := repository.GetOrCreateTrustTx(tx, p.AuthorID)
		if err != nil {
			return err
		}
		held := trust.FirstPostsHeldRemaining > 0

		var title *string
		if p.Title != "" {
			title = &p.Title
		}
		thread = model.CommunityThread{
			Site: p.Site, Kind: kind, AnchorKind: p.AnchorKind, AnchorID: p.AnchorID,
			Title: title, HeaderImageHashes: p.HeaderImageHashes,
			ContentRating: p.ContentRating, Status: model.ThreadStatusOpen,
			// Born WITH the opening post: all counters reflect post #1, so no
			// default-tag fighting (every value here is non-zero → written).
			PostsCount: 1, ParticipantsCount: 1, HighestPostNumber: 1, LastPostedAt: &now,
			CreatedBy: p.AuthorID,
		}
		if kind == model.ThreadKindFeedback {
			open := model.FeedbackStatusOpen
			thread.FbStatus = &open
		}
		if err := repository.CreateThreadTx(tx, &thread, false); err != nil {
			return err
		}

		post = model.CommunityPost{
			ThreadID: thread.ID, PostNumber: 1, AuthorID: p.AuthorID,
			ContentRaw: p.BodyRaw, ContentHTML: cooked.HTML, SanitizerVersion: int32(cooked.Version),
			ContentRating: p.ContentRating, Status: postStatus(held),
		}
		if err := repository.CreatePostTx(tx, &post); err != nil {
			return err
		}
		if held {
			return repository.DecrementHoldTx(tx, p.AuthorID)
		}
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	s.sink.Emit(Event{Kind: EventPostCreated, ThreadID: thread.ID, PostID: post.ID, ActorID: p.AuthorID})
	return &thread, &post, nil
}

// CommentsThreadParams describes the anchor a comments thread is (lazily)
// created for (Coral story model: first access/comment auto-creates it).
type CommentsThreadParams struct {
	Site          string
	AnchorKind    int16
	AnchorID      string
	ContentRating int16
	ActorID       int64
}

// GetOrCreateCommentsThread returns the single live comments thread for an
// anchor, creating an empty one when none exists (invariant 4). Idempotent and
// race-safe: a concurrent create loses the partial-unique and re-reads.
func (s *ThreadService) GetOrCreateCommentsThread(ctx context.Context, p CommentsThreadParams) (*model.CommunityThread, error) {
	if t, err := s.threads.GetLiveCommentsThread(p.AnchorKind, p.AnchorID); err != nil {
		return nil, err
	} else if t != nil {
		return t, nil
	}
	thread := model.CommunityThread{
		Site: p.Site, Kind: model.ThreadKindComments, AnchorKind: p.AnchorKind, AnchorID: p.AnchorID,
		ContentRating: p.ContentRating, Status: model.ThreadStatusOpen,
		// Born empty: CreateThreadTx forces participants_count to 0.
		PostsCount: 0, ParticipantsCount: 0, HighestPostNumber: 0,
		CreatedBy: p.ActorID,
	}
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return repository.CreateThreadTx(tx, &thread, true)
	})
	if err == nil {
		return &thread, nil
	}
	if isDuplicate(err) {
		// A sibling call created it first — return the winner.
		if t, e := s.threads.GetLiveCommentsThread(p.AnchorKind, p.AnchorID); e == nil && t != nil {
			return t, nil
		}
	}
	return nil, err
}

// Get returns a thread by id, or nil when absent.
func (s *ThreadService) Get(id int64) (*model.CommunityThread, error) {
	return s.threads.GetByID(id)
}

// ListBySite / ListByAnchor expose the two invariant-7 read dimensions.
func (s *ThreadService) ListBySite(site string, kind int16, cursor repository.ThreadCursor, limit int) ([]model.CommunityThread, error) {
	return s.threads.ListBySite(site, kind, cursor, clampLimit(limit))
}

func (s *ThreadService) ListByAnchor(anchorKind int16, anchorID string, kind int16) ([]model.CommunityThread, error) {
	return s.threads.ListByAnchor(anchorKind, anchorID, kind)
}

func postStatus(held bool) int16 {
	if held {
		return model.PostStatusHidden
	}
	return model.PostStatusVisible
}

func clampLimit(limit int) int {
	if limit <= 0 || limit > 100 {
		return 50
	}
	return limit
}

// isDuplicate reports whether err is a Postgres unique-violation (used for the
// comments-thread get-or-create race).
func isDuplicate(err error) bool {
	return err != nil && strings.Contains(err.Error(), "duplicate key")
}
