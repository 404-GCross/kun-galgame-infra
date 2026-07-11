package service

// Mod-actor edit/delete (docs/proj/17 decision 3): a consuming site's moderator
// may edit or tombstone any post by declaring as_moderator; the author path must
// keep working unchanged (regression), and a non-author WITHOUT the declaration
// stays rejected.

import (
	"context"
	"strings"
	"testing"

	"api/internal/platform/community/model"
)

func TestModActor_EditAnotherAuthorsPost(t *testing.T) {
	cleanTables(t)
	ts := NewThreadService(testDB, NoopSink{})
	ps := NewPostService(testDB, NoopSink{})
	th := openTopic(t, ts, "letmoe", 100, "b1", "opening")

	seedTrust(t, 200, model.TrustLevelBasic, 0)
	post, err := ps.Reply(context.Background(), ReplyParams{ThreadID: th.ID, AuthorID: 200, BodyRaw: "original"})
	if err != nil {
		t.Fatalf("reply: %v", err)
	}

	// A non-author without the mod declaration is still rejected (regression).
	if _, err := ps.Edit(context.Background(), EditParams{PostID: post.ID, AuthorID: 999, BodyRaw: "hijack"}); err != ErrNotAuthor {
		t.Fatalf("non-author edit must stay ErrNotAuthor, got %v", err)
	}

	// The mod-actor variant edits it: re-cooked + sanitized, edited_at stamped,
	// author unchanged.
	seedTrust(t, 999, model.TrustLevelRegular, 0)
	edited, err := ps.Edit(context.Background(), EditParams{
		PostID: post.ID, AuthorID: 999, BodyRaw: "cleaned <script>x</script> **body**", AsModerator: true,
	})
	if err != nil {
		t.Fatalf("mod edit: %v", err)
	}
	if edited.AuthorID != 200 {
		t.Fatalf("mod edit must not move the author: got %d", edited.AuthorID)
	}
	if edited.EditedAt == nil {
		t.Fatal("mod edit must stamp edited_at")
	}
	if strings.Contains(edited.ContentHTML, "<script") || !strings.Contains(edited.ContentHTML, "<strong>body</strong>") {
		t.Fatalf("mod edit must re-cook + sanitize: %s", edited.ContentHTML)
	}

	// A mod edit of a tombstone is still not an editable surface.
	if err := ps.Delete(context.Background(), post.ID, 999, true); err != nil {
		t.Fatalf("mod delete: %v", err)
	}
	if _, err := ps.Edit(context.Background(), EditParams{PostID: post.ID, AuthorID: 999, BodyRaw: "resurrect", AsModerator: true}); err != ErrPostNotEditable {
		t.Fatalf("mod edit of a tombstone must be ErrPostNotEditable, got %v", err)
	}
}

func TestModActor_DeleteTombstonesLikeAuthorPath(t *testing.T) {
	cleanTables(t)
	ts := NewThreadService(testDB, NoopSink{})
	ps := NewPostService(testDB, NoopSink{})
	th := openTopic(t, ts, "letmoe", 100, "b1", "opening")

	seedTrust(t, 200, model.TrustLevelBasic, 0)
	post, err := ps.Reply(context.Background(), ReplyParams{ThreadID: th.ID, AuthorID: 200, BodyRaw: "to be removed"})
	if err != nil {
		t.Fatalf("reply: %v", err)
	}

	// Non-author without the declaration → rejected (regression).
	if err := ps.Delete(context.Background(), post.ID, 999, false); err != ErrNotAuthor {
		t.Fatalf("non-author delete must stay ErrNotAuthor, got %v", err)
	}

	// Mod delete → the SAME tombstone terminal state the author path produces:
	// status=deleted, post_number preserved, posts_count untouched.
	if err := ps.Delete(context.Background(), post.ID, 999, true); err != nil {
		t.Fatalf("mod delete: %v", err)
	}
	got := getPost(t, post.ID)
	if got.Status != model.PostStatusDeleted || got.PostNumber != post.PostNumber {
		t.Fatalf("mod delete tombstone: status=%d number=%d (want deleted, %d)", got.Status, got.PostNumber, post.PostNumber)
	}
	reloaded := getThread(t, th.ID)
	if reloaded.PostsCount != 2 {
		t.Fatalf("posts_count must stay at numbers allocated: got %d", reloaded.PostsCount)
	}

	// Idempotent across actors: the author re-deleting the tombstone is a no-op.
	if err := ps.Delete(context.Background(), post.ID, 200, false); err != nil {
		t.Fatalf("author delete of an already-tombstoned post must no-op, got %v", err)
	}
}
