package service

import (
	"context"
	"testing"

	"api/internal/platform/ai/model"
	"api/internal/platform/ai/upstream"
)

// TestModerateNormal — the upstream-scored path: a configured upstream returns a
// flagged verdict, which is parsed and metered as status=ok with tokens+channel.
func TestModerateNormal(t *testing.T) {
	cleanTables(t)
	up := &fakeUpstream{
		configured: true,
		model:      "deepseek-chat",
		result: upstream.ChatResult{
			Content:          `{"flagged": true, "categories": ["abuse"], "score": 0.91}`,
			Channel:          "deepseek-chat",
			PromptTokens:     42,
			CompletionTokens: 8,
		},
	}
	s := newLLMOnly(up)

	res, err := s.Moderate(context.Background(), ModerateParams{Site: "letmoe", Text: "you idiot"})
	if err != nil {
		t.Fatalf("Moderate: %v", err)
	}
	if !res.Flagged || res.Degraded {
		t.Fatalf("want flagged+not-degraded, got flagged=%v degraded=%v", res.Flagged, res.Degraded)
	}
	if res.Route != model.RouteModerateText || res.Channel != "deepseek-chat" {
		t.Fatalf("route/channel = %q/%q", res.Route, res.Channel)
	}
	if res.Score == nil || *res.Score < 0.9 || len(res.Categories) != 1 || res.Categories[0] != "abuse" {
		t.Fatalf("verdict fields not parsed: score=%v categories=%v", res.Score, res.Categories)
	}
	if up.calls != 1 {
		t.Fatalf("upstream calls = %d, want 1", up.calls)
	}

	rows := usageRows(t, "letmoe", model.RouteModerateText)
	if len(rows) != 1 {
		t.Fatalf("want 1 metered row, got %d", len(rows))
	}
	r := rows[0]
	if r.Status != model.StatusOK || r.Channel != "deepseek-chat" || r.PromptTokens != 42 || r.CompletionTokens != 8 {
		t.Fatalf("metered row wrong: status=%d channel=%q prompt=%d completion=%d", r.Status, r.Channel, r.PromptTokens, r.CompletionTokens)
	}
	if r.Site != "letmoe" {
		t.Fatalf("metered site = %q, want letmoe (derived, not on wire)", r.Site)
	}
}

// TestModerateUpstreamErrorFailOpen — a configured upstream that 5xxs/errors:
// fail-open allow (flagged:false, degraded:true), metered status=upstream_error,
// and the call WAS attempted (calls==1).
func TestModerateUpstreamErrorFailOpen(t *testing.T) {
	cleanTables(t)
	up := &fakeUpstream{configured: true, model: "deepseek-chat", err: context.DeadlineExceeded}
	s := newLLMOnly(up)

	res, err := s.Moderate(context.Background(), ModerateParams{Site: "letmoe", Text: "hello"})
	if err != nil {
		t.Fatalf("Moderate must never error (fail-open): %v", err)
	}
	if res.Flagged || !res.Degraded {
		t.Fatalf("want fail-open (flagged=false degraded=true), got flagged=%v degraded=%v", res.Flagged, res.Degraded)
	}
	if up.calls != 1 {
		t.Fatalf("upstream calls = %d, want 1 (it must have dialled)", up.calls)
	}
	rows := usageRows(t, "letmoe", model.RouteModerateText)
	if len(rows) != 1 || rows[0].Status != model.StatusUpstreamError {
		t.Fatalf("want 1 row status=upstream_error(1), got %+v", rows)
	}
}

// TestModerateDegradedEnvEmpty — the decoupling proof: an UNCONFIGURED upstream
// (empty env) degrades WITHOUT dialling (calls==0), fail-open, metered
// status=degraded.
func TestModerateDegradedEnvEmpty(t *testing.T) {
	cleanTables(t)
	up := &fakeUpstream{configured: false}
	s := newLLMOnly(up)

	res, err := s.Moderate(context.Background(), ModerateParams{Site: "letmoe", Text: "anything"})
	if err != nil {
		t.Fatalf("Moderate must never error: %v", err)
	}
	if res.Flagged || !res.Degraded {
		t.Fatalf("want fail-open degraded, got flagged=%v degraded=%v", res.Flagged, res.Degraded)
	}
	if res.Channel != "" {
		t.Fatalf("degraded channel must be empty, got %q", res.Channel)
	}
	if up.calls != 0 {
		t.Fatalf("upstream calls = %d, want 0 (degraded must NOT dial)", up.calls)
	}
	rows := usageRows(t, "letmoe", model.RouteModerateText)
	if len(rows) != 1 || rows[0].Status != model.StatusDegraded {
		t.Fatalf("want 1 row status=degraded(4), got %+v", rows)
	}
}

