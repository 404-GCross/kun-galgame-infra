package service

import (
	"context"
	"strings"
	"testing"

	"api/internal/platform/community/model"
	"api/internal/platform/community/repository"
)

// openFeedback opens a feedback thread by an established (TL1) author so the
// sandbox does not perturb the flow under test.
func openFeedback(t *testing.T, ts *ThreadService, site string, author int64, anchorKind int16, anchorID, body string) *model.CommunityThread {
	t.Helper()
	seedTrust(t, author, model.TrustLevelBasic, 0)
	th, _, err := ts.OpenFeedback(context.Background(), OpenThreadParams{
		Site: site, AuthorID: author, AnchorKind: anchorKind, AnchorID: anchorID,
		Title: "fb", ContentRating: model.ContentRatingAll, BodyRaw: body,
	})
	if err != nil {
		t.Fatalf("open feedback: %v", err)
	}
	return th
}

func TestEditPost_AuthorAndSanitize(t *testing.T) {
	cleanTables(t)
	ts := NewThreadService(testDB, NoopSink{})
	ps := NewPostService(testDB, NoopSink{})
	th := openTopic(t, ts, "letmoe", 100, "b1", "opening")
	post := visibleReply(t, ps, th.ID, 200) // TL1, visible
	ctx := context.Background()

	// Author edits their own post: re-cooked HTML, raw preserved, edited_at set.
	edited, err := ps.Edit(ctx, EditParams{PostID: post.ID, AuthorID: 200, BodyRaw: "new <script>alert(1)</script> **body**"})
	if err != nil {
		t.Fatalf("edit: %v", err)
	}
	if strings.Contains(edited.ContentHTML, "<script") {
		t.Fatalf("edited HTML must be sanitized: %s", edited.ContentHTML)
	}
	if !strings.Contains(edited.ContentHTML, "<strong>body</strong>") {
		t.Fatalf("edited markdown should cook: %s", edited.ContentHTML)
	}
	if edited.ContentRaw != "new <script>alert(1)</script> **body**" {
		t.Fatal("raw markdown must be preserved verbatim")
	}
	if edited.EditedAt == nil {
		t.Fatal("edited_at must be stamped")
	}
	if edited.SanitizerVersion != 1 {
		t.Fatalf("sanitizer_version: want 1, got %d", edited.SanitizerVersion)
	}
	// post_number and status are unchanged by an edit.
	if edited.PostNumber != post.PostNumber || edited.Status != model.PostStatusVisible {
		t.Fatalf("edit must not move number/status: number=%d status=%d", edited.PostNumber, edited.Status)
	}
	// Persisted, not just returned.
	if reloaded := getPost(t, post.ID); reloaded.EditedAt == nil || !strings.Contains(reloaded.ContentHTML, "<strong>body</strong>") {
		t.Fatalf("edit not persisted: %+v", reloaded)
	}
}

func TestEditPost_NotAuthor(t *testing.T) {
	cleanTables(t)
	ts := NewThreadService(testDB, NoopSink{})
	ps := NewPostService(testDB, NoopSink{})
	th := openTopic(t, ts, "letmoe", 100, "b1", "opening")
	post := visibleReply(t, ps, th.ID, 200)

	seedTrust(t, 999, model.TrustLevelBasic, 0)
	if _, err := ps.Edit(context.Background(), EditParams{PostID: post.ID, AuthorID: 999, BodyRaw: "hijack"}); err != ErrNotAuthor {
		t.Fatalf("a different user must be refused with ErrNotAuthor, got %v", err)
	}
	if getPost(t, post.ID).ContentRaw != "content" {
		t.Fatal("a refused edit must not mutate the post")
	}
}

