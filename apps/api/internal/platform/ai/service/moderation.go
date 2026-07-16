// Package service holds the AI-gateway semantic logic: the moderate-text route,
// per-call metering into ai_usage, and the per-route daily budget fuse. All of
// moderate-text is FAIL-OPEN (doc 20 §7 / charter ruling 5): upstream down,
// over budget, or env empty → allow (flagged:false) + warn, never block, never
// error. The LLM is only ever on the async scan path — this service serves
// callers that have already decided the content is live.
package service

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"time"

	"api/internal/platform/ai/model"
	"api/internal/platform/ai/route"
	"api/internal/platform/ai/upstream"

	"gorm.io/gorm"
)

// upstreamClient is the seam the moderation service dials (satisfied by
// *upstream.Client; a fake in tests). Keeping it an interface lets the service
// tests drive the three moderate-text states without a live channel layer.
type upstreamClient interface {
	Configured() bool
	Model() string
	ChatJSON(ctx context.Context, system, user string, maxTokens int) (upstream.ChatResult, error)
}

// ModerationService serves the moderate-text route.
type ModerationService struct {
	db       *gorm.DB
	upstream upstreamClient
}

// NewModerationService wires the service to the kun_ai DB and the upstream
// client. up may be an unconfigured client (empty env) — that is the degraded
// mode, not an error.
func NewModerationService(db *gorm.DB, up upstreamClient) *ModerationService {
	return &ModerationService{db: db, upstream: up}
}

// ModerateParams is one moderate-text call. Site is DERIVED from the S2S client
// binding by the handler and passed here; it is never on the wire.
type ModerateParams struct {
	Site     string
	Text     string
	AuthorID *int64
}

// ModerateResult is the route's verdict. On any degraded path Flagged is false
// (fail-open) and Degraded is true.
type ModerateResult struct {
	Route      string
	Flagged    bool
	Categories []string
	Score      *float32
	Channel    string
	Degraded   bool
}

// moderateSystemPrompt instructs the upstream to emit a compact JSON verdict.
// Parsing is tolerant (extractJSON), and any failure degrades to allow — so the
// exact wording is not load-bearing for safety, only for quality.
const moderateSystemPrompt = `You are a content-safety classifier for a community platform.
Judge the user message for policy violations (abuse/harassment, spam, illegal content, sexual content involving minors, or other clearly harmful content).
Respond with ONLY a JSON object, no prose, of the form:
{"flagged": <bool>, "categories": [<string>, ...], "score": <number between 0 and 1>}
"flagged" is true only when the message clearly violates policy; "score" is your confidence that it violates policy.`

const moderateMaxTokens = 256

// Moderate runs the moderate-text route: budget fuse → degraded check → upstream
// call → parse, metering one ai_usage row on every path. It ALWAYS returns a
// result with a nil error (fail-open); internal metering/budget errors are
// swallowed with a warn.
func (s *ModerationService) Moderate(ctx context.Context, p ModerateParams) (ModerateResult, error) {
	routeName := model.RouteModerateText
	spec, _ := route.Lookup(routeName)
	start := time.Now()

	// 1. Budget fuse (record-don't-block). A set cap whose current-day spend is
	// exceeded → fail-open allow, status=budget_denied. A NULL/absent cap never
	// blocks (the v0 default).
	if s.overBudget(ctx, routeName, p.Site) {
		slog.Warn("ai moderate-text over budget — fail-open allow", "site", p.Site, "route", routeName)
		s.meter(ctx, model.AIUsage{
			Site: p.Site, Route: routeName, Status: model.StatusBudgetDenied,
			LatencyMs: msSince(start),
		})
		return failOpen(routeName, "", spec), nil
	}

	// 2. Degraded: channel layer not wired (empty env). Never dial, never error.
	if !s.upstream.Configured() {
		s.meter(ctx, model.AIUsage{
			Site: p.Site, Route: routeName, Status: model.StatusDegraded,
			LatencyMs: msSince(start),
		})
		return failOpen(routeName, "", spec), nil
	}

	// 3. Call upstream. Any transport/HTTP/parse failure → fail-open degraded.
	res, err := s.upstream.ChatJSON(ctx, moderateSystemPrompt, p.Text, moderateMaxTokens)
	if err != nil {
		slog.Warn("ai moderate-text upstream error — fail-open allow", "site", p.Site, "err", err)
		s.meter(ctx, model.AIUsage{
			Site: p.Site, Route: routeName, Status: model.StatusUpstreamError,
			Channel: s.upstream.Model(), LatencyMs: msSince(start),
		})
		return failOpen(routeName, "", spec), nil
	}
	v, perr := parseModeration(res.Content)
	if perr != nil {
		slog.Warn("ai moderate-text unparseable upstream reply — fail-open allow", "site", p.Site, "err", perr)
		s.meter(ctx, model.AIUsage{
			Site: p.Site, Route: routeName, Status: model.StatusUpstreamError,
			Channel: res.Channel, PromptTokens: res.PromptTokens, CompletionTokens: res.CompletionTokens,
			LatencyMs: msSince(start),
		})
		return failOpen(routeName, res.Channel, spec), nil
	}

	// 4. Normal scored path.
	s.meter(ctx, model.AIUsage{
		Site: p.Site, Route: routeName, Status: model.StatusOK,
		Channel: res.Channel, PromptTokens: res.PromptTokens, CompletionTokens: res.CompletionTokens,
		LatencyMs: msSince(start),
	})
	return ModerateResult{
		Route: routeName, Flagged: v.Flagged, Categories: v.Categories, Score: v.Score,
		Channel: res.Channel, Degraded: false,
	}, nil
}

