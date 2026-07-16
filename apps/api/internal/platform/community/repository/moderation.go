package repository

import (
	"api/internal/platform/community/model"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// InsertFlagTx inserts a report, idempotent per (post, flagger) via ON CONFLICT
// DO NOTHING. Returns true when a new flag row was written (false = the user had
// already flagged this post).
func InsertFlagTx(tx *gorm.DB, flag *model.CommunityFlag) (bool, error) {
	res := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(flag)
	if res.Error != nil {
		return false, res.Error
	}
	return res.RowsAffected > 0, nil
}

// SumPendingFlagWeightTx returns a post's accumulated pending report weight.
func SumPendingFlagWeightTx(tx *gorm.DB, postID int64) (float32, error) {
	var sum float32
	err := tx.Model(&model.CommunityFlag{}).
		Where("post_id = ? AND status = ?", postID, model.FlagStatusPending).
		Select("COALESCE(SUM(weight), 0)").Scan(&sum).Error
	return sum, err
}

// PendingFlaggersTx returns the ids of every user with a pending flag on a post
// — the set whose accuracy a queue decision backfills.
func PendingFlaggersTx(tx *gorm.DB, postID int64) ([]int64, error) {
	var ids []int64
	err := tx.Model(&model.CommunityFlag{}).
		Where("post_id = ? AND status = ?", postID, model.FlagStatusPending).
		Pluck("flagger_id", &ids).Error
	return ids, err
}

// ResolvePendingFlagsTx moves every pending flag on a post to a decided status
// (agreed/disagreed) in one update.
func ResolvePendingFlagsTx(tx *gorm.DB, postID int64, status int16) error {
	return tx.Model(&model.CommunityFlag{}).
		Where("post_id = ? AND status = ?", postID, model.FlagStatusPending).
		Update("status", status).Error
}

// EnqueueReviewIfAbsentTx creates a pending review item for a post unless one
// already exists (a post is enqueued once per open review, not once per flag).
// Returns the new item's id and created=true when a fresh item was created; on
// "already enqueued" it returns (0, false, nil). The caller forwards the created
// item to the trust inbox AFTER the transaction commits (step 03 outbox — never
// HTTP inside the tx).
func EnqueueReviewIfAbsentTx(tx *gorm.DB, site string, postID int64, source int16) (int64, bool, error) {
	var existing int64
	if err := tx.Model(&model.CommunityReviewItem{}).
		Where("post_id = ? AND status = ?", postID, model.ReviewStatusPending).
		Count(&existing).Error; err != nil {
		return 0, false, err
	}
	if existing > 0 {
		return 0, false, nil
	}
	item := model.CommunityReviewItem{
		Site: &site, PostID: &postID, Source: &source, Status: model.ReviewStatusPending,
	}
	if err := tx.Create(&item).Error; err != nil {
		return 0, false, err
	}
	return item.ID, true, nil
}

// ForwardTarget carries the columns the trust-forward payload needs (the review
// item joined to its post for the context-note excerpt).
type ForwardTarget struct {
	ItemID     int64
	Site       string
	PostID     int64
	Source     *int16
	AuthorID   int64
	ContentRaw string
	Forwarded  bool // trust_review_item_id IS NOT NULL
}

// LoadForwardTargetTx joins a review item to its post for the forward payload.
// found=false when the item is gone / has no site or post / the post is absent.
func LoadForwardTargetTx(tx *gorm.DB, itemID int64) (*ForwardTarget, bool, error) {
	var ft ForwardTarget
	err := tx.Table("community_review_item AS ri").
		Select("ri.id AS item_id, ri.site AS site, ri.post_id AS post_id, ri.source AS source, "+
			"(ri.trust_review_item_id IS NOT NULL) AS forwarded, p.author_id AS author_id, p.content_raw AS content_raw").
		Joins("JOIN community_post AS p ON p.id = ri.post_id").
		Where("ri.id = ? AND ri.site IS NOT NULL AND ri.post_id IS NOT NULL", itemID).
		Take(&ft).Error
	if err == gorm.ErrRecordNotFound {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return &ft, true, nil
}

// ScanTarget carries the columns the trust-scan payload needs: a post joined to
// its thread for the tenant site and (first-post-only) the thread title prefix.
type ScanTarget struct {
	PostID     int64
	Site       string
	AuthorID   int64
	ContentRaw string
	PostNumber int32
	Title      string // "" when the thread has no title (a comments thread)
}

// LoadScanTargetTx joins a post to its thread for the scan payload. found=false
// when the post is absent (community never hard-deletes, so a tombstoned post
// still resolves — held/sandbox posts are scanned too, ruling 6).
func LoadScanTargetTx(tx *gorm.DB, postID int64) (*ScanTarget, bool, error) {
	var row struct {
		PostID     int64
		Site       string
		AuthorID   int64
		ContentRaw string
		PostNumber int32
		Title      *string
	}
	err := tx.Table("community_post AS p").
		Select("p.id AS post_id, t.site AS site, p.author_id AS author_id, "+
			"p.content_raw AS content_raw, p.post_number AS post_number, t.title AS title").
		Joins("JOIN community_thread AS t ON t.id = p.thread_id").
		Where("p.id = ?", postID).
		Take(&row).Error
	if err == gorm.ErrRecordNotFound {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	title := ""
	if row.Title != nil {
		title = *row.Title
	}
	return &ScanTarget{
		PostID: row.PostID, Site: row.Site, AuthorID: row.AuthorID,
		ContentRaw: row.ContentRaw, PostNumber: row.PostNumber, Title: title,
	}, true, nil
}

// LockUnforwardedTx claims a batch of not-yet-forwarded review items with FOR
// UPDATE SKIP LOCKED so concurrent sweeps never process the same rows.
func LockUnforwardedTx(tx *gorm.DB, limit int) ([]model.CommunityReviewItem, error) {
	var rows []model.CommunityReviewItem
	err := tx.Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}).
		Where("trust_review_item_id IS NULL AND post_id IS NOT NULL AND site IS NOT NULL").
		Order("id ASC").Limit(limit).Find(&rows).Error
	return rows, err
}

// PostBodyTx reads a post's author + raw content (for the forward context note).
// found=false when the post is absent.
func PostBodyTx(tx *gorm.DB, postID int64) (authorID int64, contentRaw string, found bool, err error) {
	var row struct {
		AuthorID   int64
		ContentRaw string
	}
	e := tx.Table("community_post").Select("author_id, content_raw").
		Where("id = ?", postID).Take(&row).Error
	if e == gorm.ErrRecordNotFound {
		return 0, "", false, nil
	}
	if e != nil {
		return 0, "", false, e
	}
	return row.AuthorID, row.ContentRaw, true, nil
}

// SetTrustReviewItemIDTx back-fills the trust item id on an unforwarded row
// (idempotent — the WHERE guards against clobbering a concurrent success).
func SetTrustReviewItemIDTx(tx *gorm.DB, itemID, trustItemID int64) error {
	return tx.Model(&model.CommunityReviewItem{}).
		Where("id = ? AND trust_review_item_id IS NULL", itemID).
		Update("trust_review_item_id", trustItemID).Error
}

// BumpForwardAttemptsTx increments the sweep attempt counter for a row.
func BumpForwardAttemptsTx(tx *gorm.DB, itemID int64) error {
	return tx.Model(&model.CommunityReviewItem{}).Where("id = ?", itemID).
		Update("forward_attempts", gorm.Expr("forward_attempts + 1")).Error
}

// CloseReviewItemsForPostTx closes every still-pending review item for a post
// (system-decided: decided_by stays NULL). Used when a trust callback enforces
// on a post — the local queue item is closed alongside (step 03 ruling 7).
func CloseReviewItemsForPostTx(tx *gorm.DB, postID int64, status int16) error {
	return tx.Model(&model.CommunityReviewItem{}).
		Where("post_id = ? AND status = ?", postID, model.ReviewStatusPending).
		Updates(map[string]any{"status": status, "decided_at": gorm.Expr("now()")}).Error
}

// ReviewRepository reads the moderation queue.
type ReviewRepository struct{ db *gorm.DB }

func NewReviewRepository(db *gorm.DB) *ReviewRepository { return &ReviewRepository{db: db} }

// ReviewItemRow is a pending queue item joined to its subject post's thread +
// author, so a consuming site can deep-link the post (thread page) and resolve
// the author without a per-item round trip. Flat ad-hoc projection with
// explicit column tags (never rely on GORM name derivation for ad-hoc scans).
type ReviewItemRow struct {
	ID        int64   `gorm:"column:id"`
	Site      *string `gorm:"column:site"`
	PostID    *int64  `gorm:"column:post_id"`
	Source    *int16  `gorm:"column:source"`
	Status    int16   `gorm:"column:status"`
	DecidedBy *int64  `gorm:"column:decided_by"`
	// ThreadID / AuthorID come from the subject post (LEFT JOIN — nil when the
	// item has no post or the post is gone).
	ThreadID *int64 `gorm:"column:thread_id"`
	AuthorID *int64 `gorm:"column:author_id"`
}

// ListPending returns pending queue items for a site (joined to their subject
// post's thread + author), newest first. source ≥ 0 filters to one source;
// source < 0 = all sources.
func (r *ReviewRepository) ListPending(site string, source int16, limit int) ([]ReviewItemRow, error) {
	q := r.db.Table("community_review_item AS ri").
		Select("ri.id, ri.site, ri.post_id, ri.source, ri.status, ri.decided_by, p.thread_id, p.author_id").
		Joins("LEFT JOIN community_post AS p ON p.id = ri.post_id").
		Where("ri.site = ? AND ri.status = ?", site, model.ReviewStatusPending)
	if source >= 0 {
		q = q.Where("ri.source = ?", source)
	}
	var items []ReviewItemRow
	err := q.Order("ri.id DESC").Limit(limit).Scan(&items).Error
	return items, err
}

// GetReviewItemTx reads a queue item inside a tx (nil when absent).
func GetReviewItemTx(tx *gorm.DB, id int64) (*model.CommunityReviewItem, error) {
	var item model.CommunityReviewItem
	err := tx.First(&item, id).Error
	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &item, nil
}

// DecideReviewItemTx records a queue decision (approved/rejected + who + when).
func DecideReviewItemTx(tx *gorm.DB, id int64, status int16, decidedBy int64) error {
	return tx.Model(&model.CommunityReviewItem{}).Where("id = ?", id).Updates(map[string]any{
		"status":     status,
		"decided_by": decidedBy,
		"decided_at": gorm.Expr("now()"),
	}).Error
}

// PostContext is a post's moderation context: its author, current status, and
// the tenant site of its thread (for enqueuing site-scoped review items), plus
// the thread id and anchor — returned through the reaction toggle so a consuming
// site can fan out its like notification (recipient + jump target) without a
// second read (docs/proj/17).
type PostContext struct {
	AuthorID   int64
	Status     int16
	Site       string
	ThreadID   int64
	AnchorKind int16
	AnchorID   string
}

// PostContextTx joins post→thread to fetch the moderation context. Returns
// found=false when the post is absent.
func PostContextTx(tx *gorm.DB, postID int64) (PostContext, bool, error) {
	var pc PostContext
	err := tx.Table("community_post AS p").
		Select("p.author_id AS author_id, p.status AS status, t.site AS site, t.id AS thread_id, t.anchor_kind AS anchor_kind, t.anchor_id AS anchor_id").
		Joins("JOIN community_thread AS t ON t.id = p.thread_id").
		Where("p.id = ?", postID).
		Take(&pc).Error
	if err == gorm.ErrRecordNotFound {
		return PostContext{}, false, nil
	}
	if err != nil {
		return PostContext{}, false, err
	}
	return pc, true, nil
}
