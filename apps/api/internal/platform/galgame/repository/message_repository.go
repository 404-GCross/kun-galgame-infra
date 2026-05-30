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
func (r *MessageRepository) ListMine(ctx context.Context, userID int, sinceID int64, limit int) ([]model.GalgameMessage, int64, error) {
	var items []model.GalgameMessage
	var total int64

	q := r.db.WithContext(ctx).Model(&model.GalgameMessage{}).Where("target_user_id = ?", userID)
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}

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

// LatestDeclinedByGalgameIDs returns the most recent 'declined' message per
// galgame in `ids`, addressed to `userID`. Used by SubmissionService.ListMine
// to surface the decline reason on the user's own "my submissions" page —
// the user sees "rejected because: X" without having to dig into messages.
//
// Returns map[galgame_id]message. Galgames with no declined message (e.g.
// status=3 entries) are absent from the map.
func (r *MessageRepository) LatestDeclinedByGalgameIDs(ctx context.Context, userID int, ids []int) (map[int]model.GalgameMessage, error) {
	if len(ids) == 0 {
		return map[int]model.GalgameMessage{}, nil
	}
	// PostgreSQL: DISTINCT ON gives one row per galgame_id, ordered by id
	// DESC inside the partition. Compatible with any pg version.
	var rows []model.GalgameMessage
	err := r.db.WithContext(ctx).
		Raw(`
			SELECT DISTINCT ON (galgame_id) *
			FROM galgame_message
			WHERE galgame_id IN ?
			  AND type = ?
			  AND target_user_id = ?
			ORDER BY galgame_id, id DESC
		`, ids, model.MessageTypeDeclined, userID).
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	out := make(map[int]model.GalgameMessage, len(rows))
	for _, m := range rows {
		out[m.GalgameID] = m
	}
	return out, nil
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

	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	err := q.Order("galgame_message.id DESC").
		Offset((page - 1) * limit).
		Limit(limit).
		Find(&items).Error
	return items, total, err
}
