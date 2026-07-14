package main

import (
	"sort"
	"time"
)

// SrcComment is one source comment normalized for planning. It is a plain value
// type (no gorm tags) so planEntity stays pure and unit-testable; the DB-facing
// srcRow with its explicit column tags lives in source.go. ParentID is nil for
// the flat source (rating); TargetUserID is set only for rating (copied
// verbatim) — tree replies derive their target from the parent's author.
type SrcComment struct {
	ID           int
	EntityID     int
	UserID       int
	Content      string
	ParentID     *int
	TargetUserID *int
	Edited       *time.Time
	Created      time.Time
}

// PlannedPost is one comment resolved to its in-thread position and pointer
// intent. Pointers are expressed as OLD comment ids (nil = top-level); the
// writer resolves them to community_post ids through the running old->new map.
// TargetUserID is a resolved global user id (not an old comment id).
type PlannedPost struct {
	OldID        int
	PostNumber   int32
	AuthorID     int64
	Content      string
	EditedAt     *time.Time
	CreatedAt    time.Time
	ReplyToOldID *int   // nil = top-level (or orphan-degraded / flat source)
	RootOldID    *int   // nil = top-level (or orphan-degraded / flat source)
	TargetUserID *int64 // rating: verbatim; tree reply: parent author; else nil
	Dangling     bool   // tree parent pointer missing from the set -> degraded to root
}

// ThreadPlan is the complete plan for one anchor entity's comment thread.
// Counters are computed here for the dry-run report; the writer RE-computes them
// from the target after inserts so a resumed run stays correct (charter ruling
// 10). The three sections have no status column, so every post is visible.
type ThreadPlan struct {
	EntityID          int
	Posts             []PlannedPost
	PostsCount        int32
	ParticipantsCount int32
	HighestPostNumber int32
	VisibleCount      int32 // every post is visible; drives galgame_website.comment_count
	FirstAuthorID     int64
	CreatedAt         time.Time // first post created (thread created_at)
	LastPostedAt      time.Time // last post created (thread last_posted_at)
	Dangling          int       // orphan-degraded posts in this thread (tree only)
	SelfTargets       int       // rating self-directed target_user_id (author == target)
	OverLen           int       // posts whose content exceeds spec.MaxRunes
}

// planEntity orders one anchor entity's comments by (created, id) and lays out
// the thread. The (created, id) order guarantees a parent is numbered before its
// children (a reply never predates its parent; ties broken by the auto-increment
// id), so the writer can resolve reply_to/root pointers from an old->new map
// filled incrementally.
//
// Tree sources (website/toolset, charter ruling 19): reply_to = the immediate
// parent, root = the top ancestor derived by walking the parent chain (these
// tables have NO stored root column, unlike v1 galgame_comment), target_user_id
// = the parent post's author. A reply whose parent is absent from the set is
// degraded to a top-level post (content preserved, all three pointers NULL) and
// counted as an anomaly.
//
// Flat source (rating): reply_to/root stay NULL; target_user_id is copied
// verbatim from the source column, including self-directed rows (notification
// suppression is a consumer-side concern).
func planEntity(isTree bool, maxRunes int, comments []SrcComment) ThreadPlan {
	sorted := make([]SrcComment, len(comments))
	copy(sorted, comments)
	sort.Slice(sorted, func(i, j int) bool {
		if !sorted[i].Created.Equal(sorted[j].Created) {
			return sorted[i].Created.Before(sorted[j].Created)
		}
		return sorted[i].ID < sorted[j].ID
	})

	present := make(map[int]bool, len(sorted))
	authorOf := make(map[int]int64, len(sorted))
	for i := range sorted {
		present[sorted[i].ID] = true
		authorOf[sorted[i].ID] = int64(sorted[i].UserID)
	}

	var plan ThreadPlan
	plan.Posts = make([]PlannedPost, 0, len(sorted))
	rootOf := make(map[int]*int, len(sorted)) // resolved RootOldID per planned post
	participants := make(map[int64]bool, len(sorted))

	var num int32
	for i := range sorted {
		c := sorted[i]
		num++
		p := PlannedPost{
			OldID:      c.ID,
			PostNumber: num,
			AuthorID:   int64(c.UserID),
			Content:    c.Content,
			EditedAt:   c.Edited,
			CreatedAt:  c.Created,
		}

		if isTree {
			if c.ParentID != nil {
				if present[*c.ParentID] {
					pid := *c.ParentID
					p.ReplyToOldID = &pid
					if rootOf[pid] != nil {
						// Parent is itself a reply: share the parent's root.
						p.RootOldID = rootOf[pid]
					} else {
						// Parent is a top-level post: it is this reply's root.
						p.RootOldID = &pid
					}
					tgt := authorOf[pid]
					p.TargetUserID = &tgt
				} else {
					// Dangling parent: keep the content, drop the pointers.
					p.Dangling = true
					plan.Dangling++
				}
			}
		} else if c.TargetUserID != nil {
			// Flat (rating): copy the target verbatim, self-target included.
			t := int64(*c.TargetUserID)
			p.TargetUserID = &t
			if t == int64(c.UserID) {
				plan.SelfTargets++
			}
		}

		rootOf[c.ID] = p.RootOldID
		if len([]rune(c.Content)) > maxRunes {
			plan.OverLen++
		}
		participants[p.AuthorID] = true
		plan.Posts = append(plan.Posts, p)
	}

	plan.PostsCount = num
	plan.HighestPostNumber = num
	plan.VisibleCount = num
	plan.ParticipantsCount = int32(len(participants))
	if len(plan.Posts) > 0 {
		plan.EntityID = sorted[0].EntityID
		plan.FirstAuthorID = plan.Posts[0].AuthorID
		plan.CreatedAt = plan.Posts[0].CreatedAt
		plan.LastPostedAt = plan.Posts[len(plan.Posts)-1].CreatedAt
	}
	return plan
}
