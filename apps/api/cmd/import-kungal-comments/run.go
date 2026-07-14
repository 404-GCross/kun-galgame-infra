package main

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"api/internal/platform/community/model"
	"api/internal/platform/community/sanitize"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// trustBatch bounds the multi-row VALUES for the trust seed insert.
const trustBatch = 1000

// run performs the whole import (dry run unless apply). It never modifies the
// community service code; every write reuses the community model structs for the
// target and raw explicit-column SQL for counters/reactions/trust/map so no DDL
// default silently wins (charter ruling 14).
func run(src, tgt *gorm.DB, site string, apply bool) (*Report, error) {
	rep := &Report{}

	// Load the full source set (tiny: ~9.4k comments, ~1.2k likes).
	var comments []srcComment
	if err := src.Order("created ASC, id ASC").Find(&comments).Error; err != nil {
		return nil, fmt.Errorf("load galgame_comment: %w", err)
	}
	var likes []srcLike
	if err := src.Find(&likes).Error; err != nil {
		return nil, fmt.Errorf("load galgame_comment_like: %w", err)
	}

	byGame := groupByGame(comments)
	likesByComment := groupLikes(likes)

	// Source-orphan likes: a like referencing a comment that no longer exists in
	// the source (would never map to a post). Counted once, up front.
	commentIDs := make(map[int]bool, len(comments))
	for i := range comments {
		commentIDs[comments[i].ID] = true
	}
	for i := range likes {
		if !commentIDs[likes[i].GalgameCommentID] {
			rep.OrphanLikes++
		}
	}

	// Existing id map (old_comment_id -> post_id) from the ledger, if it exists.
	// A missing table (first run) yields an empty map. In apply mode the table is
	// created up-front so writes can proceed.
	if apply {
		if err := src.Exec(mapTableDDL).Error; err != nil {
			return nil, fmt.Errorf("create map table: %w", err)
		}
	}
	existingMap, err := loadExistingMap(src)
	if err != nil {
		return nil, err
	}

	// Deterministic game order keeps the run reproducible and the log readable.
	gameIDs := make([]int, 0, len(byGame))
	for gid := range byGame {
		gameIDs = append(gameIDs, gid)
	}
	sort.Ints(gameIDs)
	rep.GamesTotal = len(gameIDs)

	authors := make(map[int64]bool) // distinct comment authors -> trust seed set

	for _, gid := range gameIDs {
		plan := planThread(byGame[gid])
		for i := range plan.Posts {
			authors[plan.Posts[i].AuthorID] = true
		}
		rep.DanglingParents += plan.Dangling
		rep.Status1Rows += plan.Status1
		rep.OverLenRows += plan.OverLen

		if err := processGame(tgt, src, site, plan, likesByComment, existingMap, apply, rep); err != nil {
			return nil, fmt.Errorf("game %d: %w", gid, err)
		}
	}

	if err := seedTrust(tgt, authors, apply, rep); err != nil {
		return nil, err
	}
	return rep, nil
}

