package main

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"api/internal/platform/community/model"
	"api/internal/platform/community/sanitize"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const trustBatch = 1000

func run(src, tgt *gorm.DB, site string, apply bool) (*Report, error) {
	rep := &Report{}

	if apply {
		if err := src.Exec(mapTableDDL).Error; err != nil {
			return nil, fmt.Errorf("create map table: %w", err)
		}
	}

	authors := make(map[int64]bool)

	for _, s := range sources {
		rows, err := loadSource(src, s)
		if err != nil {
			return nil, err
		}
		byEntity := groupByEntity(rows)

		existingMap, err := loadExistingMap(src, s.Name)
		if err != nil {
			return nil, err
		}

		entityIDs := make([]int, 0, len(byEntity))
		for eid := range byEntity {
			entityIDs = append(entityIDs, eid)
		}
		slices.Sort(entityIDs)

		sr := rep.source(s.Name)
		sr.EntitiesTotal = len(entityIDs)
		sr.MaxRunes = s.MaxRunes

		for _, eid := range entityIDs {
			plan := planEntity(s.IsTree, s.MaxRunes, byEntity[eid])
			for i := range plan.Posts {
				authors[plan.Posts[i].AuthorID] = true
			}
			sr.DanglingParents += plan.Dangling
			sr.SelfTargets += plan.SelfTargets
			sr.OverLenRows += plan.OverLen

			if err := processEntity(tgt, src, site, s, plan, existingMap, apply, sr); err != nil {
				return nil, fmt.Errorf("%s entity %d: %w", s.Name, eid, err)
			}
		}
	}

	if err := seedTrust(tgt, authors, apply, rep); err != nil {
		return nil, err
	}
	return rep, nil
}

