package service

import (
	"context"
	"log/slog"
	"time"

	"api/pkg/trustclient"
)

// checkTimeout bounds the SYNCHRONOUS pre-write word-list check. Unlike the scan
// sink (off-request, best-effort), the check sits on the create/edit request
// path, so it is tightly bounded: a slower or erroring trust service must never
// stall a post. On timeout the check fails OPEN (ruling 3) — the write proceeds.
const checkTimeout = 500 * time.Millisecond

// Tier0 check decisions on the wire (mirror trust service.Decision*). deny wins
// over hold; allow is the clean path.
const (
	checkAllow = "allow"
	checkDeny  = "deny"
	checkHold  = "hold"
)

// Checker is the trust check-face client, abstracted so tests fake it.
// *trustclient.Client satisfies it directly.
type Checker interface {
	Check(ctx context.Context, req trustclient.CheckRequest) (decision string, matched []string, err error)
}

// CheckService runs the synchronous Tier0 word-list gate BEFORE a community
// write commits (step 06). It is entirely fail-open: a nil checker, or any
// error/timeout, resolves to allow (with a slog.Warn) so a down/slow trust
// service never blocks posting (ruling 3). A nil *CheckService is itself a valid
// "gate off" value — Decision is nil-receiver safe — so the write services can
// hold a nil field when the gate is unwired.
type CheckService struct {
	ck Checker
}

// NewCheckService builds the gate. ck nil = the gate is off (every Decision is
// allow); main.go passes nil unless KUN_TRUST_CHECK_ENABLED is set AND the trust
// client is wired.
func NewCheckService(ck Checker) *CheckService {
	return &CheckService{ck: ck}
}

// Enabled reports whether the gate will actually dial.
func (s *CheckService) Enabled() bool { return s != nil && s.ck != nil }

// Decision runs the gate for one piece of content and returns
// checkAllow/checkDeny/checkHold. A disabled gate or any checker failure is a
// fail-open checkAllow. The 500ms timeout is applied here — the pkg client stays
// timeout-agnostic (ruling 6). The HTTP call never runs inside a DB transaction:
// callers invoke Decision strictly before opening their tx.
func (s *CheckService) Decision(ctx context.Context, site, text string, authorID *int64) string {
	if !s.Enabled() {
		return checkAllow
	}
	cctx, cancel := context.WithTimeout(ctx, checkTimeout)
	defer cancel()
	decision, _, err := s.ck.Check(cctx, trustclient.CheckRequest{Site: site, Text: text, AuthorID: authorID})
	if err != nil {
		slog.Warn("community trust-check fail-open", "site", site, "err", err)
		return checkAllow
	}
	switch decision {
	case checkDeny:
		return checkDeny
	case checkHold:
		return checkHold
	default:
		return checkAllow
	}
}
