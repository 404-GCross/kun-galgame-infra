// Package repository is the data-access layer of the community platform domain.
// Following the catalog convention: read paths hold their own *gorm.DB;
// multi-table write paths are package-level helpers that take the caller's
// transaction, so the service composes them into one atomic unit.
package repository

import (
	"time"

	"api/internal/platform/community/model"

	"gorm.io/gorm"
)

// CountOpenedByCreatorSince counts topic/feedback threads a user opened since t
// — the first-day topic-cap input for the TL0 sandbox (comments threads, which
// are system-opened per anchor, are excluded).
func (r *ThreadRepository) CountOpenedByCreatorSince(creatorID int64, since time.Time) (int64, error) {
	var n int64
	err := r.db.Model(&model.CommunityThread{}).
		Where("created_by = ? AND created_at >= ? AND kind IN ?",
			creatorID, since, []int16{model.ThreadKindTopic, model.ThreadKindFeedback}).
		Count(&n).Error
	return n, err
}

// ThreadRepository reads community_thread.
type ThreadRepository struct{ db *gorm.DB }

func NewThreadRepository(db *gorm.DB) *ThreadRepository { return &ThreadRepository{db: db} }

// GetByID returns the thread or nil when absent.
func (r *ThreadRepository) GetByID(id int64) (*model.CommunityThread, error) {
	return getThread(r.db, id)
}

func getThread(db *gorm.DB, id int64) (*model.CommunityThread, error) {
	var t model.CommunityThread
	err := db.First(&t, id).Error
	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &t, nil
}

// GetThreadTx reads a thread inside the caller's transaction (nil when absent).
func GetThreadTx(tx *gorm.DB, id int64) (*model.CommunityThread, error) {
	return getThread(tx, id)
}

// GetLiveCommentsThread returns the single live (status<>deleted) comments
// thread for an anchor, or nil — the read side of invariant 4.
func (r *ThreadRepository) GetLiveCommentsThread(anchorKind int16, anchorID string) (*model.CommunityThread, error) {
	return getLiveCommentsThread(r.db, anchorKind, anchorID)
}

func getLiveCommentsThread(db *gorm.DB, anchorKind int16, anchorID string) (*model.CommunityThread, error) {
	var t model.CommunityThread
	err := db.Where(
		"anchor_kind = ? AND anchor_id = ? AND kind = ? AND status <> ?",
		anchorKind, anchorID, model.ThreadKindComments, model.ThreadStatusDeleted,
	).First(&t).Error
	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &t, nil
}

// ThreadCursor is the keyset cursor of the per-site thread list: ordered
// (last_posted_at DESC NULLS LAST, id DESC). LastPostedNull marks the NULL tail
// (threads with no activity yet). The zero value starts from the top.
type ThreadCursor struct {
	LastPostedNull bool
	LastPosted     time.Time
	ID             int64
}

// ListBySite returns up to limit threads of a kind for a site, newest activity
// first, after the cursor (invariant 7 tenant-first index). NULL last_posted_at
// (activity-less threads) sort last, then by id — the keyset handles both zones.
// An optional anchor filter (anchorID != "") narrows the page to a single anchor
// within the tenant — the resource-detail feedback wall / per-anchor read path;
// it is generic across kinds (a comments/topic anchor listing benefits too). A
// real anchor id is never empty, so "" is a safe "no filter" sentinel.
func (r *ThreadRepository) ListBySite(site string, kind int16, anchorKind int16, anchorID string, cursor ThreadCursor, limit int) ([]model.CommunityThread, error) {
	q := r.db.Where("site = ? AND kind = ?", site, kind)
	if anchorID != "" {
		q = q.Where("anchor_kind = ? AND anchor_id = ?", anchorKind, anchorID)
	}
	if cursor != (ThreadCursor{}) {
		if cursor.LastPostedNull {
			// Already in the NULL tail: only later NULL rows remain.
			q = q.Where("last_posted_at IS NULL AND id < ?", cursor.ID)
		} else {
			q = q.Where(
				"last_posted_at IS NULL OR last_posted_at < ? OR (last_posted_at = ? AND id < ?)",
				cursor.LastPosted, cursor.LastPosted, cursor.ID,
			)
		}
	}
	var rows []model.CommunityThread
	err := q.Order("last_posted_at DESC NULLS LAST, id DESC").Limit(limit).Find(&rows).Error
	return rows, err
}