func TestEditPost_TombstonedAndHiddenRejected(t *testing.T) {
	cleanTables(t)
	ts := NewThreadService(testDB, NoopSink{})
	ps := NewPostService(testDB, NoopSink{})
	th := openTopic(t, ts, "letmoe", 100, "b1", "opening")
	ctx := context.Background()

	// A tombstoned post is not editable.
	tomb := visibleReply(t, ps, th.ID, 200)
	if err := ps.Delete(ctx, tomb.ID, 200, false); err != nil {
		t.Fatalf("self-delete: %v", err)
	}
	if _, err := ps.Edit(ctx, EditParams{PostID: tomb.ID, AuthorID: 200, BodyRaw: "resurrect"}); err != ErrPostNotEditable {
		t.Fatalf("editing a tombstoned post must return ErrPostNotEditable, got %v", err)
	}

	// A held/hidden (first-post-hold) newcomer post is not editable either.
	held, err := ps.Reply(ctx, ReplyParams{ThreadID: th.ID, AuthorID: 700, BodyRaw: "held"})
	if err != nil {
		t.Fatalf("reply: %v", err)
	}
	if held.Status != model.PostStatusHidden {
		t.Fatalf("first newcomer post should be held: %d", held.Status)
	}
	if _, err := ps.Edit(ctx, EditParams{PostID: held.ID, AuthorID: 700, BodyRaw: "edit while held"}); err != ErrPostNotEditable {
		t.Fatalf("editing a held post must return ErrPostNotEditable, got %v", err)
	}
}

func TestEditPost_TL0CapAppliesToEdit(t *testing.T) {
	cleanTables(t)
	ts := NewThreadService(testDB, NoopSink{})
	ps := NewPostService(testDB, NoopSink{})
	th := openTopic(t, ts, "letmoe", 100, "b1", "opening")
	ctx := context.Background()

	// A TL0 author with the hold budget spent posts a visible reply.
	seedTrust(t, 800, model.TrustLevelNew, 0)
	post, err := ps.Reply(ctx, ReplyParams{ThreadID: th.ID, AuthorID: 800, BodyRaw: "clean"})
	if err != nil {
		t.Fatalf("reply: %v", err)
	}
	if post.Status != model.PostStatusVisible {
		t.Fatalf("expected visible, got %d", post.Status)
	}

	// The TL0 per-post link cap (2) applies to the edited body too — editing is
	// not an escape hatch out of the sandbox.
	overLinks := "[1](http://a.com) [2](http://b.com) [3](http://c.com)"
	if _, err := ps.Edit(ctx, EditParams{PostID: post.ID, AuthorID: 800, BodyRaw: overLinks}); err == nil {
		t.Fatal("a TL0 edit exceeding the link cap must be refused")
	} else if _, ok := err.(*SandboxError); !ok {
		t.Fatalf("expected a SandboxError, got %T: %v", err, err)
	}
	if getPost(t, post.ID).ContentRaw != "clean" {
		t.Fatal("a capped edit must not mutate the post")
	}

	// A within-cap edit by the same TL0 author still succeeds.
	if _, err := ps.Edit(ctx, EditParams{PostID: post.ID, AuthorID: 800, BodyRaw: "still [ok](http://a.com)"}); err != nil {
		t.Fatalf("a within-cap TL0 edit should pass: %v", err)
	}
}

func TestDeletePost_TombstonePreservesNumbering(t *testing.T) {
	cleanTables(t)
	ts := NewThreadService(testDB, NoopSink{})
	ps := NewPostService(testDB, NoopSink{})
	th := openTopic(t, ts, "letmoe", 100, "b1", "opening") // post #1
	ctx := context.Background()

	seedTrust(t, 200, model.TrustLevelBasic, 0)
	p2, _ := ps.Reply(ctx, ReplyParams{ThreadID: th.ID, AuthorID: 200, BodyRaw: "two"})   // #2
	p3, _ := ps.Reply(ctx, ReplyParams{ThreadID: th.ID, AuthorID: 200, BodyRaw: "three"}) // #3

	// Author self-deletes the middle post.
	if err := ps.Delete(ctx, p2.ID, 200, false); err != nil {
		t.Fatalf("self-delete: %v", err)
	}
	gone := getPost(t, p2.ID)
	if gone.Status != model.PostStatusDeleted {
		t.Fatalf("self-delete should tombstone: status=%d", gone.Status)
	}
	// Invariant 13: the number is preserved (no thread collapse).
	if gone.PostNumber != 2 {
		t.Fatalf("post_number must be preserved on tombstone: %d", gone.PostNumber)
	}
	if getPost(t, p3.ID).PostNumber != 3 {
		t.Fatal("a later post must keep its number after a mid-thread delete")
	}
	// posts_count does NOT decrement — the tombstone still occupies its number.
	th = getThread(t, th.ID)
	if th.PostsCount != 3 || th.HighestPostNumber != 3 {
		t.Fatalf("counts must not collapse: posts=%d highest=%d", th.PostsCount, th.HighestPostNumber)
	}

	// Idempotent: deleting again is a no-op (no error, still tombstoned).
	if err := ps.Delete(ctx, p2.ID, 200, false); err != nil {
		t.Fatalf("idempotent re-delete should be a no-op: %v", err)
	}
	if getPost(t, p2.ID).Status != model.PostStatusDeleted {
		t.Fatal("re-delete must leave the tombstone intact")
	}

	// A different user cannot self-delete someone else's post (authorization is
	// checked before the idempotent short-circuit).
	seedTrust(t, 999, model.TrustLevelBasic, 0)
	p4, _ := ps.Reply(ctx, ReplyParams{ThreadID: th.ID, AuthorID: 200, BodyRaw: "four"})
	if err := ps.Delete(ctx, p4.ID, 999, false); err != ErrNotAuthor {
		t.Fatalf("a non-author delete must return ErrNotAuthor, got %v", err)
	}
	if getPost(t, p4.ID).Status != model.PostStatusVisible {
		t.Fatal("a refused delete must not tombstone the post")
	}
}

