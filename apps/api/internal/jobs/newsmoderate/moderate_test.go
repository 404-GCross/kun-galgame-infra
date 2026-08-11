package newsmoderate

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"api/internal/platform/news/model"
	"api/internal/platform/news/newstest"

	"gorm.io/gorm"
)

var testDB *gorm.DB

func TestMain(m *testing.M) {
	db, release, ok := newstest.Open()
	if !ok {
		os.Exit(0)
	}
	testDB = db
	code := m.Run()
	release()
	os.Exit(code)
}

type fakeScorer struct {
	tier0    Tier0Verdict
	tier0Err error
	score    ScoreVerdict
	scoreErr error
	t0Calls  int
	scCalls  int
}

func (f *fakeScorer) Tier0(context.Context, string) (Tier0Verdict, error) {
	f.t0Calls++
	return f.tier0, f.tier0Err
}

func (f *fakeScorer) Score(context.Context, string) (ScoreVerdict, error) {
	f.scCalls++
	return f.score, f.scoreErr
}

func allow() Tier0Verdict { return Tier0Verdict{Decision: model.Tier0Allow, Matched: []string{}} }

func reset(t *testing.T) {
	t.Helper()
	if err := newstest.Truncate(testDB); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	if err := testDB.Exec(`
		INSERT INTO news_source (key, display_name, homepage_url, attribution, publisher_uid, column_url, active)
		VALUES ('ymgal', '月幕 Galgame', 'https://www.ymgal.games', 'attr', 114748, '', true)
		ON CONFLICT (key) DO NOTHING`).Error; err != nil {
		t.Fatalf("seed source: %v", err)
	}
}

func seedItem(t *testing.T, extID, title string) model.NewsItem {
	t.Helper()
	row := model.NewsItem{
		SourceKey: model.SourceKeyYmgal, Lane: model.LaneNews, ExternalID: extID,
		Title: title, Preview: "preview", UpstreamCategory: "资讯",
		SourceURL: "https://www.ymgal.games/a/" + extID, BannerOriginURL: "",
		PublishedAt: time.Now().UTC(), Status: model.StatusPending,
	}
	if err := testDB.Create(&row).Error; err != nil {
		t.Fatalf("seed item: %v", err)
	}
	return row
}

func statusOf(t *testing.T, id int64) int16 {
	t.Helper()
	var s int16
	testDB.Raw(`SELECT status FROM news_item WHERE id = ?`, id).Scan(&s)
	return s
}

func run(t *testing.T, f *fakeScorer) Stats {
	t.Helper()
	st, err := New(testDB, f, Opts{Apply: true, Limit: 100}).Run(context.Background())
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	return st
}

// TestDegradedNeverAdvancesAndNeverCountsAsScored is the whole point of this
// package. Every other moderation caller in the repo treats a degraded reply as
// flagged=false, i.e. as an approval. Here it must leave the item pending AND
// leave it in the queue, or one AI outage silently clears a whole backlog.
func TestDegradedNeverAdvancesAndNeverCountsAsScored(t *testing.T) {
	reset(t)
	it := seedItem(t, "deg-1", "some title")

	f := &fakeScorer{tier0: allow(), score: ScoreVerdict{Degraded: true, Channel: "x"}}
	st := run(t, f)
	if st.Degraded != 1 || st.Scored != 0 {
		t.Fatalf("degraded=%d scored=%d, want 1 and 0", st.Degraded, st.Scored)
	}
	if got := statusOf(t, it.ID); got != model.StatusPending {
		t.Errorf("a degraded verdict moved the item to status=%d", got)
	}

	var v model.NewsModerationVerdict
	testDB.Where("item_id = ?", it.ID).Take(&v)
	if v.AIFlagged != nil {
		t.Errorf("ai_flagged must stay NULL when the model did not speak, got %v", *v.AIFlagged)
	}

	// The item must come back around: degraded is not scored.
	st = run(t, f)
	if st.NeedScoring != 1 {
		t.Errorf("a degraded item was not re-queued (need_scoring=%d)", st.NeedScoring)
	}
}

