package service

import (
	"context"
	"encoding/json"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"api/internal/platform/galgame/dto"
	"api/internal/platform/galgame/model"
	"api/internal/platform/galgame/repository"

	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// Query-flexibility layer (step 07): sparse fieldset + list-level include. These
// are add-only over the frozen step-02 contract; the pure tests pin the fields
// semantics (id恒留 / 只裁不改 / unknown-ignored / order-independent / inactive =
// byte-identical) and the DB tests pin the batched (non-N+1) include expansion.

func keysOf(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func TestApplyPublicFieldsInactiveByteIdentical(t *testing.T) {
	svc := &GalgameService{cdnBase: "https://cdn.example.com/img"}
	rec := svc.projectDetail(sampleGalgame(), sampleScoreMeta(),
		PublicInclude{Intro: true, Scores: true, Covers: true, Taxonomy: true}, "sfw", 0)

	// An absent/empty selector must return the value untouched → the default
	// response is byte-for-byte the frozen contract.
	a, _ := json.Marshal(rec)
	b, _ := json.Marshal(ApplyPublicFields(rec, ParsePublicFields("")))
	if string(a) != string(b) {
		t.Errorf("inactive fields must be byte-identical:\n want %s\n  got %s", a, b)
	}
	if string(a) != mustMarshal(t, ApplyPublicFields(rec, ParsePublicFields("  ,  "))) {
		t.Errorf("whitespace-only fields must be inactive (byte-identical)")
	}
}

func TestApplyPublicFieldsSemantics(t *testing.T) {
	svc := &GalgameService{cdnBase: "https://cdn.example.com/img"}
	rec := svc.projectDetail(sampleGalgame(), sampleScoreMeta(),
		PublicInclude{Intro: true, Scores: true, Taxonomy: true}, "sfw", 0)
	full := toMap(t, rec)

	// id is always retained, even when not named; only the named key joins it.
	m := toMap(t, ApplyPublicFields(rec, ParsePublicFields("names")))
	if _, ok := m["id"]; !ok {
		t.Errorf("id must always be retained")
	}
	if len(m) != 2 || m["names"] == nil {
		t.Errorf("fields=names must yield exactly {id,names}, got %v", keysOf(m))
	}

	// 只裁不改: the retained value is byte-identical to the frozen shape.
	if mustMarshal(t, full["names"]) != mustMarshal(t, m["names"]) {
		t.Errorf("retained value must be unchanged")
	}

	// order-independent.
	m1 := toMap(t, ApplyPublicFields(rec, ParsePublicFields("names,refs")))
	m2 := toMap(t, ApplyPublicFields(rec, ParsePublicFields("refs,names")))
	if len(m1) != 3 || len(m2) != 3 {
		t.Errorf("order must not matter: %v vs %v", keysOf(m1), keysOf(m2))
	}

	// unknown names silently ignored (never a 400, never a phantom key).
	m3 := toMap(t, ApplyPublicFields(rec, ParsePublicFields("nonexistent,typo")))
	if len(m3) != 1 {
		t.Errorf("unknown fields must be ignored (only id survives), got %v", keysOf(m3))
	}

	// id-only.
	if m4 := toMap(t, ApplyPublicFields(rec, ParsePublicFields("id"))); len(m4) != 1 {
		t.Errorf("fields=id must yield only id, got %v", keysOf(m4))
	}
}

func TestFieldsIncludeInteractionOnItem(t *testing.T) {
	// include=scores expanded → fields=scores returns {id, scores}.
	sc := buildGalgameScores(sampleScoreMeta())
	withScores := dto.PublicGalgameItem{ID: 1, Scores: &sc}
	m := toMap(t, ApplyPublicFields(withScores, ParsePublicFields("scores")))
	if len(m) != 2 || m["scores"] == nil {
		t.Errorf("fields=scores with include must return {id,scores}, got %v", keysOf(m))
	}

	// No include → Scores nil → omitempty → fields=scores does NOT conjure the
	// key (fields acts AFTER expansion; it never implicitly expands).
	noScores := dto.PublicGalgameItem{ID: 1}
	m2 := toMap(t, ApplyPublicFields(noScores, ParsePublicFields("scores")))
	if _, ok := m2["scores"]; ok {
		t.Errorf("fields=scores without include must not add the key")
	}
	if len(m2) != 1 {
		t.Errorf("want only {id}, got %v", keysOf(m2))
	}
}

// ── DB-backed: list-level include expansion + non-N+1 batching ──

func seedGameWithExpansions(t *testing.T, id int) {
	t.Helper()
	insertPublicGalgame(t, id, "g", "sfw", time.Date(2026, 1, 1, 0, 0, id, 0, time.UTC))

	off := model.GalgameOfficial{Name: "maker-" + strconv.Itoa(id), Category: "producer"}
	if err := testDB.Create(&off).Error; err != nil {
		t.Fatalf("official: %v", err)
	}
	if err := testDB.Create(&model.GalgameOfficialRelation{GalgameID: id, OfficialID: off.ID}).Error; err != nil {
		t.Fatalf("official rel: %v", err)
	}

	rating, median, now := 80.0, 78, time.Now()
	if err := testDB.Create(&model.GalgameVNDBMeta{GalgameID: id, VNDBID: "v" + strconv.Itoa(id), Rating: &rating, VoteCount: 100, SyncedAt: now}).Error; err != nil {
		t.Fatalf("vndb meta: %v", err)
	}
	if err := testDB.Create(&model.GalgameBangumiMeta{GalgameID: id, BID: id, Score: 7.5, Rank: id, Total: 50, SyncedAt: now}).Error; err != nil {
		t.Fatalf("bangumi meta: %v", err)
	}
	if err := testDB.Create(&model.GalgameEGMeta{GalgameID: id, EGGameID: id, Median: &median, VoteCount: 30, SyncedAt: now}).Error; err != nil {
		t.Fatalf("eg meta: %v", err)
	}
}

func TestPublicListIncludeExpansion(t *testing.T) {
	cleanTables(t)
	migratePublicMeta(t)
	for i := 1; i <= 3; i++ {
		seedGameWithExpansions(t, i)
	}
	ctx := context.Background()

	// Without include: the thin item carries neither block (default unchanged).
	plain, err := testSvc.PublicList(ctx, "id", "", 10, "sfw", PublicItemInclude{})
	if err != nil {
		t.Fatalf("plain: %v", err)
	}
	for _, it := range plain.Items {
		if it.Officials != nil || it.Scores != nil {
			t.Errorf("thin item must not carry officials/scores without include")
		}
	}

	// With include: both blocks present and correctly populated per item.
	rich, err := testSvc.PublicList(ctx, "id", "", 10, "sfw", PublicItemInclude{Officials: true, Scores: true})
	if err != nil {
		t.Fatalf("rich: %v", err)
	}
	if len(rich.Items) != 3 {
		t.Fatalf("want 3 items, got %d", len(rich.Items))
	}
	for _, it := range rich.Items {
		if it.Officials == nil || len(*it.Officials) != 1 || (*it.Officials)[0].Name != "maker-"+strconv.Itoa(it.ID) {
			t.Errorf("item %d: officials expansion wrong: %v", it.ID, it.Officials)
		}
		if it.Scores == nil || it.Scores.VNDB == nil || it.Scores.Bangumi == nil || it.Scores.EG == nil {
			t.Errorf("item %d: scores expansion missing a source: %+v", it.ID, it.Scores)
		}
	}
}

// countingLogger tallies every SQL statement GORM traces, so a test can prove a
// page-load's query count is constant regardless of page size (non-N+1).
type countingLogger struct {
	logger.Interface
	n *int64
}

func (c countingLogger) LogMode(logger.LogLevel) logger.Interface { return c }
func (c countingLogger) Trace(context.Context, time.Time, func() (string, int64), error) {
	atomic.AddInt64(c.n, 1)
}

func countingService(n *int64) *GalgameService {
	sess := testDB.Session(&gorm.Session{Logger: countingLogger{Interface: logger.Default.LogMode(logger.Silent), n: n}})
	return NewGalgameService(
		repository.NewGalgameRepository(sess),
		repository.NewRevisionRepository(sess),
		repository.NewPRRepository(sess),
		repository.NewUserReadonlyRepository(sess),
	).WithCDNBase("https://cdn.example.com/img")
}

func TestPublicListIncludeNonN1(t *testing.T) {
	cleanTables(t)
	migratePublicMeta(t)
	for i := 1; i <= 6; i++ {
		seedGameWithExpansions(t, i)
	}
	ctx := context.Background()

	count := func(limit int) int64 {
		var n int64
		data, err := countingService(&n).PublicList(ctx, "id", "", limit, "sfw",
			PublicItemInclude{Officials: true, Scores: true})
		if err != nil {
			t.Fatalf("list(limit=%d): %v", limit, err)
		}
		if len(data.Items) != limit {
			t.Fatalf("want %d items, got %d", limit, len(data.Items))
		}
		return n
	}

	small, large := count(2), count(6)
	if small != large {
		t.Errorf("query count must be constant across page size (non-N+1): 2-item=%d 6-item=%d", small, large)
	}
	// The whole page = a bounded, batched query set: list + 2 pin lookups +
	// officials join + 3 score-meta INs = 7. Guard against a per-item regression.
	if large > 8 {
		t.Errorf("expected a small constant query count (~7), got %d — possible N+1", large)
	}
}

func mustMarshal(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(b)
}
