package repository

import (
	"time"

	"api/internal/platform/community/model"

	"gorm.io/gorm"
)

// PostRepository reads community_post.
type PostRepository struct{ db *gorm.DB }

func NewPostRepository(db *gorm.DB) *PostRepository { return &PostRepository{db: db} }

// ListByThread returns up to limit posts of a thread with post_number greater
// than afterNumber, in ascending post_number order — the keyset read path (the
// permalink numbering is the natural cursor; pass 0 for the first page).
func (r *PostRepository) ListByThread(threadID int64, afterNumber int32, limit int) ([]model.CommunityPost, error) {
	var rows []model.CommunityPost
	err := r.db.
		Where("thread_id = ? AND post_number > ?", threadID, afterNumber).
		Order("post_number ASC").
		Limit(limit).
		Find(&rows).Error
	return rows, err
}

// CountByAuthorSince counts a user's posts created since t — the first-day
// reply-cap input for the TL0 sandbox.
func (r *PostRepository) CountByAuthorSince(authorID int64, since time.Time) (int64, error) {
	var n int64
	err := r.db.Model(&model.CommunityPost{}).
		Where("author_id = ? AND created_at >= ?", authorID, since).
		Count(&n).Error
	return n, err
}

// --- tx write helpers ------------------------------------------------------

// CreatePostTx inserts a post (post_number already allocated by the caller
// inside the same tx).
func CreatePostTx(tx *gorm.DB, p *model.CommunityPost) error {
	return tx.Create(p).Error
}

// AuthorHasPostedTx reports whether the author already has a post in the thread
// — drives the participants_count increment decision.
func AuthorHasPostedTx(tx *gorm.DB, threadID, authorID int64) (bool, error) {
	var n int64
	if err := tx.Model(&model.CommunityPost{}).
		Where("thread_id = ? AND author_id = ?", threadID, authorID).
		Count(&n).Error; err != nil {
		return false, err
	}
	return n > 0, nil
}

// SetPostStatusTx sets a post's status (flag auto-hide, queue approve/reject
// tombstone — invariant 13: status, not gorm.DeletedAt).
func SetPostStatusTx(tx *gorm.DB, postID int64, status int16) error {
	return tx.Model(&model.CommunityPost{}).Where("id = ?", postID).
		Update("status", status).Error
}