// processGame imports one game's thread. The target writes (thread + posts +
// reactions + counters) run in a single target transaction; the source ledger
// rows and comment_count reset are written after that commit (no cross-database
// transaction exists). Idempotency is recovered on the source side by matching
// deterministic post_number, so a crash between the two commits never double-
// inserts (charter idempotency + resume clause).
func processGame(tgt, src *gorm.DB, site string, plan ThreadPlan, likesByComment map[int][]srcLike,
	existingMap map[int]int64, apply bool, rep *Report) error {

	anchorID := strconv.Itoa(plan.GalgameID)

	// old_comment_id -> community_post.id for THIS thread (seeded from the ledger
	// so a resumed reply can resolve a parent imported in a previous run).
	oldToNew := make(map[int]int64, len(plan.Posts))
	for i := range plan.Posts {
		if pid, ok := existingMap[plan.Posts[i].OldID]; ok {
			oldToNew[plan.Posts[i].OldID] = pid
		}
	}

	// newMapRows accumulates ledger rows to write to the source after commit
	// (both freshly inserted posts and crash-reconciled ones).
	type mapRow struct {
		old, gid int
		post     int64
	}
	var newMapRows []mapRow
	var threadID int64

	writeThread := func(tx *gorm.DB) error {
		// ── get-or-create thread by the site-scoped comments anchor (step 01) ──
		var th model.CommunityThread
		findErr := tx.Where(
			"site = ? AND anchor_kind = ? AND anchor_id = ? AND kind = ? AND status <> ?",
			site, model.AnchorKindSiteGame, anchorID, model.ThreadKindComments, model.ThreadStatusDeleted,
		).First(&th).Error

		switch {
		case findErr == nil:
			rep.ThreadsExisting++
		case errors.Is(findErr, gorm.ErrRecordNotFound):
			th = model.CommunityThread{
				Site:              site,
				Kind:              model.ThreadKindComments,
				AnchorKind:        model.AnchorKindSiteGame,
				AnchorID:          anchorID,
				Title:             nil,
				ContentRating:     model.ContentRatingAll,
				Status:            model.ThreadStatusOpen,
				PostsCount:        plan.PostsCount,        // explicit (ruling 14)
				ParticipantsCount: plan.ParticipantsCount, // DDL default 1 -> explicit
				HighestPostNumber: plan.HighestPostNumber, // explicit
				LastPostedAt:      tp(plan.LastPostedAt),
				CreatedBy:         plan.FirstAuthorID,
				CreatedAt:         plan.CreatedAt,    // backdate to first comment
				UpdatedAt:         plan.LastPostedAt, // backdate to last activity
			}
			if err := tx.Create(&th).Error; err != nil {
				return fmt.Errorf("create thread: %w", err)
			}
			rep.ThreadsToCreate++
		default:
			return fmt.Errorf("find thread: %w", findErr)
		}
		threadID = th.ID

		// Existing posts of this thread keyed by post_number — the crash-window
		// reconciliation surface (post present in target, ledger row missing).
		existingByNumber := make(map[int32]int64)
		{
			var rows []struct {
				PostNumber int32
				ID         int64
			}
			if err := tx.Model(&model.CommunityPost{}).
				Select("post_number, id").Where("thread_id = ?", th.ID).
				Find(&rows).Error; err != nil {
				return fmt.Errorf("load existing posts: %w", err)
			}
			for _, r := range rows {
				existingByNumber[r.PostNumber] = r.ID
			}
		}

		for i := range plan.Posts {
			pp := plan.Posts[i]
			if _, done := oldToNew[pp.OldID]; done {
				rep.PostsExisting++
				continue // already in the ledger
			}
			if id, ok := existingByNumber[pp.PostNumber]; ok {
				// Post exists but the ledger row is missing (crash between the two
				// commits): adopt it, backfill the ledger, do NOT re-insert.
				oldToNew[pp.OldID] = id
				newMapRows = append(newMapRows, mapRow{pp.OldID, plan.GalgameID, id})
				rep.PostsExisting++
				continue
			}
			cooked := sanitize.Cook(pp.Content)
			post := model.CommunityPost{
				ThreadID:         th.ID,
				PostNumber:       pp.PostNumber,
				RootPostID:       resolvePtr(oldToNew, pp.RootOldID),
				ReplyToPostID:    resolvePtr(oldToNew, pp.ReplyToOldID),
				TargetUserID:     nil,
				AuthorID:         pp.AuthorID,
				ContentRaw:       pp.Content,
				ContentHTML:      cooked.HTML,
				SanitizerVersion: int32(cooked.Version), // NOT NULL, no default
				ContentRating:    model.ContentRatingAll,
				Status:           pp.Status,
				EditedAt:         pp.EditedAt,
				CreatedAt:        pp.CreatedAt, // backdate
			}
			if err := tx.Create(&post).Error; err != nil {
				return fmt.Errorf("create post (old_comment_id=%d): %w", pp.OldID, err)
			}
			oldToNew[pp.OldID] = post.ID
			newMapRows = append(newMapRows, mapRow{pp.OldID, plan.GalgameID, post.ID})
			rep.PostsToInsert++
		}

		// Reactions: one per like whose target post is known. Composite-PK
		// conflict is skipped (idempotent); a missing target post is an anomaly.
		for i := range plan.Posts {
			for _, lk := range likesByComment[plan.Posts[i].OldID] {
				postID, ok := oldToNew[lk.GalgameCommentID]
				if !ok {
					rep.ReactionsSkipped++
					continue
				}
				res := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&model.CommunityReaction{
					PostID:    postID,
					UserID:    int64(lk.UserID),
					Kind:      model.ReactionKindLike,
					CreatedAt: lk.Created, // backdate
				})
				if res.Error != nil {
					return fmt.Errorf("create reaction: %w", res.Error)
				}
				rep.ReactionsToInsert += int(res.RowsAffected)
			}
		}

		// Recompute counters from the target so a resumed run converges (ruling 10).
		return recomputeThreadCounters(tx, th.ID)
	}

	// ── dry run: report only, no writes, no transaction ──
	if !apply {
		return dryRunGame(tgt, site, anchorID, plan, likesByComment, existingMap, rep)
	}

	if err := tgt.Transaction(writeThread); err != nil {
		return err
	}

	// ── source side (post-commit): ledger rows + comment_count reset ──
	if len(newMapRows) > 0 {
		rows := make([]commentMap, 0, len(newMapRows))
		for _, m := range newMapRows {
			rows = append(rows, commentMap{OldCommentID: m.old, ThreadID: threadID, PostID: m.post, GalgameID: m.gid})
		}
		if err := src.Clauses(clause.OnConflict{DoNothing: true}).CreateInBatches(rows, 500).Error; err != nil {
			return fmt.Errorf("write ledger rows: %w", err)
		}
		rep.MapRowsToWrite += len(rows)
	}
	// comment_count = visible post count (charter ruling 11: display counter).
	if err := src.Exec("UPDATE galgame SET comment_count = ? WHERE id = ?", plan.VisibleCount, plan.GalgameID).Error; err != nil {
		return fmt.Errorf("reset comment_count: %w", err)
	}
	return nil
}

