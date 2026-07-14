package main

import (
	"sort"
	"time"
)

// maxContentRunes is the source's declared markdown ceiling. Rows above it are
// still imported verbatim (content is never truncated); the overage is only
// counted as an anomaly for the report.
const maxContentRunes = 5000

// SrcComment is one galgame_comment row loaded for planning. It is a plain value
// type (no gorm tags) so planThread stays pure and unit-testable; the DB-facing
// ad-hoc struct with its explicit column tags lives in source.go.
type SrcComment struct {
	ID              int
	GalgameID       int
	UserID          int
	Content         string
	ParentCommentID *int
	RootCommentID   *int
	Status          int16
	Edited          *time.Time
	Created         time.Time
}

// PlannedPost is one comment resolved to its in-thread position and pointer
// intent. Pointers are expressed as OLD comment ids (nil = top-level); the
// writer resolves them to community_post ids through the running old->new map.
type PlannedPost struct {
	OldID        int
	PostNumber   int32
	AuthorID     int64
	Content      string
	Status       int16
	EditedAt     *time.Time
	CreatedAt    time.Time
	ReplyToOldID *int // nil = top-level (or orphan-degraded)
	RootOldID    *int // nil = top-level (or orphan-degraded)
	Dangling     bool // parent pointer missing from the set -> degraded to root
}

// ThreadPlan is the complete plan for one galgame_id's comment thread. Counters
// are computed here for the dry-run report; the writer RE-computes them from the
// target after inserts so a resumed run stays correct (charter ruling 10/14).
type ThreadPlan struct {
	GalgameID         int
	Posts             []PlannedPost
	PostsCount        int32
	ParticipantsCount int32
	HighestPostNumber int32
	VisibleCount      int32 // status = visible; drives galgame.comment_count
	FirstAuthorID     int64
	CreatedAt         time.Time // first post created (thread created_at)
	LastPostedAt      time.Time // last post created (thread last_posted_at)
	Dangling          int       // orphan-degraded posts in this thread
	OverLen           int       // posts whose content exceeds maxContentRunes
	Status1           int       // source status=1 (hidden) rows in this thread
}

// planThread orders a game's comments by (created, id) and lays out the thread.
//
// The (created, id) order guarantees a parent is numbered before its children:
// a reply never predates its parent, and — ties on created broken by id — a
// reply's auto-increment id always exceeds its parent's. So the writer can
// resolve reply_to/root pointers from an old->new map filled incrementally.
//
// Pointer rules (charter ruling 10): reply_to_post_id = map[parent_comment_id],
// root_post_id = map[root_comment_id]; a top-level post leaves both NULL. A
// reply whose parent is absent from the set is degraded to a top-level post
// (content preserved, both pointers NULL) and counted as an anomaly. root_post_id
// prefers root_comment_id but falls back to the parent-chain root when the
// stored root is absent — the two agree in clean data (verified), the fallback
// only guards a corrupt/partial set.
func planThread(comments []SrcComment) ThreadPlan {
	sorted := make([]SrcComment, len(comments))
	copy(sorted, comments)
	sort.Slice(sorted, func(i, j int) bool {
		if !sorted[i].Created.Equal(sorted[j].Created) {
			return sorted[i].Created.Before(sorted[j].Created)
		}
		return sorted[i].ID < sorted[j].ID
	})

	present := make(map[int]bool, len(sorted))
	for i := range sorted {
		present[sorted[i].ID] = true
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
			Status:     c.Status,
			EditedAt:   c.Edited,
			CreatedAt:  c.Created,
		}

		if c.ParentCommentID != nil {
			if present[*c.ParentCommentID] {
				pid := *c.ParentCommentID
				p.ReplyToOldID = &pid
				switch {
				case c.RootCommentID != nil && present[*c.RootCommentID]:
					rc := *c.RootCommentID
					p.RootOldID = &rc
				case rootOf[pid] != nil:
					// Parent is itself a reply: share the parent's root.
					p.RootOldID = rootOf[pid]
				default:
					// Parent is a top-level post: it is this reply's root.
					p.RootOldID = &pid
				}
			} else {
				// Dangling parent: keep the content, drop the pointers.
				p.Dangling = true
				plan.Dangling++
			}
		}

		rootOf[c.ID] = p.RootOldID
		if c.Status == 1 {
			plan.Status1++
		}
		if len([]rune(c.Content)) > maxContentRunes {
			plan.OverLen++
		}
		if c.Status == 0 {
			plan.VisibleCount++
		}
		participants[p.AuthorID] = true
		plan.Posts = append(plan.Posts, p)
	}

	plan.PostsCount = num
	plan.HighestPostNumber = num
	plan.ParticipantsCount = int32(len(participants))
	if len(plan.Posts) > 0 {
		plan.GalgameID = sorted[0].GalgameID
		plan.FirstAuthorID = plan.Posts[0].AuthorID
		plan.CreatedAt = plan.Posts[0].CreatedAt
		plan.LastPostedAt = plan.Posts[len(plan.Posts)-1].CreatedAt
	}
	return plan
}