func processEntity(tgt, src *gorm.DB, site string, s sourceSpec, plan ThreadPlan,
	existingMap map[int]int64, apply bool, sr *SourceReport) error {

	anchorID := s.Name + ":" + strconv.Itoa(plan.EntityID)

	oldToNew := make(map[int]int64, len(plan.Posts))
	for i := range plan.Posts {
		if pid, ok := existingMap[plan.Posts[i].OldID]; ok {
			oldToNew[plan.Posts[i].OldID] = pid
		}
	}

	if !apply {
		return dryRunEntity(tgt, site, anchorID, plan, existingMap, sr)
	}

	type mapRow struct {
		old  int
		post int64
	}
	var newMapRows []mapRow
	var threadID int64

	writeThread := func(tx *gorm.DB) error {
		var th model.CommunityThread
		findErr := tx.Where(
			"site = ? AND anchor_kind = ? AND anchor_id = ? AND kind = ? AND status <> ?",
			site, model.AnchorKindSiteResource, anchorID, model.ThreadKindComments, model.ThreadStatusDeleted,
		).First(&th).Error

		switch {
		case findErr == nil:
			sr.ThreadsExisting++
		case errors.Is(findErr, gorm.ErrRecordNotFound):
			th = model.CommunityThread{
				Site:              site,
				Kind:              model.ThreadKindComments,
				AnchorKind:        model.AnchorKindSiteResource,
				AnchorID:          anchorID,
				Title:             nil,
				ContentRating:     model.ContentRatingAll,
				Status:            model.ThreadStatusOpen,
				PostsCount:        plan.PostsCount,
				ParticipantsCount: plan.ParticipantsCount,
				HighestPostNumber: plan.HighestPostNumber,
				LastPostedAt:      tp(plan.LastPostedAt),
				CreatedBy:         plan.FirstAuthorID,
				CreatedAt:         plan.CreatedAt,
				UpdatedAt:         plan.LastPostedAt,
			}
			if err := tx.Create(&th).Error; err != nil {
				return fmt.Errorf("create thread: %w", err)
			}
			sr.ThreadsToCreate++
		default:
			return fmt.Errorf("find thread: %w", findErr)
		}
		threadID = th.ID

		existingByNumber := make(map[int32]int64)
		{
			var pnRows []struct {
				PostNumber int32
				ID         int64
			}
			if err := tx.Model(&model.CommunityPost{}).
				Select("post_number, id").Where("thread_id = ?", th.ID).
				Find(&pnRows).Error; err != nil {
				return fmt.Errorf("load existing posts: %w", err)
			}
			for _, r := range pnRows {
				existingByNumber[r.PostNumber] = r.ID
			}
		}

		for i := range plan.Posts {
			pp := plan.Posts[i]
			if _, done := oldToNew[pp.OldID]; done {
				sr.PostsExisting++
				continue
			}
			if id, ok := existingByNumber[pp.PostNumber]; ok {
				oldToNew[pp.OldID] = id
				newMapRows = append(newMapRows, mapRow{pp.OldID, id})
				sr.PostsExisting++
				continue
			}
			cooked := sanitize.Cook(pp.Content)
			post := model.CommunityPost{
				ThreadID:         th.ID,
				PostNumber:       pp.PostNumber,
				RootPostID:       resolvePtr(oldToNew, pp.RootOldID),
				ReplyToPostID:    resolvePtr(oldToNew, pp.ReplyToOldID),
				TargetUserID:     pp.TargetUserID,
				AuthorID:         pp.AuthorID,
				ContentRaw:       pp.Content,
				ContentHTML:      cooked.HTML,
				SanitizerVersion: int32(cooked.Version),
				ContentRating:    model.ContentRatingAll,
				Status:           model.PostStatusVisible,
				EditedAt:         pp.EditedAt,
				CreatedAt:        pp.CreatedAt,
			}
			if err := tx.Create(&post).Error; err != nil {
				return fmt.Errorf("create post (old_id=%d): %w", pp.OldID, err)
			}
			oldToNew[pp.OldID] = post.ID
			newMapRows = append(newMapRows, mapRow{pp.OldID, post.ID})
			sr.PostsToInsert++
		}

		return recomputeThreadCounters(tx, th.ID)
	}

	if err := tgt.Transaction(writeThread); err != nil {
		return err
	}

	if len(newMapRows) > 0 {
		rows := make([]resourceMap, 0, len(newMapRows))
		for _, m := range newMapRows {
			rows = append(rows, resourceMap{Source: s.Name, OldID: m.old, ThreadID: threadID, PostID: m.post})
		}
		if err := src.Clauses(clause.OnConflict{DoNothing: true}).CreateInBatches(rows, 500).Error; err != nil {
			return fmt.Errorf("write ledger rows: %w", err)
		}
		sr.MapRowsToWrite += len(rows)
	}
	if s.CounterTable != "" {
		q := fmt.Sprintf("UPDATE %s SET comment_count = ? WHERE %s = ?", s.CounterTable, s.CounterCol)
		if err := src.Exec(q, plan.VisibleCount, plan.EntityID).Error; err != nil {
			return fmt.Errorf("reset comment_count: %w", err)
		}
	}
	return nil
}

func dryRunEntity(tgt *gorm.DB, site, anchorID string, plan ThreadPlan,
	existingMap map[int]int64, sr *SourceReport) error {

	var th model.CommunityThread
	findErr := tgt.Where(
		"site = ? AND anchor_kind = ? AND anchor_id = ? AND kind = ? AND status <> ?",
		site, model.AnchorKindSiteResource, anchorID, model.ThreadKindComments, model.ThreadStatusDeleted,
	).First(&th).Error
	if findErr == nil {
		sr.ThreadsExisting++
	} else if errors.Is(findErr, gorm.ErrRecordNotFound) {
		sr.ThreadsToCreate++
	} else {
		return fmt.Errorf("dry-run find thread: %w", findErr)
	}

	for i := range plan.Posts {
		if _, imported := existingMap[plan.Posts[i].OldID]; imported {
			sr.PostsExisting++
		} else {
			sr.PostsToInsert++
			sr.MapRowsToWrite++
		}
	}
	return nil
}

