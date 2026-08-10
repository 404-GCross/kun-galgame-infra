package repository

import (
	"time"

	"api/internal/platform/community/model"

	"gorm.io/gorm"
)

func (r *ThreadRepository) CountOpenedByCreatorSince(creatorID int64, since time.Time) (int64, error) {
	var n int64
	err := r.db.Model(&model.CommunityThread{}).
		Where("created_by = ? AND created_at >= ? AND kind IN ?",
			creatorID, since, []int16{model.ThreadKindTopic, model.ThreadKindFeedback}).
		Count(&n).Error
	return n, err
}

type ThreadRepository struct{ db *gorm.DB }

func NewThreadRepository(db *gorm.DB) *ThreadRepository { return &ThreadRepository{db: db} }

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

func GetThreadTx(tx *gorm.DB, id int64) (*model.CommunityThread, error) {
	return getThread(tx, id)
}

func (r *ThreadRepository) GetLiveCommentsThread(site string, anchorKind int16, anchorID string) (*model.CommunityThread, error) {
	return getLiveCommentsThread(r.db, site, anchorKind, anchorID)
}

func getLiveCommentsThread(db *gorm.DB, site string, anchorKind int16, anchorID string) (*model.CommunityThread, error) {
	q := db.Where(
		"anchor_kind = ? AND anchor_id = ? AND kind = ? AND status <> ?",
		anchorKind, anchorID, model.ThreadKindComments, model.ThreadStatusDeleted,
	)
	if model.AnchorIsSiteLocal(anchorKind) {
		q = q.Where("site = ?", site)
	}
	var t model.CommunityThread
	err := q.First(&t).Error
	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &t, nil
}

type ThreadCursor struct {
	LastPostedNull bool
	LastPosted     time.Time
	ID             int64
}

func (r *ThreadRepository) ListBySite(site string, kind int16, anchorKind int16, anchorID string, cursor ThreadCursor, limit int) ([]model.CommunityThread, error) {
	q := r.db.Where("site = ? AND kind = ?", site, kind)
	if anchorID != "" {
		q = q.Where("anchor_kind = ? AND anchor_id = ?", anchorKind, anchorID)
	}
	if cursor != (ThreadCursor{}) {
		if cursor.LastPostedNull {
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

type OpeningPostMeta struct {
	ThreadID int64 `gorm:"column:thread_id"`
	Status   int16 `gorm:"column:status"`
	AuthorID int64 `gorm:"column:author_id"`
}

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

func (r *ThreadRepository) ListByAnchor(site string, anchorKind int16, anchorID string, kind int16) ([]model.CommunityThread, error) {
	q := r.db.Where("anchor_kind = ? AND anchor_id = ? AND kind = ?", anchorKind, anchorID, kind)
	if model.AnchorIsSiteLocal(anchorKind) {
		q = q.Where("site = ?", site)
	}
	var rows []model.CommunityThread
	err := q.Order("last_posted_at DESC NULLS LAST, id DESC").Find(&rows).Error
	return rows, err
}

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