func TestDeletePost_CoexistsWithModReject(t *testing.T) {
	cleanTables(t)
	ts := NewThreadService(testDB, NoopSink{})
	ps := NewPostService(testDB, NoopSink{})
	fs := NewFlagService(testDB, &recordingSink{})
	rs := NewReviewService(testDB)
	th := openTopic(t, ts, "letmoe", 100, "b1", "opening")
	ctx := context.Background()

	// A post tombstoned by a moderator reject (three reports → hide → reject).
	post := visibleReply(t, ps, th.ID, 200)
	for _, r := range []int64{300, 301, 302} {
		fs.Submit(ctx, post.ID, r, nil, nil)
	}
	item := pendingReviewForPost(t, post.ID)
	if item == nil {
		t.Fatal("no queue item")
	}
	if err := rs.Reject(ctx, item.ID, 999); err != nil {
		t.Fatalf("mod reject: %v", err)
	}
	if getPost(t, post.ID).Status != model.PostStatusDeleted {
		t.Fatal("mod reject should tombstone")
	}

	// The author's self-delete on the same (already mod-tombstoned) post is a
	// harmless idempotent no-op — the two tombstone actors share one terminal
	// state and coexist.
	if err := ps.Delete(ctx, post.ID, 200, false); err != nil {
		t.Fatalf("author delete over a mod tombstone must be a no-op: %v", err)
	}
	final := getPost(t, post.ID)
	if final.Status != model.PostStatusDeleted || final.PostNumber != post.PostNumber {
		t.Fatalf("terminal state must be a numbered tombstone: status=%d number=%d", final.Status, final.PostNumber)
	}
}

// TestOpeningPostMeta covers the per-site list projection (finding: a held or
// self-deleted opening post must not leak its title into the thread list). The
// thread's own status stays open in all three cases, so the list must read the
// opening POST's status.
func TestOpeningPostMeta(t *testing.T) {
	cleanTables(t)
	ts := NewThreadService(testDB, NoopSink{})
	ps := NewPostService(testDB, NoopSink{})
	ctx := context.Background()

	// A visible opening post (established TL1 author).
	visible := openTopic(t, ts, "letmoe", 100, "b1", "visible opening")

	// A held opening post: a TL0 newcomer's first topic is held (first-post hold).
	seedTrust(t, 700, model.TrustLevelNew, 2)
	heldThread, heldPost, err := ts.OpenTopic(ctx, OpenThreadParams{
		Site: "letmoe", AuthorID: 700, AnchorKind: model.AnchorKindBoard, AnchorID: "b1",
		Title: "held", ContentRating: model.ContentRatingAll, BodyRaw: "held opening",
	})
	if err != nil {
		t.Fatalf("open held topic: %v", err)
	}
	if heldPost.Status != model.PostStatusHidden {
		t.Fatalf("a newcomer's first opening post should be held: %d", heldPost.Status)
	}

	// A self-deleted (tombstoned) opening post.
	seedTrust(t, 300, model.TrustLevelBasic, 0)
	delThread, delPost, err := ts.OpenTopic(ctx, OpenThreadParams{
		Site: "letmoe", AuthorID: 300, AnchorKind: model.AnchorKindBoard, AnchorID: "b1",
		Title: "to delete", ContentRating: model.ContentRatingAll, BodyRaw: "to delete",
	})
	if err != nil {
		t.Fatalf("open topic to delete: %v", err)
	}
	if err := ps.Delete(ctx, delPost.ID, 300, false); err != nil {
		t.Fatalf("self-delete opening: %v", err)
	}

	metas, err := ts.OpeningPostMeta([]int64{visible.ID, heldThread.ID, delThread.ID})
	if err != nil {
		t.Fatalf("opening post meta: %v", err)
	}
	if m := metas[visible.ID]; m.Status != model.PostStatusVisible || m.AuthorID != 100 {
		t.Fatalf("visible opening: want {status=0 author=100}, got %+v", m)
	}
	if m := metas[heldThread.ID]; m.Status != model.PostStatusHidden || m.AuthorID != 700 {
		t.Fatalf("held opening: want {status=1 author=700}, got %+v", m)
	}
	if m := metas[delThread.ID]; m.Status != model.PostStatusDeleted || m.AuthorID != 300 {
		t.Fatalf("deleted opening: want {status=2 author=300}, got %+v", m)
	}

	// An empty comments thread has no opening post → absent from the map (so the
	// list leaves its opening_* fields nil).
	empty, err := ts.GetOrCreateCommentsThread(ctx, CommentsThreadParams{
		Site: "letmoe", AnchorKind: model.AnchorKindSiteGame, AnchorID: "g1", ContentRating: model.ContentRatingAll, ActorID: 1,
	})
	if err != nil {
		t.Fatalf("resolve comments: %v", err)
	}
	metas2, err := ts.OpeningPostMeta([]int64{empty.ID})
	if err != nil {
		t.Fatalf("opening post meta (empty): %v", err)
	}
	if _, ok := metas2[empty.ID]; ok {
		t.Fatal("an empty comments thread must have no opening-post meta")
	}
}

