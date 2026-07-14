package main

import (
	"testing"
	"time"
)

func ptr(i int) *int { return &i }

func ts(sec int) time.Time { return time.Date(2024, 1, 1, 0, 0, sec, 0, time.UTC) }

// mkComment is a terse fixture builder for the planner tests.
func mkComment(id, gid, uid int, parent, root *int, status int16, sec int) SrcComment {
	return SrcComment{
		ID: id, GalgameID: gid, UserID: uid, Content: "c",
		ParentCommentID: parent, RootCommentID: root, Status: status, Created: ts(sec),
	}
}

// TestPlanOrderingAndNumbering: posts are ordered by (created, id) and numbered
// 1..N; a parent is always numbered before its children.
func TestPlanOrderingAndNumbering(t *testing.T) {
	// Deliberately shuffled input; two share a created ts (tie broken by id).
	in := []SrcComment{
		mkComment(4, 1, 10, ptr(1), ptr(1), 0, 3),
		mkComment(1, 1, 10, nil, nil, 0, 1),
		mkComment(3, 1, 11, nil, nil, 0, 2), // same ts as id=2 below
		mkComment(2, 1, 12, nil, nil, 0, 2),
	}
	p := planThread(in)
	if p.PostsCount != 4 || p.HighestPostNumber != 4 {
		t.Fatalf("counts: posts=%d highest=%d", p.PostsCount, p.HighestPostNumber)
	}
	wantOrder := []int{1, 2, 3, 4} // id=2 before id=3 (tie on ts broken by id)
	for i, pp := range p.Posts {
		if pp.OldID != wantOrder[i] {
			t.Fatalf("post %d: oldID=%d want %d", i, pp.OldID, wantOrder[i])
		}
		if pp.PostNumber != int32(i+1) {
			t.Fatalf("post %d: number=%d want %d", i, pp.PostNumber, i+1)
		}
	}
	if p.FirstAuthorID != 10 || !p.CreatedAt.Equal(ts(1)) || !p.LastPostedAt.Equal(ts(3)) {
		t.Fatalf("thread meta: author=%d created=%v last=%v", p.FirstAuthorID, p.CreatedAt, p.LastPostedAt)
	}
}

// TestPlanPointerMappingReplyOfReply: the 52-case shape — a reply whose parent
// is itself a reply. reply_to points at the immediate parent; root points at the
// top-level ancestor (root_comment_id), NOT the parent.
func TestPlanPointerMappingReplyOfReply(t *testing.T) {
	in := []SrcComment{
		mkComment(1, 1, 10, nil, nil, 0, 1),       // top-level
		mkComment(2, 1, 11, ptr(1), ptr(1), 0, 2), // reply to 1
		mkComment(3, 1, 12, ptr(2), ptr(1), 0, 3), // reply to 2 (parent is a reply)
	}
	p := planThread(in)
	byOld := indexByOld(p.Posts)

	if byOld[1].ReplyToOldID != nil || byOld[1].RootOldID != nil {
		t.Fatalf("top-level should have nil pointers, got reply=%v root=%v", byOld[1].ReplyToOldID, byOld[1].RootOldID)
	}
	if got := byOld[2]; deref(got.ReplyToOldID) != 1 || deref(got.RootOldID) != 1 {
		t.Fatalf("post 2: reply=%v root=%v want 1/1", got.ReplyToOldID, got.RootOldID)
	}
	if got := byOld[3]; deref(got.ReplyToOldID) != 2 || deref(got.RootOldID) != 1 {
		t.Fatalf("post 3 (reply-of-reply): reply=%v root=%v want 2/1", got.ReplyToOldID, got.RootOldID)
	}
}

// TestPlanRootFallbackFromParentChain: when root_comment_id is NULL/missing, the
// planner derives the root from the parent chain (equal to root_comment_id in
// clean data; this guards a corrupt set).
func TestPlanRootFallbackFromParentChain(t *testing.T) {
	in := []SrcComment{
		mkComment(1, 1, 10, nil, nil, 0, 1),
		mkComment(2, 1, 11, ptr(1), nil, 0, 2), // reply, root_comment_id NULL
		mkComment(3, 1, 12, ptr(2), nil, 0, 3), // reply-of-reply, root NULL
	}
	p := planThread(in)
	byOld := indexByOld(p.Posts)
	if deref(byOld[2].RootOldID) != 1 {
		t.Fatalf("post 2 root fallback: got %v want 1", byOld[2].RootOldID)
	}
	if deref(byOld[3].RootOldID) != 1 {
		t.Fatalf("post 3 root fallback: got %v want 1", byOld[3].RootOldID)
	}
}

// TestPlanDanglingDegradesToRoot: a reply whose parent is absent from the set is
// degraded to a top-level post (content kept, pointers NULL) and counted.
func TestPlanDanglingDegradesToRoot(t *testing.T) {
	in := []SrcComment{
		mkComment(1, 1, 10, nil, nil, 0, 1),
		mkComment(3, 1, 11, ptr(99), ptr(99), 0, 2), // parent 99 not present
	}
	p := planThread(in)
	if p.Dangling != 1 {
		t.Fatalf("dangling count=%d want 1", p.Dangling)
	}
	byOld := indexByOld(p.Posts)
	if !byOld[3].Dangling || byOld[3].ReplyToOldID != nil || byOld[3].RootOldID != nil {
		t.Fatalf("post 3 should be degraded to root, got dangling=%v reply=%v root=%v",
			byOld[3].Dangling, byOld[3].ReplyToOldID, byOld[3].RootOldID)
	}
}

// TestPlanCountersAndAnomalies: participants distinct, visible vs hidden, status1
// and over-length tallies.
func TestPlanCountersAndAnomalies(t *testing.T) {
	long := make([]rune, maxContentRunes+1)
	for i := range long {
		long[i] = 'x'
	}
	in := []SrcComment{
		mkComment(1, 1, 10, nil, nil, 0, 1),
		mkComment(2, 1, 10, nil, nil, 1, 2), // hidden, same author (participants=2 total)
		mkComment(3, 1, 11, nil, nil, 0, 3),
	}
	in[2].Content = string(long) // over length
	p := planThread(in)
	if p.ParticipantsCount != 2 {
		t.Fatalf("participants=%d want 2", p.ParticipantsCount)
	}
	if p.VisibleCount != 2 { // c2 hidden
		t.Fatalf("visible=%d want 2", p.VisibleCount)
	}
	if p.Status1 != 1 {
		t.Fatalf("status1=%d want 1", p.Status1)
	}
	if p.OverLen != 1 {
		t.Fatalf("overlen=%d want 1", p.OverLen)
	}
}

func indexByOld(posts []PlannedPost) map[int]PlannedPost {
	m := make(map[int]PlannedPost, len(posts))
	for _, p := range posts {
		m[p.OldID] = p
	}
	return m
}

func deref(p *int) int {
	if p == nil {
		return -1
	}
	return *p
}
