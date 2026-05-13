package repository

import (
	"context"

	"api/internal/platform/galgame/model"

	"gorm.io/gorm"
)

// MessageRepository handles galgame_message table access.
type MessageRepository struct {
	db *gorm.DB
}

// NewMessageRepository creates a new MessageRepository.
func NewMessageRepository(db *gorm.DB) *MessageRepository {
	return &MessageRepository{db: db}
}

// DB exposes the underlying gorm.DB for transactions.
func (r *MessageRepository) DB() *gorm.DB { return r.db }

// Create writes one message row. Callers usually pass a tx from an outer
// transaction so the message is committed atomically with the galgame change.
func (r *MessageRepository) Create(ctx context.Context, tx *gorm.DB, msg *model.GalgameMessage) error {
	if tx == nil {
		tx = r.db
	}
	return tx.WithContext(ctx).Create(msg).Error
}

// ListMine returns messages targeted at the given user, id-descending.
// since_id is exclusive lower bound (id > since_id).
func (r *MessageRepository) ListMine(ctx context.Context, uid int, sinceID int64, limit int) ([]model.GalgameMessage, int64, error) {
	var items []model.GalgameMessage
	var total int64

	q := r.db.WithContext(ctx).Model(&model.GalgameMessage{}).Where("target_user_id = ?", uid)
	q.Count(&total)

	if sinceID > 0 {
		q = q.Where("id > ?", sinceID)
	}
	err := q.Order("id DESC").Limit(limit).Find(&items).Error
	return items, total, err
}

// ListFeed returns messages with non-null target, id-ascending.
// Used by kungal/moyu cron to consume events linearly.
// since_id is exclusive lower bound.
func (r *MessageRepository) ListFeed(ctx context.Context, sinceID int64, limit int) ([]model.GalgameMessage, error) {
	var items []model.GalgameMessage
	q := r.db.WithContext(ctx).Model(&model.GalgameMessage{}).
		Where("target_user_id IS NOT NULL")
	if sinceID > 0 {
		q = q.Where("id > ?", sinceID)
	}
	err := q.Order("id ASC").Limit(limit).Find(&items).Error
	return items, err
}

// ListAdminQueue returns messages of given types whose linked galgame is
// still in status=3. Used by admin web UI to show pending review queue.
//
// We JOIN galgame and filter status=3 so messages whose galgame has already
// been approved/declined/banned/deleted automatically drop out — no need to
// maintain a "handled" flag on the message row.
func (r *MessageRepository) ListAdminQueue(ctx context.Context, types []string, page, limit int) ([]model.GalgameMessage, int64, error) {
	var items []model.GalgameMessage
	var total int64

	q := r.db.WithContext(ctx).Model(&model.GalgameMessage{}).
		Joins("JOIN galgame g ON g.id = galgame_message.galgame_id").
		Where("galgame_message.type IN ?", types).
		Where("g.status = ?", model.GalgameStatusPending)

	q.Count(&total)

	err := q.Order("galgame_message.id DESC").
		Offset((page - 1) * limit).
		Limit(limit).
		Find(&items).Error
	return items, total, err
}