// TestAttemptCeilingStopsTheRetryLoop: the append-only verdict log is the retry
// counter. A permanently broken gateway must stop being hammered, and the item
// must end up parked for a human rather than approved.
func TestAttemptCeilingStopsTheRetryLoop(t *testing.T) {
	reset(t)
	it := seedItem(t, "ceil-1", "some title")
	f := &fakeScorer{tier0: allow(), scoreErr: errors.New("gateway down")}

	for i := range model.ModerationAttemptCeiling {
		st := run(t, f)
		if st.NeedScoring != 1 {
			t.Fatalf("attempt %d: need_scoring=%d, want 1", i+1, st.NeedScoring)
		}
	}
	st := run(t, f)
	if st.NeedScoring != 0 || st.Exhausted != 1 {
		t.Errorf("after %d attempts: need_scoring=%d exhausted=%d, want 0 and 1",
			model.ModerationAttemptCeiling, st.NeedScoring, st.Exhausted)
	}
	if got := statusOf(t, it.ID); got != model.StatusPending {
		t.Errorf("an unscoreable item must stay pending for a human, got status=%d", got)
	}
}

// TestTier0DenyAutoRejectsWithoutSpendingAnAICall: a banned term is already a
// decision. Calling the gateway to confirm it is pure spend, and spend on this
// track comes out of a shared bucket (free-quota-is-a-production-dependency).
func TestTier0DenyAutoRejectsWithoutSpendingAnAICall(t *testing.T) {
	reset(t)
	it := seedItem(t, "deny-1", "加微信看片")
	f := &fakeScorer{tier0: Tier0Verdict{Decision: model.Tier0Deny, Matched: []string{"加微信"}}}

	st := run(t, f)
	if st.AutoRejected != 1 {
		t.Fatalf("auto_rejected=%d, want 1", st.AutoRejected)
	}
	if f.scCalls != 0 {
		t.Errorf("the AI gateway was called %d times on a settled Tier0 deny", f.scCalls)
	}
	if got := statusOf(t, it.ID); got != model.StatusRejected {
		t.Errorf("status=%d, want rejected", got)
	}

	var d model.NewsModerationDecision
	if err := testDB.Where("item_id = ?", it.ID).Take(&d).Error; err != nil {
		t.Fatalf("no decision row was written for the auto-reject: %v", err)
	}
	if d.ActorUID != model.SystemActorUID {
		t.Errorf("actor_uid=%d, want the system actor", d.ActorUID)
	}
	if d.Reason == "" {
		t.Error("an auto-reject must record which term matched, or it cannot be appealed")
	}
	if d.FromStatus != model.StatusPending || d.ToStatus != model.StatusRejected {
		t.Errorf("decision recorded %d→%d", d.FromStatus, d.ToStatus)
	}
}

// TestNothingHerePublishes: a clean verdict is advice, not a decision. The queue
// is emptied by people.
func TestNothingHerePublishes(t *testing.T) {
	reset(t)
	it := seedItem(t, "clean-1", "a perfectly ordinary announcement")
	score := float32(0.01)
	f := &fakeScorer{tier0: allow(), score: ScoreVerdict{Flagged: false, Score: &score, Channel: "omni"}}

	st := run(t, f)
	if st.Scored != 1 {
		t.Fatalf("scored=%d, want 1", st.Scored)
	}
	if got := statusOf(t, it.ID); got != model.StatusPending {
		t.Errorf("a clean AI verdict published the item (status=%d) — the AI is an adviser, not a gate", got)
	}

	var v model.NewsModerationVerdict
	testDB.Where("item_id = ?", it.ID).Take(&v)
	if v.AIFlagged == nil || *v.AIFlagged {
		t.Errorf("ai_flagged=%v, want a non-nil false", v.AIFlagged)
	}

	// Scored means scored: it must not be graded twice.
	if st2 := run(t, f); st2.NeedScoring != 0 {
		t.Errorf("a settled item was re-queued (need_scoring=%d)", st2.NeedScoring)
	}
}

// TestFingerprintRequeuesEditedText: wave 02 sends a changed published item back
// to pending. Its old verdict judged text that no longer exists, so it must not
// count.
func TestFingerprintRequeuesEditedText(t *testing.T) {
	reset(t)
	it := seedItem(t, "fp-1", "original title")
	f := &fakeScorer{tier0: allow(), score: ScoreVerdict{Channel: "omni"}}
	run(t, f)

	if st := run(t, f); st.NeedScoring != 0 {
		t.Fatalf("unchanged item re-queued (need_scoring=%d)", st.NeedScoring)
	}
	if err := testDB.Model(&model.NewsItem{}).Where("id = ?", it.ID).
		UpdateColumn("title", "the partner rewrote this").Error; err != nil {
		t.Fatalf("edit: %v", err)
	}
	if st := run(t, f); st.NeedScoring != 1 {
		t.Errorf("edited item was not re-queued (need_scoring=%d)", st.NeedScoring)
	}
	var n int64
	testDB.Raw(`SELECT count(*) FROM news_moderation_verdict WHERE item_id = ?`, it.ID).Scan(&n)
	if n != 2 {
		t.Errorf("verdict log holds %d rows, want 2 — the log is append-only history", n)
	}
}