// OpeningPostMeta is the opening post (post_number=1) status + author of a
// thread — the projection the per-site topic/feedback list needs to hide an
// opening post that must not leak its title: a held (TL0 first-post) opening
// post is author-only, a self-deleted (tombstoned) one is gone. The thread's
// own status stays open in both cases (the moderation state lives on the post),
// so the list cannot filter on community_thread.status alone.
type OpeningPostMeta struct {
	ThreadID int64 `gorm:"column:thread_id"`
	Status   int16 `gorm:"column:status"`
	AuthorID int64 `gorm:"column:author_id"`
}

// OpeningPostMetaByThreadIDs returns the opening post (post_number=1) status +
// author for each of threadIDs, keyed by thread id. A thread with no opening
// post yet (an empty comments thread) is simply absent from the map. One batch
// query — the caller merges it into the thread-list view.
func (r *ThreadRepository) OpeningPostMetaByThreadIDs(threadIDs []int64) (map[int64]OpeningPostMeta, error) {
	out := make(map[int64]OpeningPostMeta, len(threadIDs))
	if len(threadIDs) == 0 {
		return out, nil
	}
	var rows []OpeningPostMeta
	err := r.db.Model(&model.CommunityPost{}).
		Select("thread_id, status, author_id").
		Where("thread_id IN ? AND post_number = 1", threadIDs).
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	for _, m := range rows {
		out[m.ThreadID] = m
	}
	return out, nil
}

// ListByAnchor returns every thread for an anchor of a kind — the cross-site
// aggregation dimension of invariant 7 (NextMoe read).
func (r *ThreadRepository) ListByAnchor(anchorKind int16, anchorID string, kind int16) ([]model.CommunityThread, error) {
	var rows []model.CommunityThread
	err := r.db.
		Where("anchor_kind = ? AND anchor_id = ? AND kind = ?", anchorKind, anchorID, kind).
		Order("last_posted_at DESC NULLS LAST, id DESC").
		Find(&rows).Error
	return rows, err
}

// --- tx write helpers ------------------------------------------------------

// CreateThreadTx inserts a thread. bornEmpty forces participants_count back to
// 0 for a Coral-style comments thread (no participants until the first
// comment): the DDL default is 1 (it assumes an opener-born thread), and GORM
// both omits the zero on INSERT AND reads the default back into the struct via
// RETURNING, so the zero must be written explicitly here.
func CreateThreadTx(tx *gorm.DB, t *model.CommunityThread, bornEmpty bool) error {
	if err := tx.Create(t).Error; err != nil {
		return err
	}
	if bornEmpty {
		if err := tx.Model(t).UpdateColumn("participants_count", 0).Error; err != nil {
			return err
		}
		t.ParticipantsCount = 0
	}
	return nil
}

// AllocateReplyTx atomically bumps a thread's denormalized counters for one new
// reply and RETURNS the allocated post_number (doc 11 §4.3: app-maintained, no
// triggers). The single UPDATE takes a row lock, so concurrent replies serialize
// and never collide on a post_number — the numbering stays gap-free and
// dup-free without an advisory retry loop. newParticipant increments
// participants_count only when the author had not posted in the thread before.
func AllocateReplyTx(tx *gorm.DB, threadID int64, at time.Time, newParticipant bool) (int32, error) {
	incParticipant := 0
	if newParticipant {
		incParticipant = 1
	}
	var number int32
	err := tx.Raw(`
		UPDATE community_thread
		   SET posts_count         = posts_count + 1,
		       highest_post_number = highest_post_number + 1,
		       participants_count  = participants_count + ?,
		       last_posted_at      = ?,
		       updated_at          = ?
		 WHERE id = ?
		RETURNING highest_post_number`,
		incParticipant, at, at, threadID,
	).Scan(&number).Error
	return number, err
}