// dryRunGame tallies what an apply WOULD do, reading the target read-only.
func dryRunGame(tgt *gorm.DB, site, anchorID string, plan ThreadPlan,
	likesByComment map[int][]srcLike, existingMap map[int]int64, rep *Report) error {

	var th model.CommunityThread
	findErr := tgt.Where(
		"site = ? AND anchor_kind = ? AND anchor_id = ? AND kind = ? AND status <> ?",
		site, model.AnchorKindSiteGame, anchorID, model.ThreadKindComments, model.ThreadStatusDeleted,
	).First(&th).Error
	if findErr == nil {
		rep.ThreadsExisting++
	} else if errors.Is(findErr, gorm.ErrRecordNotFound) {
		rep.ThreadsToCreate++
	} else {
		return fmt.Errorf("dry-run find thread: %w", findErr)
	}

	for i := range plan.Posts {
		old := plan.Posts[i].OldID
		imported := false
		if _, ok := existingMap[old]; ok {
			imported = true
		}
		if imported {
			rep.PostsExisting++
		} else {
			rep.PostsToInsert++
			rep.MapRowsToWrite++
		}
		for _, lk := range likesByComment[old] {
			_ = lk
			// A like becomes a reaction unless already imported would dedupe it;
			// in dry-run we count each like as a would-insert (target is empty on a
			// fresh run; resume re-count is harmless — it never writes).
			rep.ReactionsToInsert++
		}
	}
	return nil
}

// recomputeThreadCounters overwrites the four denormalized counters from the
// authoritative post rows (idempotent under resume).
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

// seedTrust inserts a community_trust row for every distinct comment author that
// has none (level=1, first_posts_held_remaining=0). It NEVER updates an existing
// row — a letmoe-active user's trust state must not be touched (charter ruling 6;
// PK is the global user id). ON CONFLICT DO NOTHING enforces that.
func seedTrust(tgt *gorm.DB, authors map[int64]bool, apply bool, rep *Report) error {
	ids := make([]int64, 0, len(authors))
	for id := range authors {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })

	present := make(map[int64]bool)
	for start := 0; start < len(ids); start += trustBatch {
		end := start + trustBatch
		if end > len(ids) {
			end = len(ids)
		}
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

	// Raw multi-row INSERT with every column listed explicitly. A GORM Create
	// would omit first_posts_held_remaining=0 (its DDL default is 2) and the
	// default would silently win — the zero-value trap this seed must avoid.
	// ON CONFLICT DO NOTHING guarantees an existing row is never mutated.
	now := time.Now()
	const cols = "(user_id, level, first_posts_held_remaining, updated_at)"
	for start := 0; start < len(absent); start += trustBatch {
		end := start + trustBatch
		if end > len(absent) {
			end = len(absent)
		}
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

// ── small helpers ──

func groupByGame(comments []srcComment) map[int][]SrcComment {
	out := make(map[int][]SrcComment)
	for i := range comments {
		c := comments[i]
		out[c.GalgameID] = append(out[c.GalgameID], SrcComment{
			ID:              c.ID,
			GalgameID:       c.GalgameID,
			UserID:          c.UserID,
			Content:         c.Content,
			ParentCommentID: c.ParentCommentID,
			RootCommentID:   c.RootCommentID,
			Status:          c.Status,
			Edited:          c.Edited,
			Created:         c.Created,
		})
	}
	return out
}

func groupLikes(likes []srcLike) map[int][]srcLike {
	out := make(map[int][]srcLike)
	for i := range likes {
		out[likes[i].GalgameCommentID] = append(out[likes[i].GalgameCommentID], likes[i])
	}
	return out
}

func loadExistingMap(src *gorm.DB) (map[int]int64, error) {
	out := make(map[int]int64)
	var reg *string // to_regclass yields NULL when the table is absent
	if err := src.Raw("SELECT to_regclass('galgame_comment_community_map')::text").Scan(&reg).Error; err != nil {
		return nil, fmt.Errorf("probe map table: %w", err)
	}
	if reg == nil {
		return out, nil // table absent (dry run before first apply)
	}
	var rows []commentMap
	if err := src.Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("load map table: %w", err)
	}
	for _, r := range rows {
		out[r.OldCommentID] = r.PostID
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