// failOpen is the allow-and-warn result shared by all degraded paths. spec is
// carried so a future functional (fail-closed) route can branch here instead.
func failOpen(routeName, channel string, _ route.Spec) ModerateResult {
	return ModerateResult{Route: routeName, Flagged: false, Channel: channel, Degraded: true}
}

// overBudget reports whether the resolved cap for (route, site) is set AND the
// site's current-day spend on the route has reached it. A missing/NULL cap = no
// block. A query error is treated as "not over budget" (fail-open) with a warn.
func (s *ModerationService) overBudget(ctx context.Context, routeName, site string) bool {
	capMicro := s.resolveCap(ctx, routeName, site)
	if capMicro == nil {
		return false
	}
	var spent int64
	if err := s.db.WithContext(ctx).Model(&model.AIUsage{}).
		Where("route = ? AND site = ? AND created_at >= date_trunc('day', now())", routeName, site).
		Select("COALESCE(SUM(cost_micro), 0)").Scan(&spent).Error; err != nil {
		slog.Warn("ai budget spend query failed — fail-open (no block)", "site", site, "route", routeName, "err", err)
		return false
	}
	return spent >= *capMicro
}

// resolveCap returns the applicable daily cap: the (route, site) row if present
// (its cap, even if NULL — an explicit per-site override), else the (route, ”)
// route-wide default row's cap, else nil (no cap).
func (s *ModerationService) resolveCap(ctx context.Context, routeName, site string) *int64 {
	scopes := []string{site}
	if site != "" {
		scopes = append(scopes, "")
	}
	for _, sc := range scopes {
		var b model.AIRouteBudget
		err := s.db.WithContext(ctx).Where("route = ? AND site = ?", routeName, sc).Take(&b).Error
		if err == nil {
			return b.DailyCostCapMicro
		}
	}
	return nil
}

// meter writes one ai_usage row best-effort. A failure is logged and swallowed —
// metering never fails the caller's moderation result (charter ruling 6).
func (s *ModerationService) meter(ctx context.Context, row model.AIUsage) {
	if err := s.db.WithContext(ctx).Create(&row).Error; err != nil {
		slog.Warn("ai usage metering failed (non-fatal)", "site", row.Site, "route", row.Route, "status", row.Status, "err", err)
	}
}

func msSince(start time.Time) int {
	return int(time.Since(start).Milliseconds())
}

// verdict is the parsed upstream moderation reply.
type verdict struct {
	Flagged    bool
	Categories []string
	Score      *float32
}

// parseModeration tolerantly parses the upstream JSON verdict. It extracts the
// first {...} span (some servers wrap JSON in prose despite the instruction),
// then unmarshals the known fields.
func parseModeration(content string) (verdict, error) {
	raw := extractJSON(content)
	var v struct {
		Flagged    bool     `json:"flagged"`
		Categories []string `json:"categories"`
		Score      *float32 `json:"score"`
	}
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		return verdict{}, err
	}
	return verdict{Flagged: v.Flagged, Categories: v.Categories, Score: v.Score}, nil
}

// extractJSON returns the substring from the first '{' to the last '}', or the
// input unchanged when no braces are present (json.Unmarshal then reports the
// real error).
func extractJSON(s string) string {
	i := strings.IndexByte(s, '{')
	j := strings.LastIndexByte(s, '}')
	if i >= 0 && j > i {
		return s[i : j+1]
	}
	return s
}
