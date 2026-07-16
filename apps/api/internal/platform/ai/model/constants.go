// Package model holds the kun_ai schema: the per-call usage ledger (ai_usage)
// and the per-route daily budget fuse (ai_route_budget). The AI gateway never
// stores UGC (doc 20 invariant 5) — only the accounting and the fuse config.
package model

// Semantic route names. v0 ships ONLY moderate-text; the dispatch registry
// (internal/platform/ai/route) admits a new route as one table row, so the
// route name lives here as the shared constant.
const RouteModerateText = "moderate-text"

// AIUsage.Status values — the terminal state of one gateway call, metered per
// call (doc 20 §5). moderate-text is fail-open, so 1/2/4 all still returned an
// allow (flagged:false) to the caller; the status is the audit trail of WHY.
const (
	StatusOK            int16 = 0 // upstream scored the text normally
	StatusUpstreamError int16 = 1 // upstream 5xx / timeout / unparseable → fail-open allow
	StatusBudgetDenied  int16 = 2 // over the route×site daily cap → fail-open allow (v0 record-don't-block)
	StatusRateLimited   int16 = 3 // reserved: v0 has no rate limiter (budget fuse is the only soft gate)
	StatusDegraded      int16 = 4 // upstream env empty → degraded, no call dialled → fail-open allow
)
