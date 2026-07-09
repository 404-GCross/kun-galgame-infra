package service

import (
	"context"
	"fmt"
	"testing"

	"api/internal/platform/community/model"
	"api/internal/platform/community/repository"
)

func TestKeyset_ThreadList(t *testing.T) {
	cleanTables(t)
	ts := NewThreadService(testDB, NoopSink{})

	// Five topics, opened in order → increasing last_posted_at.
	var opened []int64
	for i := range 5 {
		th := openTopic(t, ts, "letmoe", 100, fmt.Sprintf("b%d", i), "x")
		opened = append(opened, th.ID)
	}

	// Paginate the list (limit 2) and collect every id.
	var got []int64
	seen := map[int64]bool{}
	cursor := repository.ThreadCursor{}
	for {
		page, err := ts.ListBySite("letmoe", model.ThreadKindTopic, cursor, 2)
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		if len(page) == 0 {
			break
		}
		for _, th := range page {
			if seen[th.ID] {
				t.Fatalf("keyset returned duplicate id %d", th.ID)
			}
			seen[th.ID] = true
			got = append(got, th.ID)
		}
		last := page[len(page)-1]
		cursor = repository.ThreadCursor{LastPosted: *last.LastPostedAt, ID: last.ID}
		if len(page) < 2 {
			break
		}
	}
	if len(got) != len(opened) {
		t.Fatalf("keyset should cover all threads without gaps: got %d of %d", len(got), len(opened))
	}
	// Newest activity first = reverse of open order.
	for i := 0; i < len(opened); i++ {
		want := opened[len(opened)-1-i]
		if got[i] != want {
			t.Fatalf("order at %d: want %d, got %d (full %v)", i, want, got[i], got)
		}
	}
}

func TestKeyset_Posts(t *testing.T) {
	cleanTables(t)
	ts := NewThreadService(testDB, NoopSink{})
	ps := NewPostService(testDB, NoopSink{})
	th := openTopic(t, ts, "letmoe", 100, "b1", "opening")

	// 12 replies by a TL1 author (exempt from the TL0 daily reply cap).
	seedTrust(t, 800, model.TrustLevelBasic, 0)
	const replies = 12
	for i := range replies {
		if _, err := ps.Reply(context.Background(), ReplyParams{ThreadID: th.ID, AuthorID: 800, BodyRaw: "r"}); err != nil {
			t.Fatalf("reply %d: %v", i, err)
		}
	}

	// Paginate posts ascending by post_number (page size 5); expect 1..13.
	var numbers []int32
	var after int32 = 0
	for {
		page, err := ps.ListPosts(th.ID, after, 5)
		if err != nil {
			t.Fatalf("list posts: %v", err)
		}
		if len(page) == 0 {
			break
		}
		for _, p := range page {
			numbers = append(numbers, p.PostNumber)
		}
		after = page[len(page)-1].PostNumber
		if len(page) < 5 {
			break
		}
	}
	want := replies + 1 // opening post + replies
	if len(numbers) != want {
		t.Fatalf("expected %d posts, got %d", want, len(numbers))
	}
	for i, n := range numbers {
		if int(n) != i+1 {
			t.Fatalf("post_number at %d: want %d, got %d", i, i+1, n)
		}
	}
}
