package service

import (
	"context"
	"testing"
	"time"

	"api/internal/platform/news/model"
)

func newAdminFixture(t *testing.T) *AdminService {
	t.Helper()
	newFixture(t)
	return NewAdminService(testDB, testCDNBase)
}

func adminItem(t *testing.T, extID string, status int16) int64 {
	t.Helper()
	return insertLane(t, extID, model.LaneNews, status, time.Now().UTC().Truncate(time.Second), false)
}

func addVerdict(t *testing.T, itemID int64, fp string, degraded bool) {
	t.Helper()
	v := model.NewsModerationVerdict{
		ItemID: itemID, ContentFingerprint: fp,
		Tier0Decision: model.Tier0Allow, Degraded: degraded,
	}
	if !degraded {
		flagged := false
		v.AIFlagged = &flagged
	}
	if err := testDB.Create(&v).Error; err != nil {
		t.Fatalf("verdict: %v", err)
	}
}

func fingerprintOf(t *testing.T, id int64) string {
	t.Helper()
	var it model.NewsItem
	if err := testDB.Where("id = ?", id).Take(&it).Error; err != nil {
		t.Fatalf("load item: %v", err)
	}
	return it.Fingerprint()
}

// TestTransitionTable pins the whole write contract, including the appeals.
// Tier0 auto-rejects will produce false positives, and a rejected item with no
// way back would make an automated decision permanent by accident.
func TestTransitionTable(t *testing.T) {
	cases := []struct {
		from   int16
		action string
		want   int16
		legal  bool
	}{
		{model.StatusPending, ActionPublish, model.StatusPublished, true},
		{model.StatusPending, ActionReject, model.StatusRejected, true},
		{model.StatusPending, ActionWithdraw, 0, false},
		{model.StatusPending, ActionRepend, 0, false},

		{model.StatusPublished, ActionWithdraw, model.StatusWithdrawn, true},
		{model.StatusPublished, ActionRepend, model.StatusPending, true},
		{model.StatusPublished, ActionPublish, 0, false},
		{model.StatusPublished, ActionReject, 0, false},

		{model.StatusRejected, ActionPublish, model.StatusPublished, true},
		{model.StatusRejected, ActionRepend, model.StatusPending, true},
		{model.StatusRejected, ActionWithdraw, 0, false},

		{model.StatusWithdrawn, ActionPublish, model.StatusPublished, true},
		{model.StatusWithdrawn, ActionReject, model.StatusRejected, true},
		{model.StatusWithdrawn, ActionRepend, model.StatusPending, true},
	}
	for _, c := range cases {
		svc := newAdminFixture(t)
		id := adminItem(t, "tr", c.from)
		_, err := svc.Decide(context.Background(), id, 42, c.action, "because")
		if c.legal {
			if err != nil {
				t.Errorf("%s from %d: %v", c.action, c.from, err)
				continue
			}
			var got int16
			testDB.Raw(`SELECT status FROM news_item WHERE id = ?`, id).Scan(&got)
			if got != c.want {
				t.Errorf("%s from %d landed on %d, want %d", c.action, c.from, got, c.want)
			}
			continue
		}
		if err == nil {
			t.Errorf("%s from %d was accepted but must be illegal", c.action, c.from)
		}
	}
}

// TestWithdrawIsTheRetractionPathAndDeletesNothing: 苍麟 clears ad spam by hand
// after the fact, so we must be able to pull an item back. The charter's rule is
// mark, never delete — the row stays visible to moderators.
func TestWithdrawIsTheRetractionPathAndDeletesNothing(t *testing.T) {
	svc := newAdminFixture(t)
	id := adminItem(t, "wd", model.StatusPublished)

	if _, err := svc.Decide(context.Background(), id, 7, ActionWithdraw, "上游删了"); err != nil {
		t.Fatalf("withdraw: %v", err)
	}
	var n int64
	testDB.Raw(`SELECT count(*) FROM news_item WHERE id = ?`, id).Scan(&n)
	if n != 1 {
		t.Fatal("withdrawing deleted the row")
	}
	if _, err := svc.Item(context.Background(), id); err != nil {
		t.Errorf("a withdrawn item must stay visible to moderators: %v", err)
	}
	// The public face is a different question, and wave 01 already answers it.
	feed, err := NewPublicService(testDB, testCDNBase).Feed(context.Background(), FeedFilter{}, "", 50)
	if err != nil {
		t.Fatalf("feed: %v", err)
	}
	if len(feed.Items) != 0 {
		t.Errorf("a withdrawn item is still on the public feed: %d items", len(feed.Items))
	}
}

func TestDecisionsAreAudited(t *testing.T) {
	svc := newAdminFixture(t)
	id := adminItem(t, "au", model.StatusPending)

	if _, err := svc.Decide(context.Background(), id, 115235, ActionPublish, "看过了"); err != nil {
		t.Fatalf("publish: %v", err)
	}
	if _, err := svc.Decide(context.Background(), id, 114748, ActionWithdraw, "改主意"); err != nil {
		t.Fatalf("withdraw: %v", err)
	}
	detail, err := svc.Item(context.Background(), id)
	if err != nil {
		t.Fatalf("detail: %v", err)
	}
	if len(detail.Decisions) != 2 {
		t.Fatalf("audit line holds %d rows, want 2", len(detail.Decisions))
	}
	newest := detail.Decisions[0]
	if newest.ActorUID != 114748 || newest.FromStatus != model.StatusPublished || newest.ToStatus != model.StatusWithdrawn {
		t.Errorf("newest decision = %+v", newest)
	}
	if newest.Reason == "" {
		t.Error("the reason must survive to the wire; it is the only account of why")
	}
}

