// Package service holds the AI-gateway semantic logic: the moderate-text route,
// per-call metering into ai_usage, and the per-route daily budget fuse. All of
// moderate-text is FAIL-OPEN (doc 20 §7 / charter ruling 5): upstream down,
// over budget, or env empty → allow (flagged:false) + warn, never block, never
// error. The LLM is only ever on the async scan path — this service serves
// callers that have already decided the content is live.
//
// moderate-text is a Tier1→Tier2 cascade (spec 09): omni-moderation (Tier1,
// coarse) escalates to the LLM (Tier2, fine) only for verdicts whose adopted
// relevantScore meets the escalation threshold, plus a small negative-sampling
// fraction of below-threshold verdicts for shadow calibration. Each upstream
// call meters its own ai_usage row, so a single cascade can produce two rows.
package service

import (
	"context"
	"encoding/json"
	"log/slog"
	"math/rand/v2"
	"strings"
	"time"

	"api/internal/platform/ai/model"
	"api/internal/platform/ai/route"
	"api/internal/platform/ai/upstream"

	"gorm.io/gorm"
)

// upstreamClient is the Tier2 LLM seam (satisfied by *upstream.Client; a fake in
// tests). Keeping it an interface lets the service tests drive the cascade
// without a live channel layer.
type upstreamClient interface {
	Configured() bool
	Model() string
	ChatJSON(ctx context.Context, system, user string, maxTokens int) (upstream.ChatResult, error)
}

// omniClient is the Tier1 coarse-pass seam (satisfied by *upstream.OmniClient; a
// fake in tests). Empty env → not configured, and the cascade runs the Tier2 LLM
// path exactly as before.
type omniClient interface {
	Configured() bool
	Model() string
	Moderate(ctx context.Context, input string) (upstream.OmniResult, error)
}

// ModerationService serves the moderate-text route as the Tier1→Tier2 cascade.
type ModerationService struct {
	db   *gorm.DB
	omni omniClient
	llm  upstreamClient

	escalateThreshold  float32
	negativeSampleRate float64
	// rand is the injectable random source for the negative-sampling seam
	// (test determinism); it returns a value in [0,1). nil in options →
	// math/rand/v2.Float64.
	rand func() float64
}

// ModerationOptions carries the cascade tuning (spec 09 ruling 3). Rand is the
// injectable random source for negative sampling; nil → math/rand/v2.
type ModerationOptions struct {
	EscalateThreshold  float32
	NegativeSampleRate float64
	Rand               func() float64
}

