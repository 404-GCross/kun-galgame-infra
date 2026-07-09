package service

import (
	"context"
	"slices"
	"sync"
	"testing"

	"api/internal/platform/community/model"
)

// TestConcurrent_CommentsGetOrCreate races many callers to get-or-create the
// SAME anchor's comments thread: exactly one row is created and everyone gets
// the same id (invariant 4 under concurrency — the partial unique + re-read).
func TestConcurrent_CommentsGetOrCreate(t *testing.T) {
	cleanTables(t)
	ts := NewThreadService(testDB, NoopSink{})
	const n = 8

	ids := make([]int64, n)
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			th, err := ts.GetOrCreateCommentsThread(context.Background(), CommentsThreadParams{
				Site: "letmoe", AnchorKind: model.AnchorKindSiteGame, AnchorID: "race", ContentRating: 0, ActorID: int64(i),
			})
			if err != nil {
				errs[i] = err
				return
			}
			ids[i] = th.ID
		}(i)
	}
	wg.Wait()

	for i := range n {
		if errs[i] != nil {
			t.Fatalf("goroutine %d: %v", i, errs[i])
		}
		if ids[i] == 0 || ids[i] != ids[0] {
			t.Fatalf("all callers must get the same thread id: %v", ids)
		}
	}
	var rows int64
	testDB.Model(&model.CommunityThread{}).
		Where("anchor_kind = ? AND anchor_id = ?", model.AnchorKindSiteGame, "race").Count(&rows)
	if rows != 1 {
		t.Fatalf("exactly one comments thread should exist, got %d", rows)
	}
}

// TestConcurrent_ReplyPostNumbers races many replies on one thread: the
// row-locked allocation makes post_number gap-free and dup-free (invariant 5
// under concurrency).
func TestConcurrent_ReplyPostNumbers(t *testing.T) {
	cleanTables(t)
	ts := NewThreadService(testDB, NoopSink{})
	ps := NewPostService(testDB, NoopSink{})
	th := openTopic(t, ts, "letmoe", 100, "b1", "opening") // post #1
	const n = 10

	nums := make([]int32, n)
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			// Distinct authors so each is a genuine new participant (no
			// same-author participant-count contention to reason about here).
			p, err := ps.Reply(context.Background(), ReplyParams{ThreadID: th.ID, AuthorID: int64(900 + i), BodyRaw: "r"})
			if err != nil {
				errs[i] = err
				return
			}
			nums[i] = p.PostNumber
		}(i)
	}
	wg.Wait()

	for i := range n {
		if errs[i] != nil {
			t.Fatalf("reply %d: %v", i, errs[i])
		}
	}
	// The n replies must be exactly post_numbers 2..n+1 (opening is 1).
	got := append([]int32(nil), nums...)
	slices.Sort(got)
	for i, v := range got {
		if int(v) != i+2 {
			t.Fatalf("post_numbers must be contiguous 2..%d, got %v", n+1, got)
		}
	}
	if th := getThread(t, th.ID); th.PostsCount != int32(n+1) || th.HighestPostNumber != int32(n+1) {
		t.Fatalf("counters after %d concurrent replies: posts=%d highest=%d", n, th.PostsCount, th.HighestPostNumber)
	}
}