// TestVerdictCurrencyFollowsTheText: a verdict that judged older text is history.
// Rendering it as the item's standing judgement would show a moderator an
// approval of words that are no longer on the page.
func TestVerdictCurrencyFollowsTheText(t *testing.T) {
	svc := newAdminFixture(t)
	id := adminItem(t, "cur", model.StatusPending)
	addVerdict(t, id, fingerprintOf(t, id), false)

	detail, err := svc.Item(context.Background(), id)
	if err != nil {
		t.Fatalf("detail: %v", err)
	}
	if detail.Verdict == nil || !detail.Verdict.Current {
		t.Fatalf("a fresh verdict must be current: %+v", detail.Verdict)
	}

	if err := testDB.Model(&model.NewsItem{}).Where("id = ?", id).
		UpdateColumn("title", "rewritten upstream").Error; err != nil {
		t.Fatalf("edit: %v", err)
	}
	detail, err = svc.Item(context.Background(), id)
	if err != nil {
		t.Fatalf("detail: %v", err)
	}
	if detail.Verdict != nil {
		t.Errorf("a stale verdict is being presented as the standing judgement: %+v", detail.Verdict)
	}
	if len(detail.Verdicts) != 1 || detail.Verdicts[0].Current {
		t.Errorf("the verdict must stay in the history marked not-current: %+v", detail.Verdicts)
	}
}

// TestUngradedAndDegradedAreSeparateQueues: "nobody looked" and "the machine
// looked and could not answer" need different handling, and a single status
// column cannot tell them apart.
func TestUngradedAndDegradedAreSeparateQueues(t *testing.T) {
	svc := newAdminFixture(t)
	ungraded := adminItem(t, "q-none", model.StatusPending)
	degraded := adminItem(t, "q-deg", model.StatusPending)
	graded := adminItem(t, "q-ok", model.StatusPending)
	addVerdict(t, degraded, fingerprintOf(t, degraded), true)
	addVerdict(t, graded, fingerprintOf(t, graded), false)

	ung, err := svc.Queue(context.Background(), QueueFilter{Ungraded: true}, 0, 50)
	if err != nil {
		t.Fatalf("queue: %v", err)
	}
	if len(ung.Items) != 1 || ung.Items[0].ID != ungraded {
		t.Errorf("ungraded queue = %+v", ung.Items)
	}
	deg, err := svc.Queue(context.Background(), QueueFilter{Degraded: true}, 0, 50)
	if err != nil {
		t.Fatalf("queue: %v", err)
	}
	if len(deg.Items) != 1 || deg.Items[0].ID != degraded {
		t.Errorf("degraded queue = %+v", deg.Items)
	}

	all, err := svc.Queue(context.Background(), QueueFilter{Statuses: []int16{model.StatusPending}}, 0, 50)
	if err != nil {
		t.Fatalf("queue: %v", err)
	}
	if all.Total != 3 {
		t.Errorf("unfiltered pending total = %d, want 3", all.Total)
	}

	stats, err := svc.Stats(context.Background())
	if err != nil {
		t.Fatalf("stats: %v", err)
	}
	if stats.Ungraded != 1 || stats.Degraded != 1 {
		t.Errorf("stats ungraded=%d degraded=%d, want 1 and 1", stats.Ungraded, stats.Degraded)
	}
}

// TestFilteredQueueTotalMatchesTheFilteredSet: the grading filters cannot run in
// SQL, so the page has to be cut after filtering. Cutting before it would leave
// later pages short and report a total the caller never sees.
func TestFilteredQueueTotalMatchesTheFilteredSet(t *testing.T) {
	svc := newAdminFixture(t)
	for i := range 5 {
		id := adminItem(t, string(rune('a'+i)), model.StatusPending)
		if i%2 == 0 {
			addVerdict(t, id, fingerprintOf(t, id), false)
		}
	}
	page, err := svc.Queue(context.Background(), QueueFilter{Ungraded: true}, 0, 1)
	if err != nil {
		t.Fatalf("queue: %v", err)
	}
	if page.Total != 2 {
		t.Errorf("total = %d, want 2 ungraded", page.Total)
	}
	if len(page.Items) != 1 {
		t.Errorf("page holds %d items, want 1", len(page.Items))
	}
	second, err := svc.Queue(context.Background(), QueueFilter{Ungraded: true}, 1, 1)
	if err != nil {
		t.Fatalf("queue: %v", err)
	}
	if len(second.Items) != 1 || second.Items[0].ID == page.Items[0].ID {
		t.Errorf("second page did not advance: %+v", second.Items)
	}
	beyond, err := svc.Queue(context.Background(), QueueFilter{Ungraded: true}, 99, 1)
	if err != nil {
		t.Fatalf("queue: %v", err)
	}
	if len(beyond.Items) != 0 {
		t.Errorf("an offset past the end returned %d items", len(beyond.Items))
	}
}

func TestUnknownActionAndMissingItem(t *testing.T) {
	svc := newAdminFixture(t)
	id := adminItem(t, "err", model.StatusPending)
	if _, err := svc.Decide(context.Background(), id, 1, "delete", ""); err != ErrUnknownAction {
		t.Errorf("unknown action returned %v", err)
	}
	if _, err := svc.Decide(context.Background(), 999999, 1, ActionPublish, ""); err != ErrNotFound {
		t.Errorf("missing item returned %v", err)
	}
}