func TestListThreads_AnchorFilter(t *testing.T) {
	cleanTables(t)
	ts := NewThreadService(testDB, NoopSink{})

	// Two anchors under the same site, three feedback threads total.
	fb1 := openFeedback(t, ts, "letmoe", 100, model.AnchorKindSiteResource, "r1", "a")
	openFeedback(t, ts, "letmoe", 101, model.AnchorKindSiteResource, "r2", "b")
	fb3 := openFeedback(t, ts, "letmoe", 102, model.AnchorKindSiteResource, "r1", "c")

	// Filter to r1 → exactly its two feedback threads.
	got, err := ts.ListBySite("letmoe", model.ThreadKindFeedback, model.AnchorKindSiteResource, "r1", repository.ThreadCursor{}, 50)
	if err != nil {
		t.Fatalf("anchor list: %v", err)
	}
	ids := map[int64]bool{}
	for _, th := range got {
		ids[th.ID] = true
	}
	if len(got) != 2 || !ids[fb1.ID] || !ids[fb3.ID] {
		t.Fatalf("r1 filter should return exactly {%d,%d}, got %v", fb1.ID, fb3.ID, got)
	}

	// A non-existent anchor → empty.
	empty, err := ts.ListBySite("letmoe", model.ThreadKindFeedback, model.AnchorKindSiteResource, "r404", repository.ThreadCursor{}, 50)
	if err != nil {
		t.Fatalf("empty anchor list: %v", err)
	}
	if len(empty) != 0 {
		t.Fatalf("a non-existent anchor should be empty, got %d", len(empty))
	}

	// No anchor filter (empty id) → the whole site's feedback threads.
	all, err := ts.ListBySite("letmoe", model.ThreadKindFeedback, 0, "", repository.ThreadCursor{}, 50)
	if err != nil {
		t.Fatalf("site list: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("the site should have 3 feedback threads, got %d", len(all))
	}

	// The filter is generic across kinds: a comments thread on an anchor is
	// reachable the same way.
	if _, err := ts.GetOrCreateCommentsThread(context.Background(), CommentsThreadParams{
		Site: "letmoe", AnchorKind: model.AnchorKindSiteGame, AnchorID: "g5", ContentRating: model.ContentRatingAll, ActorID: 1,
	}); err != nil {
		t.Fatalf("resolve comments: %v", err)
	}
	comments, err := ts.ListBySite("letmoe", model.ThreadKindComments, model.AnchorKindSiteGame, "g5", repository.ThreadCursor{}, 50)
	if err != nil {
		t.Fatalf("comments anchor list: %v", err)
	}
	if len(comments) != 1 {
		t.Fatalf("the anchor filter should be generic across kinds, got %d comments threads", len(comments))
	}
}
