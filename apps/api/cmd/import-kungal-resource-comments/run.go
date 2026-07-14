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

// trustBatch bounds the multi-row VALUES for the trust seed insert.
const trustBatch = 1000

// run performs the whole three-source import (dry run unless apply). It never
// modifies the community service code or the existing v1 importer; every write
// reuses the community model structs for the target and raw explicit-column SQL
// for counters/trust/map so no DDL default silently wins (charter ruling 14).
func run(src, tgt *gorm.DB, site string, apply bool) (*Report, error) {
	rep := &Report{}

	// The map table lives in the source (forum) database. Create it up-front in
	// apply mode so the per-entity writes can proceed.
	if apply {
		if err := src.Exec(mapTableDDL).Error; err != nil {
			return nil, fmt.Errorf("create map table: %w", err)
		}
	}

	authors := make(map[int64]bool) // distinct comment authors across ALL sources -> trust seed set

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

		// Deterministic entity order keeps the run reproducible and the log readable.
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

// processEntity imports one anchor entity's thread. The target writes (thread +
// posts + counters) run in a single target transaction; the source ledger rows
// and comment_count reset are written after that commit (no cross-database
// transaction exists). Idempotency is recovered on the source side by matching
// the deterministic post_number, so a crash between the two commits never
// double-inserts (charter idempotency + resume clause).
func processEntity(tgt, src *gorm.DB, site string, s sourceSpec, plan ThreadPlan,
	existingMap map[int]int64, apply bool, sr *SourceReport) error {

	anchorID := s.Name + ":" + strconv.Itoa(plan.EntityID)

	// old_id -> community_post.id for THIS thread (seeded from the ledger so a
	// resumed reply can resolve a parent imported in a previous run).
	oldToNew := make(map[int]int64, len(plan.Posts))
	for i := range plan.Posts {
		if pid, ok := existingMap[plan.Posts[i].OldID]; ok {
			oldToNew[plan.Posts[i].OldID] = pid
		}
	}

	// ── dry run: report only, no writes, no transaction ──
	if !apply {
		return dryRunEntity(tgt, site, anchorID, plan, existingMap, sr)
	}

	// newMapRows accumulates ledger rows to write to the source after commit
	// (both freshly inserted posts and crash-reconciled ones).
	type mapRow struct {
		old  int
		post int64
	}
	var newMapRows []mapRow
	var threadID int64

	writeThread := func(tx *gorm.DB) error {
		// ── get-or-create thread by the site-scoped resource anchor (step 01) ──
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
			sr.ThreadsToCreate++
		default:
			return fmt.Errorf("find thread: %w", findErr)
		}
		threadID = th.ID

		// Existing posts of this thread keyed by post_number — the crash-window
		// reconciliation surface (post present in target, ledger row missing).
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
				continue // already in the ledger
			}
			if id, ok := existingByNumber[pp.PostNumber]; ok {
				// Post exists but the ledger row is missing (crash between the two
				// commits): adopt it, backfill the ledger, do NOT re-insert.
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
				TargetUserID:     pp.TargetUserID, // rating verbatim; tree = parent author
				AuthorID:         pp.AuthorID,
				ContentRaw:       pp.Content,
				ContentHTML:      cooked.HTML,
				SanitizerVersion: int32(cooked.Version), // NOT NULL, no default
				ContentRating:    model.ContentRatingAll,
				Status:           model.PostStatusVisible, // no source status column
				EditedAt:         pp.EditedAt,
				CreatedAt:        pp.CreatedAt, // backdate
			}
			if err := tx.Create(&post).Error; err != nil {
				return fmt.Errorf("create post (old_id=%d): %w", pp.OldID, err)
			}
			oldToNew[pp.OldID] = post.ID
			newMapRows = append(newMapRows, mapRow{pp.OldID, post.ID})
			sr.PostsToInsert++
		}

		// Recompute counters from the target so a resumed run converges (ruling 10).
		return recomputeThreadCounters(tx, th.ID)
	}

	if err := tgt.Transaction(writeThread); err != nil {
		return err
	}

	// ── source side (post-commit): ledger rows + optional comment_count reset ──
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
	// Only galgame_website carries a maintained comment_count (ruling 21):
	// reset it to the visible post count. rating/toolset are left untouched.
	if s.CounterTable != "" {
		q := fmt.Sprintf("UPDATE %s SET comment_count = ? WHERE %s = ?", s.CounterTable, s.CounterCol)
		if err := src.Exec(q, plan.VisibleCount, plan.EntityID).Error; err != nil {
			return fmt.Errorf("reset comment_count: %w", err)
		}
	}
	return nil
}

// dryRunEntity tallies what an apply WOULD do, reading the target read-only.
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
// row — a letmoe/v1-active user's trust state must not be touched (charter ruling
// 6; PK is the global user id). ON CONFLICT DO NOTHING enforces that.
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

	// Raw multi-row INSERT with every column listed explicitly. A GORM Create
	// would omit first_posts_held_remaining=0 (its DDL default is 2) and the
	// default would silently win — the zero-value trap this seed must avoid.
	// ON CONFLICT DO NOTHING guarantees an existing row is never mutated.
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

// ── small helpers ──

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
	var reg *string // to_regclass yields NULL when the table is absent
	if err := src.Raw("SELECT to_regclass('resource_comment_community_map')::text").Scan(&reg).Error; err != nil {
		return nil, fmt.Errorf("probe map table: %w", err)
	}
	if reg == nil {
		return out, nil // table absent (dry run before first apply)
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
