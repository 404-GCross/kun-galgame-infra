package service

import (
	"context"
	"log/slog"
	"strconv"

	"api/internal/platform/community/repository"
	"api/pkg/trustclient"

	"gorm.io/gorm"
)

// scanSubjectKind is the trust subject_kind a scanned post carries. It is the
// SAME kind the forward face uses (forwardSubjectKind = "community_post"): the
// two signals about one subject converge naturally on the trust side.
const scanSubjectKind = forwardSubjectKind

// Scanner is the trust scan-face client, abstracted so tests fake it.
// *trustclient.Client satisfies it directly.
type Scanner interface {
	Scan(ctx context.Context, req trustclient.ScanRequest) (scanID int64, truncated bool, err error)
}

// ScanService feeds a published post's text into the trust shadow-scan pipeline
// (step 04). It is pure best-effort: no outbox, no retry (unlike forwarding) —
// scan events are shadow-calibration corpus, editing self-heals a miss, and the
// receiving face is repeatable (every edit is one fresh scan). A nil Scanner =
// scanning disabled: every path is a no-op.
type ScanService struct {
	db *gorm.DB
	sc Scanner
}

// NewScanService builds the service. sc nil = scanning off.
func NewScanService(db *gorm.DB, sc Scanner) *ScanService {
	return &ScanService{db: db, sc: sc}
}

// Enabled reports whether scanning is wired.
func (s *ScanService) Enabled() bool { return s.sc != nil }

// ScanPostBg best-effort scans a single post off the request path (goroutine-
// safe, logs on failure — a miss is harmless; the next edit re-scans).
func (s *ScanService) ScanPostBg(postID int64) {
	if err := s.scanPost(context.Background(), postID); err != nil {
		slog.Warn("community trust-scan", "post_id", postID, "err", err)
	}
}

// scanPost loads the post's site/author/raw body (and the thread title for a
// first post) and submits the scan. A missing post is a no-op.
func (s *ScanService) scanPost(ctx context.Context, postID int64) error {
	if s.sc == nil {
		return nil
	}
	tgt, found, err := repository.LoadScanTargetTx(s.db.WithContext(ctx), postID)
	if err != nil {
		return err
	}
	if !found {
		return nil
	}
	// Ruling 5: scan the author's RAW text; the first post (post_number==1) is
	// prefixed with the thread title. The 8000-rune cap belongs to the receiving
	// face — the client never pre-truncates.
	text := tgt.ContentRaw
	if tgt.PostNumber == 1 && tgt.Title != "" {
		text = tgt.Title + "\n\n" + tgt.ContentRaw
	}
	author := tgt.AuthorID
	_, _, err = s.sc.Scan(ctx, trustclient.ScanRequest{
		Site: tgt.Site, SubjectKind: scanSubjectKind, SubjectID: strconv.FormatInt(tgt.PostID, 10),
		Text: text, AuthorID: &author,
	})
	return err
}

// ScanningSink decorates an inner EventSink, turning post.created / post.edited
// into off-request trust scan calls. Every other event passes through untouched.
// Composed in cmd/community only when scanning is wired (KUN_TRUST_SCAN_ENABLED);
// the services stay unaware. It nests with ForwardingSink in any order — the two
// consume disjoint event kinds and forward the rest to their inner sink.
type ScanningSink struct {
	inner EventSink
	scan  *ScanService
}

// NewScanningSink composes a scanning decorator over an inner sink.
func NewScanningSink(inner EventSink, scan *ScanService) ScanningSink {
	return ScanningSink{inner: inner, scan: scan}
}

// Emit implements EventSink. Scan calls run off-request (goroutine) so a slow /
// down trust service never blocks the create/edit path (ruling 7 hard assertion).
func (s ScanningSink) Emit(e Event) {
	if s.inner != nil {
		s.inner.Emit(e)
	}
	if s.scan == nil || !s.scan.Enabled() {
		return
	}
	switch e.Kind {
	case EventPostCreated, EventPostEdited:
		go s.scan.ScanPostBg(e.PostID)
	}
}
