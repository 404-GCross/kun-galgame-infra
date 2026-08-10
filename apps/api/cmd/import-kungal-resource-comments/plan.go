package main

import (
	"sort"
	"time"
)

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

type PlannedPost struct {
	OldID        int
	PostNumber   int32
	AuthorID     int64
	Content      string
	EditedAt     *time.Time
	CreatedAt    time.Time
	ReplyToOldID *int
	RootOldID    *int
	TargetUserID *int64
	Dangling     bool
}

type ThreadPlan struct {
	EntityID          int
	Posts             []PlannedPost
	PostsCount        int32
	ParticipantsCount int32
	HighestPostNumber int32
	VisibleCount      int32
	FirstAuthorID     int64
	CreatedAt         time.Time
	LastPostedAt      time.Time
	Dangling          int
	SelfTargets       int
	OverLen           int
}

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
	rootOf := make(map[int]*int, len(sorted))
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
						p.RootOldID = rootOf[pid]
					} else {
						p.RootOldID = &pid
					}
					tgt := authorOf[pid]
					p.TargetUserID = &tgt
				} else {
					p.Dangling = true
					plan.Dangling++
				}
			}
		} else if c.TargetUserID != nil {
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
