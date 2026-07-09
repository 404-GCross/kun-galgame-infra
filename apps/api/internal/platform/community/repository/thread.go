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
func (r *ThreadRepository) ListBySite(site string, kind int16, cursor ThreadCursor, limit int) ([]model.CommunityThread, error) {
	q := r.db.Where("site = ? AND kind = ?", site, kind)
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

// BumpThreadCountersTx applies one new post's effect on the denormalized
// counters in the same tx (doc 11 §4.3: application-maintained, no triggers).
// newParticipant increments participants_count only when the author had not
// posted in this thread before.
func BumpThreadCountersTx(tx *gorm.DB, threadID int64, postNumber int32, at time.Time, newParticipant bool) error {
	updates := map[string]any{
		"posts_count":         gorm.Expr("posts_count + 1"),
		"highest_post_number": postNumber,
		"last_posted_at":      at,
		"updated_at":          at,
	}
	if newParticipant {
		updates["participants_count"] = gorm.Expr("participants_count + 1")
	}
	return tx.Model(&model.CommunityThread{}).Where("id = ?", threadID).Updates(updates).Error
}