// NewModerationService wires the service to the kun_ai DB, the Tier1 (omni) and
// Tier2 (llm) clients, and the cascade tuning. Either client may be unconfigured
// (empty env) — that is a valid tier-off state, not an error.
func NewModerationService(db *gorm.DB, omni omniClient, llm upstreamClient, opts ModerationOptions) *ModerationService {
	r := opts.Rand
	if r == nil {
		r = rand.Float64
	}
	return &ModerationService{
		db:                 db,
		omni:               omni,
		llm:                llm,
		escalateThreshold:  opts.EscalateThreshold,
		negativeSampleRate: opts.NegativeSampleRate,
		rand:               r,
	}
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

// ModerateSystemPrompt instructs the upstream to emit a compact JSON verdict.
// Parsing is tolerant (extractJSON), and any failure degrades to allow — so the
// exact wording is not load-bearing for safety, only for quality. It is EXPORTED
// so the offline batch scorer (cmd/scan-backlog, spec 10) scores with the exact
// same instruction, keeping its calibration set comparable to production
// moderate-text output.
const ModerateSystemPrompt = `You are a content-safety classifier for a community platform.
Judge the user message for policy violations (abuse/harassment, spam, illegal content, sexual content involving minors, or other clearly harmful content).
Respond with ONLY a JSON object, no prose, of the form:
{"flagged": <bool>, "categories": [<string>, ...], "score": <number between 0 and 1>}
"flagged" is true only when the message clearly violates policy; "score" is your confidence that it violates policy.`

// moderateMaxTokens caps the reply. The verdict object itself is ~40 tokens, so
// this looks generous — but it is a CEILING, not a budget: a model that answers
// in 40 tokens is billed for 40 whichever ceiling is set, so raising it costs
// nothing on the replies that already fit and rescues the ones that did not.
//
// It was 256, which silently destroyed half of all escalated verdicts between
// 2026-07-22 and 2026-08-07. Reasoning-style models (the Tier2 channel was
// @cf/zai-org/glm-5.2) emit their working before the JSON; past 256 the object
// was cut mid-token and came back as "unexpected end of JSON input", which
// fail-open turned into an allow. The evidence was unambiguous once looked at:
// successful calls had a median of 132 completion tokens, failed calls a median
// of exactly 256, with 73% pinned to the ceiling.
//
// Tighten this only against measured completion_tokens in ai_usage, never by
// eyeballing the size of the verdict — the verdict is not what fills the budget.
const moderateMaxTokens = 1024

// Moderate runs the moderate-text cascade: budget fuse → Tier1 (omni) coarse
// pass → Tier2 (LLM) fine adjudication, metering one ai_usage row per upstream
// call. It ALWAYS returns a result with a nil error (fail-open); internal
// metering/budget errors are swallowed with a warn.
//
// Cascade (spec 09 ruling 3):
//   - omni unconfigured → the Tier2 LLM path unchanged (today's behavior).
//   - omni error → meter status=upstream_error, then fall through to the LLM
//     path (LLM also unavailable → degraded there).
//   - relevantScore ≥ threshold → the LLM adjudicates as final; if the LLM is
//     unconfigured, omni convicts alone (a real verdict, NOT degraded).
//   - below threshold → clean omni verdict, unless negative sampling (only when
//     the LLM is configured) escalates it for shadow calibration.
func (s *ModerationService) Moderate(ctx context.Context, p ModerateParams) (ModerateResult, error) {
	routeName := model.RouteModerateText
	spec, _ := route.Lookup(routeName)

	// 1. Budget fuse (record-don't-block). A set cap whose current-day spend is
	// exceeded → fail-open allow, status=budget_denied. A NULL/absent cap never
	// blocks (the v0 default).
	start := time.Now()
	if s.overBudget(ctx, routeName, p.Site) {
		slog.Warn("ai moderate-text over budget — fail-open allow", "site", p.Site, "route", routeName)
		s.meter(ctx, model.AIUsage{
			Site: p.Site, Route: routeName, Status: model.StatusBudgetDenied,
			LatencyMs: msSince(start),
		})
		return failOpen(routeName, "", spec), nil
	}

	// 2. Tier1 unconfigured → run the Tier2 LLM path exactly as before.
	if !s.omni.Configured() {
		return s.moderateViaLLM(ctx, p, routeName, spec)
	}

	// 3. Tier1 coarse pass. Meter one row for the omni call (tokens 0 — the
	// moderations endpoint reports no usage; latency is the real omni round-trip).
	// A dial failure falls through to the Tier2 LLM path.
	omniStart := time.Now()
	ores, oerr := s.omni.Moderate(ctx, p.Text)
	if oerr != nil {
		slog.Warn("ai moderate-text omni error — falling through to LLM", "site", p.Site, "err", oerr)
		s.meter(ctx, model.AIUsage{
			Site: p.Site, Route: routeName, Status: model.StatusUpstreamError,
			Channel: s.omni.Model(), LatencyMs: msSince(omniStart),
		})
		return s.moderateViaLLM(ctx, p, routeName, spec)
	}
	s.meter(ctx, model.AIUsage{
		Site: p.Site, Route: routeName, Status: model.StatusOK,
		Channel: ores.Channel, LatencyMs: msSince(omniStart),
	})

	rel := relevantScore(ores.CategoryScores)

	// 4a. At/above threshold → LLM adjudicates as final; LLM unconfigured (and
	// NOT degraded — the coarse pass succeeded) → omni convicts alone.
	if rel >= s.escalateThreshold {
		if s.llm.Configured() {
			return s.moderateViaLLM(ctx, p, routeName, spec)
		}
		score := rel
		return ModerateResult{
			Route: routeName, Flagged: true, Categories: adoptedTrueCategories(ores.Categories),
			Score: &score, Channel: ores.Channel, Degraded: false,
		}, nil
	}

	// 4b. Below threshold → clean omni verdict, unless negative sampling (only
	// meaningful when the LLM is configured) escalates it for shadow calibration.
	if s.llm.Configured() && s.sampleHit() {
		return s.moderateViaLLM(ctx, p, routeName, spec)
	}
	score := rel
	return ModerateResult{
		Route: routeName, Flagged: false, Categories: nil,
		Score: &score, Channel: ores.Channel, Degraded: false,
	}, nil
}

// moderateViaLLM is the Tier2 path (today's moderate-text behavior): an
// unconfigured LLM degrades WITHOUT dialing; otherwise it dials, tolerantly
// parses, and any transport/parse failure fails open. It meters exactly one
// ai_usage row for the LLM call (or the degraded no-call).
func (s *ModerationService) moderateViaLLM(ctx context.Context, p ModerateParams, routeName string, spec route.Spec) (ModerateResult, error) {
	start := time.Now()

	// Degraded: the LLM tier is not wired. Never dial, never error.
	if !s.llm.Configured() {
		s.meter(ctx, model.AIUsage{
			Site: p.Site, Route: routeName, Status: model.StatusDegraded,
			LatencyMs: msSince(start),
		})
		return failOpen(routeName, "", spec), nil
	}

	res, err := s.llm.ChatJSON(ctx, ModerateSystemPrompt, p.Text, moderateMaxTokens)
	if err != nil {
		slog.Warn("ai moderate-text upstream error — fail-open allow", "site", p.Site, "err", err)
		s.meter(ctx, model.AIUsage{
			Site: p.Site, Route: routeName, Status: model.StatusUpstreamError,
			Channel: s.llm.Model(), LatencyMs: msSince(start),
		})
		return failOpen(routeName, "", spec), nil
	}
	v, perr := parseModeration(res.Content)
	if perr != nil {
		// Separate OUR fault from theirs. finish_reason=length means the ceiling
		// cut the reply off — a config fault that hides perfectly inside
		// "unparseable" unless it is named.
		status := model.StatusUpstreamError
		if res.FinishReason == "length" {
			status = model.StatusTruncated
			slog.Warn("ai moderate-text reply truncated at the token ceiling — fail-open allow; raise moderateMaxTokens",
				"site", p.Site, "channel", res.Channel, "max_tokens", moderateMaxTokens,
				"completion_tokens", res.CompletionTokens)
		} else {
			slog.Warn("ai moderate-text unparseable upstream reply — fail-open allow",
				"site", p.Site, "channel", res.Channel, "finish_reason", res.FinishReason, "err", perr)
		}
		s.meter(ctx, model.AIUsage{
			Site: p.Site, Route: routeName, Status: status,
			Channel: res.Channel, PromptTokens: res.PromptTokens, CompletionTokens: res.CompletionTokens,
			LatencyMs: msSince(start),
		})
		return failOpen(routeName, res.Channel, spec), nil
	}

	// Normal scored path.
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

// sampleHit reports whether a below-threshold verdict is negative-sampled into
// the LLM (shadow calibration). A rate ≤ 0 never samples; rand returns [0,1).
func (s *ModerationService) sampleHit() bool {
	if s.negativeSampleRate <= 0 {
		return false
	}
	return s.rand() < s.negativeSampleRate
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