// TestDecidedItemsAreNotGraded: only pending is a queue. Re-grading a rejected
// item would let the machine argue with the human who rejected it.
func TestDecidedItemsAreNotGraded(t *testing.T) {
	reset(t)
	for _, s := range []int16{model.StatusPublished, model.StatusRejected, model.StatusWithdrawn} {
		it := seedItem(t, fmt.Sprintf("decided-%d", s), "title")
		testDB.Model(&model.NewsItem{}).Where("id = ?", it.ID).UpdateColumn("status", s)
	}
	dead := seedItem(t, "dead-1", "title")
	now := time.Now()
	testDB.Model(&model.NewsItem{}).Where("id = ?", dead.ID).UpdateColumn("dead_at", now)

	f := &fakeScorer{tier0: allow(), score: ScoreVerdict{Channel: "omni"}}
	st := run(t, f)
	if st.Candidates != 0 {
		t.Errorf("candidates=%d, want 0 (decided and upstream-dead items are not a queue)", st.Candidates)
	}
}

// TestDryRunWritesNothing keeps --apply=false honest: the run still calls the
// graders (that is the forecast) but must not leave a row behind.
func TestDryRunWritesNothing(t *testing.T) {
	reset(t)
	seedItem(t, "dry-1", "加微信看片")
	f := &fakeScorer{tier0: Tier0Verdict{Decision: model.Tier0Deny, Matched: []string{"加微信"}}}

	if _, err := New(testDB, f, Opts{Apply: false, Limit: 100}).Run(context.Background()); err != nil {
		t.Fatalf("dry run: %v", err)
	}
	for _, table := range []string{"news_moderation_verdict", "news_moderation_decision"} {
		var n int64
		testDB.Raw(`SELECT count(*) FROM ` + table).Scan(&n)
		if n != 0 {
			t.Errorf("dry run wrote %d rows into %s", n, table)
		}
	}
	var s int16
	testDB.Raw(`SELECT status FROM news_item WHERE external_id = 'dry-1'`).Scan(&s)
	if s != model.StatusPending {
		t.Errorf("dry run changed a status to %d", s)
	}
}

// TestTier0OutageDoesNotSkipStraightToTheAI: if the word list is unreachable we
// do not know whether a banned term is present, so the item is degraded — not
// handed to the adviser as though Tier0 had said allow.
func TestTier0OutageDoesNotSkipStraightToTheAI(t *testing.T) {
	reset(t)
	seedItem(t, "t0-down", "title")
	f := &fakeScorer{tier0Err: errors.New("trust down"), score: ScoreVerdict{Channel: "omni"}}

	st := run(t, f)
	if f.scCalls != 0 {
		t.Errorf("the AI was consulted %d times despite Tier0 being unknown", f.scCalls)
	}
	if st.Degraded != 1 {
		t.Errorf("degraded=%d, want 1", st.Degraded)
	}
}

func TestFingerprintCoversEveryGradedField(t *testing.T) {
	base := model.NewsItem{Title: "a", Preview: "b", Lane: model.LaneNews}
	seen := map[string]string{base.Fingerprint(): "base"}
	for name, mutated := range map[string]model.NewsItem{
		"title":   {Title: "x", Preview: "b", Lane: model.LaneNews},
		"preview": {Title: "a", Preview: "x", Lane: model.LaneNews},
		"lane":    {Title: "a", Preview: "b", Lane: model.LaneColumn},
	} {
		fp := mutated.Fingerprint()
		if other, dup := seen[fp]; dup {
			t.Errorf("changing %s collides with %s", name, other)
		}
		seen[fp] = name
	}
	// A boundary shift must not produce the same digest: "ab"+"" and "a"+"b".
	if (model.NewsItem{Title: "ab", Lane: model.LaneNews}).Fingerprint() ==
		(model.NewsItem{Title: "a", Preview: "b", Lane: model.LaneNews}).Fingerprint() {
		t.Error("the field separator does not separate")
	}
}
