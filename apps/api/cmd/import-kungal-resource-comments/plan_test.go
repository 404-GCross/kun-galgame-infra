package main

import (
	"testing"
	"time"
)

func ptr(i int) *int { return &i }

func ts(sec int) time.Time { return time.Date(2024, 1, 1, 0, 0, sec, 0, time.UTC) }

// mkTree builds a website/toolset-shaped (self-referential) comment.
func mkTree(id, eid, uid int, parent *int, sec int) SrcComment {
	return SrcComment{ID: id, EntityID: eid, UserID: uid, Content: "c", ParentID: parent, Created: ts(sec)}
}

// mkFlat builds a rating-shaped (flat) comment carrying a verbatim target.
func mkFlat(id, eid, uid int, target *int, sec int) SrcComment {
	var t *int
	if target != nil {
		v := *target
		t = &v
	}
	return SrcComment{ID: id, EntityID: eid, UserID: uid, Content: "c", TargetUserID: t, Created: ts(sec)}
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

func deref64(p *int64) int64 {
	if p == nil {
		return -1
	}
	return *p
}

// TestTreeOrderingAndNumbering: posts ordered by (created, id), numbered 1..N,
// a parent always numbered before its children; tie on created broken by id.
func TestTreeOrderingAndNumbering(t *testing.T) {
	in := []SrcComment{
		mkTree(4, 1, 10, ptr(1), 3),
		mkTree(1, 1, 10, nil, 1),
		mkTree(3, 1, 11, nil, 2), // same ts as id=2 below
		mkTree(2, 1, 12, nil, 2),
	}
	p := planEntity(true, 5000, in)
	if p.PostsCount != 4 || p.HighestPostNumber != 4 || p.VisibleCount != 4 {
		t.Fatalf("counts: posts=%d highest=%d visible=%d", p.PostsCount, p.HighestPostNumber, p.VisibleCount)
	}
	wantOrder := []int{1, 2, 3, 4} // id=2 before id=3 (tie on ts broken by id)
	for i, pp := range p.Posts {
		if pp.OldID != wantOrder[i] || pp.PostNumber != int32(i+1) {
			t.Fatalf("post %d: oldID=%d number=%d want %d/%d", i, pp.OldID, pp.PostNumber, wantOrder[i], i+1)
		}
	}
	if p.FirstAuthorID != 10 || !p.CreatedAt.Equal(ts(1)) || !p.LastPostedAt.Equal(ts(3)) {
		t.Fatalf("thread meta: author=%d created=%v last=%v", p.FirstAuthorID, p.CreatedAt, p.LastPostedAt)
	}
}

// TestTreeReplyOfReplyReRoot: reply_to points at the immediate parent, root at
// the top-level ancestor, target_user_id at the parent post's author.
func TestTreeReplyOfReplyReRoot(t *testing.T) {
	in := []SrcComment{
		mkTree(1, 1, 10, nil, 1),    // top-level
		mkTree(2, 1, 11, ptr(1), 2), // reply to 1
		mkTree(3, 1, 12, ptr(2), 3), // reply to 2 (parent is a reply)
	}
	p := planEntity(true, 5000, in)
	byOld := indexByOld(p.Posts)

	if byOld[1].ReplyToOldID != nil || byOld[1].RootOldID != nil || byOld[1].TargetUserID != nil {
		t.Fatalf("top-level should have nil pointers/target, got %+v", byOld[1])
	}
	if got := byOld[2]; deref(got.ReplyToOldID) != 1 || deref(got.RootOldID) != 1 || deref64(got.TargetUserID) != 10 {
		t.Fatalf("post 2: reply=%v root=%v target=%v want 1/1/10", got.ReplyToOldID, got.RootOldID, got.TargetUserID)
	}
	if got := byOld[3]; deref(got.ReplyToOldID) != 2 || deref(got.RootOldID) != 1 || deref64(got.TargetUserID) != 11 {
		t.Fatalf("post 3 (reply-of-reply): reply=%v root=%v target=%v want 2/1/11", got.ReplyToOldID, got.RootOldID, got.TargetUserID)
	}
}

// TestTreeDeepChainReRoot: a real-data depth-4 chain (website max is 4, toolset
// 5). Every non-root descendant re-roots to the SAME top ancestor; each reply_to
// is its immediate parent; each target is the parent's author.
func TestTreeDeepChainReRoot(t *testing.T) {
	in := []SrcComment{
		mkTree(1, 7, 10, nil, 1),    // depth 1 (root)
		mkTree(2, 7, 11, ptr(1), 2), // depth 2
		mkTree(3, 7, 12, ptr(2), 3), // depth 3
		mkTree(4, 7, 13, ptr(3), 4), // depth 4
	}
	p := planEntity(true, 5000, in)
	byOld := indexByOld(p.Posts)
	for _, id := range []int{2, 3, 4} {
		if deref(byOld[id].RootOldID) != 1 {
			t.Fatalf("post %d root=%v want 1 (top ancestor)", id, byOld[id].RootOldID)
		}
	}
	if deref(byOld[4].ReplyToOldID) != 3 || deref64(byOld[4].TargetUserID) != 12 {
		t.Fatalf("post 4: reply=%v target=%v want 3/12", byOld[4].ReplyToOldID, byOld[4].TargetUserID)
	}
	if deref(byOld[3].ReplyToOldID) != 2 || deref64(byOld[3].TargetUserID) != 11 {
		t.Fatalf("post 3: reply=%v target=%v want 2/11", byOld[3].ReplyToOldID, byOld[3].TargetUserID)
	}
}

// TestTreeDanglingDegradesToRoot: a reply whose parent is absent is degraded to
// a top-level post (content kept, all pointers/target NULL) and counted.
func TestTreeDanglingDegradesToRoot(t *testing.T) {
	in := []SrcComment{
		mkTree(1, 1, 10, nil, 1),
		mkTree(3, 1, 11, ptr(99), 2), // parent 99 not present
	}
	p := planEntity(true, 5000, in)
	if p.Dangling != 1 {
		t.Fatalf("dangling count=%d want 1", p.Dangling)
	}
	byOld := indexByOld(p.Posts)
	got := byOld[3]
	if !got.Dangling || got.ReplyToOldID != nil || got.RootOldID != nil || got.TargetUserID != nil {
		t.Fatalf("post 3 should be degraded to root, got %+v", got)
	}
}

// TestFlatTargetVerbatim: rating is flat — no reply/root pointers; target copied
// verbatim including self-directed rows, which are tallied.
func TestFlatTargetVerbatim(t *testing.T) {
	in := []SrcComment{
		mkFlat(1, 1, 10, ptr(20), 1), // target another user
		mkFlat(2, 1, 11, ptr(11), 2), // self-target
		mkFlat(3, 1, 12, nil, 3),     // no target
	}
	p := planEntity(false, 1314, in)
	if p.SelfTargets != 1 {
		t.Fatalf("selfTargets=%d want 1", p.SelfTargets)
	}
	byOld := indexByOld(p.Posts)
	for _, id := range []int{1, 2, 3} {
		if byOld[id].ReplyToOldID != nil || byOld[id].RootOldID != nil {
			t.Fatalf("flat post %d must have nil reply/root, got %+v", id, byOld[id])
		}
	}
	if deref64(byOld[1].TargetUserID) != 20 || deref64(byOld[2].TargetUserID) != 11 || byOld[3].TargetUserID != nil {
		t.Fatalf("targets: p1=%v p2=%v p3=%v want 20/11/nil", byOld[1].TargetUserID, byOld[2].TargetUserID, byOld[3].TargetUserID)
	}
}

// TestCountersAndOverLen: participants distinct, over-length tally against the
// section ceiling (content is never truncated).
func TestCountersAndOverLen(t *testing.T) {
	long := make([]rune, 1315)
	for i := range long {
		long[i] = 'x'
	}
	in := []SrcComment{
		mkFlat(1, 1, 10, nil, 1),
		mkFlat(2, 1, 10, nil, 2), // same author -> participants stays counting distinct
		mkFlat(3, 1, 11, nil, 3),
	}
	in[2].Content = string(long) // over the 1314 ceiling
	p := planEntity(false, 1314, in)
	if p.ParticipantsCount != 2 {
		t.Fatalf("participants=%d want 2", p.ParticipantsCount)
	}
	if p.OverLen != 1 {
		t.Fatalf("overlen=%d want 1", p.OverLen)
	}
}