func recomputeThreadCounters(tx *gorm.DB, threadID int64) error {
	var agg struct {
		Posts        int32
		Participants int32
		Highest      int32
		Last         *time.Time
	}
	if err := tx.Model(&model.CommunityPost{}).
		Select("COUNT(*) AS posts, COUNT(DISTINCT author_id) AS participants, "+
			"COALESCE(MAX(post_number),0) AS highest, MAX(created_at) AS last").
		Where("thread_id = ?", threadID).Scan(&agg).Error; err != nil {
		return fmt.Errorf("aggregate counters: %w", err)
	}
	if err := tx.Exec(
		"UPDATE community_thread SET posts_count = ?, participants_count = ?, "+
			"highest_post_number = ?, last_posted_at = ?, updated_at = ? WHERE id = ?",
		agg.Posts, maxInt32(agg.Participants, 1), agg.Highest, agg.Last, agg.Last, threadID,
	).Error; err != nil {
		return fmt.Errorf("update counters: %w", err)
	}
	return nil
}

func seedTrust(tgt *gorm.DB, authors map[int64]bool, apply bool, rep *Report) error {
	ids := make([]int64, 0, len(authors))
	for id := range authors {
		ids = append(ids, id)
	}
	slices.Sort(ids)

	present := make(map[int64]bool)
	for start := 0; start < len(ids); start += trustBatch {
		end := min(start+trustBatch, len(ids))
		var have []int64
		if err := tgt.Model(&model.CommunityTrust{}).
			Where("user_id IN ?", ids[start:end]).Pluck("user_id", &have).Error; err != nil {
			return fmt.Errorf("probe trust: %w", err)
		}
		for _, id := range have {
			present[id] = true
		}
	}

	var absent []int64
	for _, id := range ids {
		if present[id] {
			rep.TrustSeedPresent++
		} else {
			rep.TrustSeedAbsent++
			absent = append(absent, id)
		}
	}
	if !apply || len(absent) == 0 {
		return nil
	}

	now := time.Now()
	const cols = "(user_id, level, first_posts_held_remaining, updated_at)"
	for start := 0; start < len(absent); start += trustBatch {
		end := min(start+trustBatch, len(absent))
		chunk := absent[start:end]
		placeholders := make([]string, 0, len(chunk))
		args := make([]any, 0, len(chunk)*4)
		for _, id := range chunk {
			placeholders = append(placeholders, "(?, ?, ?, ?)")
			args = append(args, id, model.TrustLevelBasic, 0, now)
		}
		sql := "INSERT INTO community_trust " + cols + " VALUES " +
			strings.Join(placeholders, ", ") + " ON CONFLICT (user_id) DO NOTHING"
		if err := tgt.Exec(sql, args...).Error; err != nil {
			return fmt.Errorf("seed trust: %w", err)
		}
	}
	return nil
}

func groupByEntity(rows []srcRow) map[int][]SrcComment {
	out := make(map[int][]SrcComment)
	for i := range rows {
		r := rows[i]
		out[r.EntityID] = append(out[r.EntityID], SrcComment{
			ID:           r.ID,
			EntityID:     r.EntityID,
			UserID:       r.UserID,
			Content:      r.Content,
			ParentID:     r.ParentID,
			TargetUserID: r.TargetUserID,
			Edited:       r.Edited,
			Created:      r.Created,
		})
	}
	return out
}

func loadExistingMap(src *gorm.DB, source string) (map[int]int64, error) {
	out := make(map[int]int64)
	var reg *string
	if err := src.Raw("SELECT to_regclass('resource_comment_community_map')::text").Scan(&reg).Error; err != nil {
		return nil, fmt.Errorf("probe map table: %w", err)
	}
	if reg == nil {
		return out, nil
	}
	var rows []resourceMap
	if err := src.Where("source = ?", source).Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("load map table: %w", err)
	}
	for _, r := range rows {
		out[r.OldID] = r.PostID
	}
	return out, nil
}

func resolvePtr(m map[int]int64, oldID *int) *int64 {
	if oldID == nil {
		return nil
	}
	if v, ok := m[*oldID]; ok {
		return &v
	}
	return nil
}

func tp(t time.Time) *time.Time { return &t }

func maxInt32(a, b int32) int32 {
	if a > b {
		return a
	}
	return b
}