// TestBudgetOverCapFailOpen — a set cap whose current-day spend is exceeded:
// fail-open allow, metered status=budget_denied, and upstream NOT dialled.
func TestBudgetOverCapFailOpen(t *testing.T) {
	cleanTables(t)
	insertBudget(t, model.RouteModerateText, "letmoe", ptr[int64](100))
	insertUsageCost(t, "letmoe", model.RouteModerateText, 60)
	insertUsageCost(t, "letmoe", model.RouteModerateText, 50) // total 110 >= 100

	up := &fakeUpstream{configured: true, model: "deepseek-chat",
		result: upstream.ChatResult{Content: `{"flagged": true}`, Channel: "deepseek-chat"}}
	s := newLLMOnly(up)

	res, err := s.Moderate(context.Background(), ModerateParams{Site: "letmoe", Text: "spend"})
	if err != nil {
		t.Fatalf("Moderate: %v", err)
	}
	if res.Flagged || !res.Degraded {
		t.Fatalf("over-budget must fail-open (flagged=false degraded=true), got flagged=%v degraded=%v", res.Flagged, res.Degraded)
	}
	if up.calls != 0 {
		t.Fatalf("over-budget must NOT dial upstream, calls=%d", up.calls)
	}
	// The two seed rows + the budget_denied row.
	var denied int64
	if err := testDB.Model(&model.AIUsage{}).
		Where("site = ? AND route = ? AND status = ?", "letmoe", model.RouteModerateText, model.StatusBudgetDenied).
		Count(&denied).Error; err != nil {
		t.Fatalf("count denied: %v", err)
	}
	if denied != 1 {
		t.Fatalf("want 1 budget_denied row, got %d", denied)
	}
}

// TestBudgetUnderCapAllows — the same cap but current-day spend below it: the
// call proceeds normally (status=ok), proving the fuse only trips over the cap.
func TestBudgetUnderCapAllows(t *testing.T) {
	cleanTables(t)
	insertBudget(t, model.RouteModerateText, "letmoe", ptr[int64](1000))
	insertUsageCost(t, "letmoe", model.RouteModerateText, 10)

	up := &fakeUpstream{configured: true, model: "deepseek-chat",
		result: upstream.ChatResult{Content: `{"flagged": false}`, Channel: "deepseek-chat"}}
	s := newLLMOnly(up)

	res, err := s.Moderate(context.Background(), ModerateParams{Site: "letmoe", Text: "fine"})
	if err != nil {
		t.Fatalf("Moderate: %v", err)
	}
	if res.Degraded {
		t.Fatalf("under-cap must proceed normally, got degraded=true")
	}
	if up.calls != 1 {
		t.Fatalf("under-cap must dial upstream, calls=%d", up.calls)
	}
}

// TestBudgetNullCapNoBlock — the v0 default: a NULL cap (or an explicit NULL-cap
// override row) never blocks, even with accumulated spend.
func TestBudgetNullCapNoBlock(t *testing.T) {
	cleanTables(t)
	insertBudget(t, model.RouteModerateText, "letmoe", nil) // explicit no-cap override
	insertUsageCost(t, "letmoe", model.RouteModerateText, 999999)

	up := &fakeUpstream{configured: true, model: "deepseek-chat",
		result: upstream.ChatResult{Content: `{"flagged": false}`, Channel: "deepseek-chat"}}
	s := newLLMOnly(up)

	res, err := s.Moderate(context.Background(), ModerateParams{Site: "letmoe", Text: "fine"})
	if err != nil {
		t.Fatalf("Moderate: %v", err)
	}
	if res.Degraded || up.calls != 1 {
		t.Fatalf("NULL cap must not block: degraded=%v calls=%d", res.Degraded, up.calls)
	}
}

// TestBudgetRouteDefaultOverride — a route-wide default (” site) cap applies to
// a site that has no specific row.
func TestBudgetRouteDefaultOverride(t *testing.T) {
	cleanTables(t)
	insertBudget(t, model.RouteModerateText, "", ptr[int64](100)) // route-wide default
	insertUsageCost(t, "letmoe", model.RouteModerateText, 150)    // letmoe over the default

	up := &fakeUpstream{configured: true, model: "deepseek-chat",
		result: upstream.ChatResult{Content: `{"flagged": false}`, Channel: "deepseek-chat"}}
	s := newLLMOnly(up)

	res, err := s.Moderate(context.Background(), ModerateParams{Site: "letmoe", Text: "spend"})
	if err != nil {
		t.Fatalf("Moderate: %v", err)
	}
	if !res.Degraded || up.calls != 0 {
		t.Fatalf("route-default cap must apply to letmoe: degraded=%v calls=%d", res.Degraded, up.calls)
	}
}
